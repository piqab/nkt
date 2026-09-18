package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"

	"golang.org/x/crypto/curve25519"

	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmnet"
)

// Режим сети «wireguard»: хосты кластера связаны туннелем, у каждого —
// своя подсеть libvirt для машин, подсети соседей доступны через
// туннель. Ключи генерирует хаб и хранит зашифрованными в записи
// кластера; на хост уходит только его приватный ключ и публичные ключи
// соседей.

const wgPort = 51820

// wgPlan — туннель целиком: по одному wgHost на хост размещения.
type wgPlan struct {
	Iface string   `json:"iface"`
	Port  int      `json:"port"`
	Hosts []wgHost `json:"hosts"`
}

type wgHost struct {
	HostID     int64  `json:"host_id"`
	Name       string `json:"name"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	Address    string `json:"address"` // адрес в сети туннеля, CIDR
	IP         string `json:"ip"`
	VMSubnet   string `json:"vm_subnet"`
	Endpoint   string `json:"endpoint"`
}

// wgIface — имя интерфейса и сети libvirt: ядро ограничивает 15
// символами, поэтому по номеру кластера, а не по имени.
func wgIface(clusterID int64) string { return fmt.Sprintf("nktwg%d", clusterID) }

// wgSubnets — сеть туннеля и подсети машин по номеру кластера и номеру
// хоста в нём: 10.200.<c>.<i>/24 и 10.<100+c>.<i>.0/24. Разные кластеры
// не пересекаются на общих хостах, пока их меньше сотни.
func wgSubnets(clusterID int64, idx int) (addr, ip, vmSubnet string) {
	c := int(clusterID % 100)
	ip = fmt.Sprintf("10.200.%d.%d", c, idx)
	return ip + "/24", ip, fmt.Sprintf("10.%d.%d.0/24", 100+c, idx)
}

// wgKeyPair — ключи Curve25519 в кодировке wg.
func wgKeyPair() (private, public string, err error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return "", "", err
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv[:]), base64.StdEncoding.EncodeToString(pub), nil
}

// buildWGPlan раздаёт хостам номера, адреса и ключи.
func buildWGPlan(clusterID int64, hosts []store.Host) (*wgPlan, error) {
	p := &wgPlan{Iface: wgIface(clusterID), Port: wgPort}
	for i, h := range hosts {
		priv, pub, err := wgKeyPair()
		if err != nil {
			return nil, err
		}
		addr, ip, vm := wgSubnets(clusterID, i+1)
		p.Hosts = append(p.Hosts, wgHost{HostID: h.ID, Name: h.Name, PrivateKey: priv, PublicKey: pub,
			Address: addr, IP: ip, VMSubnet: vm, Endpoint: fmt.Sprintf("%s:%d", h.Addr, wgPort)})
	}
	return p, nil
}

func (p *wgPlan) host(id int64) *wgHost {
	for i := range p.Hosts {
		if p.Hosts[i].HostID == id {
			return &p.Hosts[i]
		}
	}
	return nil
}

// mesh — конфигурация туннеля для одного хоста: соседи со своими
// адресами и подсетями машин.
func (p *wgPlan) mesh(h wgHost) control.WGMesh {
	m := control.WGMesh{Name: p.Iface, PrivateKey: h.PrivateKey, Address: h.Address, ListenPort: p.Port, VMSubnet: h.VMSubnet}
	for _, o := range p.Hosts {
		if o.HostID == h.HostID {
			continue
		}
		m.Peers = append(m.Peers, control.WGPeer{Name: o.Name, PublicKey: o.PublicKey, Endpoint: o.Endpoint,
			AllowedIPs: []string{o.IP + "/32", o.VMSubnet}})
	}
	return m
}

// wgHostsOf — хосты размещения в порядке первого появления.
func wgHostsOf(ctx context.Context, db *store.DB, nodes []clusterNode) ([]store.Host, error) {
	var out []store.Host
	seen := map[int64]bool{}
	for _, n := range nodes {
		if seen[n.HostID] {
			continue
		}
		seen[n.HostID] = true
		h, err := db.HostByID(ctx, n.HostID)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// clusterWG расшифровывает сохранённый план.
func (m *Manager) clusterWG(cl store.Cluster) (*wgPlan, error) {
	if len(cl.WGEnc) == 0 {
		return nil, nil
	}
	raw, err := secretbox.Decrypt(m.key, cl.WGEnc)
	if err != nil {
		return nil, err
	}
	var p wgPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (m *Manager) saveClusterWG(ctx context.Context, id int64, p *wgPlan) error {
	raw, _ := json.Marshal(p)
	enc, err := secretbox.Encrypt(m.key, raw)
	if err != nil {
		return err
	}
	return m.db.SetClusterWG(ctx, id, enc)
}

// setupMesh поднимает туннель на всех хостах размещения и сеть libvirt
// для машин на каждом, затем убеждается, что соседи отвечают по адресам
// туннеля. План хранится в записи кластера: при продолжении и
// пополнении ключи те же, конфиг просто переприменяется.
func (r *ClusterRunner) setupMesh(ctx context.Context, jc *jobs.Context, cl store.Cluster, nodes []clusterNode) (*wgPlan, error) {
	plan, err := r.m.clusterWG(cl)
	if err != nil {
		return nil, err
	}
	hosts, err := wgHostsOf(ctx, r.m.db, nodes)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		if plan, err = buildWGPlan(cl.ID, hosts); err != nil {
			return nil, err
		}
		if err := r.m.saveClusterWG(ctx, cl.ID, plan); err != nil {
			return nil, err
		}
		jc.Log("hub.clusterMeshPlanned", plan.Iface, len(plan.Hosts))
	}
	// Хост, которого в плане нет (пополнение на новом хосте) — добавить.
	changed := false
	for _, h := range hosts {
		if plan.host(h.ID) != nil {
			continue
		}
		priv, pub, err := wgKeyPair()
		if err != nil {
			return nil, err
		}
		addr, ip, vm := wgSubnets(cl.ID, len(plan.Hosts)+1)
		plan.Hosts = append(plan.Hosts, wgHost{HostID: h.ID, Name: h.Name, PrivateKey: priv, PublicKey: pub,
			Address: addr, IP: ip, VMSubnet: vm, Endpoint: fmt.Sprintf("%s:%d", h.Addr, plan.Port)})
		changed = true
	}
	if changed {
		if err := r.m.saveClusterWG(ctx, cl.ID, plan); err != nil {
			return nil, err
		}
	}
	for _, wh := range plan.Hosts {
		mesh := plan.mesh(wh)
		if _, err := r.m.HostAPI(ctx, wh.HostID, "POST", "/api/vm/wgmesh", mesh, nil); err != nil {
			return nil, msgs.Errorf("hub.clusterMeshApply", wh.Name, err)
		}
		jc.Log("hub.clusterMeshUp", wh.Name, wh.IP, wh.VMSubnet)
		if err := r.ensureVMNetwork(ctx, wh, plan.Iface); err != nil {
			return nil, msgs.Errorf("hub.clusterMeshNetwork", wh.Name, err)
		}
	}
	// Соседи отвечают?
	failed := 0
	for _, wh := range plan.Hosts {
		for _, o := range plan.Hosts {
			if o.HostID == wh.HostID {
				continue
			}
			ok, detail := r.hostPing(ctx, wh.HostID, o.IP)
			if ok {
				jc.Logf("  ✓ %s → %s (%s): %s", wh.Name, o.Name, o.IP, detail)
			} else {
				failed++
				jc.Logf("  ✗ %s → %s (%s): %s", wh.Name, o.Name, o.IP, detail)
			}
		}
	}
	if failed > 0 {
		return nil, msgs.Errorf("hub.clusterMeshUnreachable", failed)
	}
	return plan, nil
}

// ensureVMNetwork заводит на хосте сеть libvirt с подсетью машин
// (или поднимает существующую).
func (r *ClusterRunner) ensureVMNetwork(ctx context.Context, wh wgHost, name string) error {
	var res struct {
		Networks []vmnet.Network `json:"networks"`
	}
	if _, err := r.m.HostAPI(ctx, wh.HostID, "GET", "/api/vm/networks", nil, &res); err != nil {
		return err
	}
	for _, n := range res.Networks {
		if n.Name != name {
			continue
		}
		if !n.Active {
			_, err := r.m.HostAPI(ctx, wh.HostID, "POST", "/api/vm/networks/"+url.PathEscape(name)+"/start", nil, nil)
			return err
		}
		return nil
	}
	spec := vmnet.Spec{Name: name, Mode: vmnet.ModeNAT, Subnet: wh.VMSubnet, DHCP: true, Autostart: true}
	_, err := r.m.HostAPI(ctx, wh.HostID, "POST", "/api/vm/networks", spec, nil)
	return err
}

// hostPing — отвечает ли адрес с хоста.
func (r *ClusterRunner) hostPing(ctx context.Context, hostID int64, ip string) (bool, string) {
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		MS    int64  `json:"ms"`
	}
	if _, err := r.m.HostAPI(ctx, hostID, "GET", "/api/net/ping?ip="+url.QueryEscape(ip), nil, &res); err != nil {
		return false, err.Error()
	}
	if !res.OK {
		return false, res.Error
	}
	return true, fmt.Sprintf("%d ms", res.MS)
}

// removeMesh снимает туннель и сеть машин с каждого хоста плана.
func (r *ClusterDeleteRunner) removeMesh(ctx context.Context, jc *jobs.Context, cl store.Cluster) {
	plan, err := r.m.clusterWG(cl)
	if err != nil || plan == nil {
		return
	}
	for _, wh := range plan.Hosts {
		if _, err := r.m.HostAPI(ctx, wh.HostID, "DELETE", "/api/vm/wgmesh/"+url.PathEscape(plan.Iface), nil, nil); err != nil {
			jc.Log("hub.clusterDeleteVMWarn", wh.Name, err)
		}
		if _, err := r.m.HostAPI(ctx, wh.HostID, "POST", "/api/vm/networks/"+url.PathEscape(plan.Iface)+"/delete", nil, nil); err != nil {
			jc.Log("hub.clusterDeleteVMWarn", wh.Name, err)
		}
		jc.Log("hub.clusterMeshRemoved", wh.Name)
	}
}
