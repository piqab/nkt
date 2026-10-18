package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// Traffic Fly на хабе: конфигурация (маршруты, пулы, цели) — один
// документ с версиями; хаб раскладывает её по шлюзам (edge с ролью fly,
// по туннелю edge) и хостам (по API хоста) и собирает сводку для карты.
// Работает проброс без хаба: шлюзы и хосты хранят свою часть сами.

const (
	flyDocKey      = "hub.fly.doc"
	flyVersionsKey = "hub.fly.versions"
	flyAppliedKey  = "hub.fly.applied"
	flyMaxVersions = 50
	flyPortBase    = 40001
	flyDefaultWG   = 51820
)

// FlyGateway — шлюз в документе: edge и номер подсети туннеля 10.77.N.0/24.
type FlyGateway struct {
	EdgeID     int64 `json:"edge_id" yaml:"edge_id"`
	Index      int   `json:"index" yaml:"index"`
	ListenPort int   `json:"listen_port" yaml:"listen_port"`
}

// FlyTarget — цель пула: что на хосте.
type FlyTarget struct {
	ID     string `json:"id" yaml:"id"`
	Label  string `json:"label,omitempty" yaml:"label,omitempty"`
	HostID int64  `json:"host_id" yaml:"host_id"`
	// Kind — container (Docker), vm (libvirt), lxd, host (сам хост), addr
	// (адрес, видимый с хоста).
	Kind   string `json:"kind" yaml:"kind"`
	Ref    string `json:"ref,omitempty" yaml:"ref,omitempty"`
	Port   int    `json:"port" yaml:"port"`
	Weight int    `json:"weight,omitempty" yaml:"weight,omitempty"`
	Drain  bool   `json:"drain,omitempty" yaml:"drain,omitempty"`
}

// FlyRoute — маршрут: шлюз, имя или порт, пул.
type FlyRoute struct {
	ID      string   `json:"id" yaml:"id"`
	Label   string   `json:"label,omitempty" yaml:"label,omitempty"`
	Gateway int64    `json:"gateway" yaml:"gateway"`
	Kind    string   `json:"kind" yaml:"kind"`
	Names   []string `json:"names,omitempty" yaml:"names,omitempty"`
	Port    int      `json:"port" yaml:"port"`
	// PortTo — конец диапазона портов (TCP/UDP): порт сохраняется
	// насквозь, цель — одна активная (первая живая).
	PortTo        int  `json:"port_to,omitempty" yaml:"port_to,omitempty"`
	ProxyProtocol bool `json:"proxy_protocol,omitempty" yaml:"proxy_protocol,omitempty"`
	// TLS — сертификат на шлюзе: цели — HTTP с адресом клиента в заголовках.
	TLS       bool        `json:"tls,omitempty" yaml:"tls,omitempty"`
	Algorithm string      `json:"algorithm" yaml:"algorithm"`
	Check     fly.Check   `json:"check" yaml:"check"`
	Targets   []FlyTarget `json:"targets" yaml:"targets"`
}

// FlyAlloc — постоянные номера: хост в подсети шлюза, служебный порт
// цели на хосте (меняются только с удалением).
type FlyAlloc struct {
	Hosts map[string]int `json:"hosts" yaml:"-"`
	Ports map[string]int `json:"ports" yaml:"-"`
}

// FlyDoc — конфигурация Traffic Fly.
type FlyDoc struct {
	Revision int64        `json:"revision" yaml:"-"`
	Gateways []FlyGateway `json:"gateways" yaml:"gateways"`
	Routes   []FlyRoute   `json:"routes" yaml:"routes"`
	Alloc    FlyAlloc     `json:"alloc" yaml:"-"`
}

// FlyVersion — запись истории.
type FlyVersion struct {
	N    int64  `json:"n"`
	TS   string `json:"ts"`
	User string `json:"user"`
	Note string `json:"note,omitempty"`
	Doc  FlyDoc `json:"doc"`
}

var (
	flyRefRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	flyIDRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	flyLabelRe = regexp.MustCompile(`^[^\x00-\x1f"\\]{0,80}$`)
)

func flyHostKey(gw, host int64) string { return fmt.Sprintf("%d/%d", gw, host) }

func flyPortKey(gw, host int64, route, target string) string {
	return fmt.Sprintf("%d/%d/%s/%s", gw, host, route, target)
}

// flyIface — интерфейс хоста к шлюзу с номером подсети index.
func flyIface(index int) string { return "nkt-fly" + strconv.Itoa(index) }

// Text — документ для диффа: шлюзы и маршруты в YAML.
func (d FlyDoc) Text() string {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(struct {
		Gateways []FlyGateway `yaml:"gateways"`
		Routes   []FlyRoute   `yaml:"routes"`
	}{d.Gateways, d.Routes})
	return b.String()
}

// normalize — шлюзы маршрутов в списке (с номером подсети и портом),
// постоянные номера хостов и служебных портов, лишние номера — прочь.
func (d *FlyDoc) normalize() {
	used := map[int64]bool{}
	for _, r := range d.Routes {
		used[r.Gateway] = true
	}
	taken := map[int]bool{}
	var gws []FlyGateway
	for _, g := range d.Gateways {
		if used[g.EdgeID] && !taken[g.Index] && g.Index >= 1 && g.Index <= 250 {
			if g.ListenPort == 0 {
				g.ListenPort = flyDefaultWG
			}
			taken[g.Index] = true
			gws = append(gws, g)
			delete(used, g.EdgeID)
		}
	}
	var rest []int64
	for id := range used {
		rest = append(rest, id)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i] < rest[j] })
	for _, id := range rest {
		idx := 1
		for taken[idx] {
			idx++
		}
		taken[idx] = true
		gws = append(gws, FlyGateway{EdgeID: id, Index: idx, ListenPort: flyDefaultWG})
	}
	sort.Slice(gws, func(i, j int) bool { return gws[i].Index < gws[j].Index })
	d.Gateways = gws

	hosts, ports := map[string]int{}, map[string]int{}
	keepHost, keepPort := map[string]bool{}, map[string]bool{}
	for _, r := range d.Routes {
		for _, t := range r.Targets {
			keepHost[flyHostKey(r.Gateway, t.HostID)] = true
			keepPort[flyPortKey(r.Gateway, t.HostID, r.ID, t.ID)] = true
		}
	}
	for k, v := range d.Alloc.Hosts {
		if keepHost[k] {
			hosts[k] = v
		}
	}
	for k, v := range d.Alloc.Ports {
		if keepPort[k] {
			ports[k] = v
		}
	}
	for _, r := range d.Routes {
		for _, t := range r.Targets {
			hk := flyHostKey(r.Gateway, t.HostID)
			if _, ok := hosts[hk]; !ok {
				busy := map[int]bool{}
				for k, v := range hosts {
					if strings.HasPrefix(k, fmt.Sprint(r.Gateway, "/")) {
						busy[v] = true
					}
				}
				n := 2
				for busy[n] {
					n++
				}
				hosts[hk] = n
			}
			pk := flyPortKey(r.Gateway, t.HostID, r.ID, t.ID)
			if _, ok := ports[pk]; !ok {
				busy := map[int]bool{}
				for k, v := range ports {
					if strings.HasPrefix(k, fmt.Sprint(r.Gateway, "/", t.HostID, "/")) {
						busy[v] = true
					}
				}
				p := flyPortBase
				for busy[p] {
					p++
				}
				ports[pk] = p
			}
		}
	}
	d.Alloc = FlyAlloc{Hosts: hosts, Ports: ports}
}

func (d FlyDoc) gateway(edgeID int64) (FlyGateway, bool) {
	for _, g := range d.Gateways {
		if g.EdgeID == edgeID {
			return g, true
		}
	}
	return FlyGateway{}, false
}

// hostAddr — адрес хоста в туннеле шлюза.
func (d FlyDoc) hostAddr(g FlyGateway, hostID int64) string {
	return fmt.Sprintf("10.77.%d.%d", g.Index, d.Alloc.Hosts[flyHostKey(g.EdgeID, hostID)])
}

// gatewayConfig — часть документа для шлюза: ключи (keys: id хоста →
// публичный ключ; нет — заглушка для проверки формы) и имена хостов.
func (d FlyDoc) gatewayConfig(g FlyGateway, keys map[int64]string, names map[int64]string) fly.GatewayConfig {
	cfg := fly.GatewayConfig{Revision: d.Revision, Iface: "nkt-fly", Address: fmt.Sprintf("10.77.%d.1/24", g.Index), ListenPort: g.ListenPort}
	seen := map[int64]bool{}
	for _, r := range d.Routes {
		if r.Gateway != g.EdgeID {
			continue
		}
		fr := fly.Route{ID: r.ID, Kind: r.Kind, Names: r.Names, Port: r.Port, PortTo: r.PortTo, ProxyProtocol: r.ProxyProtocol, TLS: r.TLS, Algorithm: r.Algorithm, Check: r.Check}
		for _, t := range r.Targets {
			if !seen[t.HostID] {
				seen[t.HostID] = true
				key := keys[t.HostID]
				if key == "" {
					key = flyDummyKey
				}
				cfg.Peers = append(cfg.Peers, fly.Peer{Name: names[t.HostID], PublicKey: key, Address: d.hostAddr(g, t.HostID)})
			}
			port := d.Alloc.Ports[flyPortKey(g.EdgeID, t.HostID, r.ID, t.ID)]
			if r.PortTo > r.Port {
				// Диапазон: на хосте те же порты, что на шлюзе.
				port = r.Port
			}
			fr.Targets = append(fr.Targets, fly.Target{ID: t.ID, Label: t.Label, Host: d.hostAddr(g, t.HostID),
				Port: port, Weight: t.Weight, Drain: t.Drain})
		}
		cfg.Routes = append(cfg.Routes, fr)
	}
	return cfg
}

// flyDummyKey — ключ-заглушка: проверка формы конфигурации до того, как
// хосты прислали свои ключи.
const flyDummyKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// validate — документ: шлюзы — edge с ролью fly, цели — существующие
// хосты, ссылки и адреса — допустимые; форма каждой части шлюза — как её
// проверит сам шлюз.
func (s *Server) flyValidate(ctx context.Context, d FlyDoc) error {
	edges := map[int64]EdgeSettings{}
	for _, e := range s.edges(ctx) {
		edges[e.ID] = e
	}
	hosts := map[int64]bool{localHostID: true}
	if list, err := s.db.ListHosts(ctx); err == nil {
		for _, h := range list {
			hosts[h.ID] = true
		}
	}
	ids := map[string]bool{}
	for _, r := range d.Routes {
		if !flyIDRe.MatchString(r.ID) || ids[r.ID] || !flyLabelRe.MatchString(r.Label) {
			return msgs.Errorf("fly.badRoute", r.ID)
		}
		ids[r.ID] = true
		e, ok := edges[r.Gateway]
		if !ok || !e.Has(EdgeRoleFly) {
			return msgs.Errorf("fly.badGateway", r.ID)
		}
		for _, t := range r.Targets {
			if !hosts[t.HostID] || !flyLabelRe.MatchString(t.Label) {
				return msgs.Errorf("fly.badTarget", r.ID, t.ID)
			}
			switch t.Kind {
			case "container", "vm", "lxd":
				if !flyRefRe.MatchString(t.Ref) {
					return msgs.Errorf("fly.badTarget", r.ID, t.ID)
				}
			case "host":
			case "addr":
				a, err := netip.ParseAddr(t.Ref)
				if err != nil || !a.Is4() || a.IsLoopback() || a.IsUnspecified() {
					return msgs.Errorf("fly.badTarget", r.ID, t.ID)
				}
			default:
				return msgs.Errorf("fly.badTarget", r.ID, t.ID)
			}
			if t.Port < 1 || t.Port > 65535 {
				return msgs.Errorf("fly.badTarget", r.ID, t.ID)
			}
		}
	}
	for _, g := range d.Gateways {
		if err := d.gatewayConfig(g, nil, map[int64]string{}).Validate(); err != nil {
			return msgs.Errorf("fly.badConfig", err.Error())
		}
	}
	return nil
}

// flyDoc — текущий документ.
func (s *Server) flyDoc(ctx context.Context) FlyDoc {
	var d FlyDoc
	if raw, ok, err := s.db.KVGet(ctx, flyDocKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	if d.Routes == nil {
		d.Routes = []FlyRoute{}
	}
	if d.Gateways == nil {
		d.Gateways = []FlyGateway{}
	}
	return d
}

func (s *Server) flyVersions(ctx context.Context) []FlyVersion {
	var v []FlyVersion
	if raw, ok, err := s.db.KVGet(ctx, flyVersionsKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &v)
	}
	return v
}

var flyMu sync.Mutex

// flySave — новая версия документа (прежняя — в историю).
func (s *Server) flySave(ctx context.Context, d FlyDoc, user, note string) (FlyDoc, error) {
	flyMu.Lock()
	defer flyMu.Unlock()
	cur := s.flyDoc(ctx)
	d.Alloc = cur.Alloc
	d.normalize()
	if err := s.flyValidate(ctx, d); err != nil {
		return d, err
	}
	d.Revision = cur.Revision + 1
	raw, _ := json.Marshal(d)
	vs := append([]FlyVersion{{N: d.Revision, TS: time.Now().UTC().Format(time.RFC3339), User: user, Note: note, Doc: d}}, s.flyVersions(ctx)...)
	if len(vs) > flyMaxVersions {
		vs = vs[:flyMaxVersions]
	}
	rawV, _ := json.Marshal(vs)
	if err := s.db.KVSet(ctx, flyDocKey, string(raw)); err != nil {
		return d, err
	}
	return d, s.db.KVSet(ctx, flyVersionsKey, string(rawV))
}

// flySession — туннель к шлюзу (edge с ролью fly).
func (s *Server) flySession(ctx context.Context, edgeID int64) (EdgeSettings, *yamux.Session, bool) {
	if s.edge == nil {
		return EdgeSettings{}, nil, false
	}
	for _, st := range s.edges(ctx) {
		if st.ID != edgeID || !st.Has(EdgeRoleFly) {
			continue
		}
		s.edge.mu.Lock()
		c := s.edge.clients[st.ID]
		s.edge.mu.Unlock()
		if c == nil {
			return st, nil, false
		}
		c.mu.Lock()
		sess := c.sess
		c.mu.Unlock()
		return st, sess, sess != nil && !sess.IsClosed()
	}
	return EdgeSettings{}, nil, false
}

// flyGatewayCall — команда шлюзу по туннелю edge.
func flyGatewayCall(ctx context.Context, sess *yamux.Session, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	client := &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{
		DialContext:       func(context.Context, string, string) (net.Conn, error) { return sess.Open() },
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequestWithContext(ctx, method, "http://edge"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if resp.StatusCode == http.StatusNotFound && e.Error == "" {
			// Ответил не nkt-fly: у edge нет роли fly, edge старый или
			// порт команд занят чужой службой.
			return msgs.Errorf("fly.gatewayNoFly")
		}
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		return msgs.Errorf("fly.gatewayFailed", fmt.Sprintf("HTTP %d %s", resp.StatusCode, e.Error))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// flyEndpoint — публичный IPv4 шлюза (адрес edge без порта туннеля).
func flyEndpoint(ctx context.Context, st EdgeSettings, port int) (string, error) {
	if port <= 0 || port > 65535 {
		return "", msgs.Errorf("fly.badListenPort", port)
	}
	p := uint16(port) // #nosec G115 -- 1…65535 проверено выше
	host, _, err := net.SplitHostPort(st.Address)
	if err != nil {
		host = st.Address
	}
	if a, err := netip.ParseAddr(host); err == nil && a.Is4() {
		return netip.AddrPortFrom(a, p).String(), nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return "", msgs.Errorf("fly.noEndpoint", host)
	}
	return netip.AddrPortFrom(ips[0].Unmap(), p).String(), nil
}

// flyResolve — адрес цели, как его видит хост: IP контейнера, машины,
// инстанса; host — свой адрес в туннеле; addr — как есть.
func (s *Server) flyResolve(ctx context.Context, user string, t FlyTarget, tunnelAddr string, cache map[string]any) (string, error) {
	get := func(path string, out any) error {
		k := fmt.Sprint(t.HostID, path)
		if v, ok := cache[k]; ok {
			raw, _ := json.Marshal(v)
			return json.Unmarshal(raw, out)
		}
		var raw json.RawMessage
		if _, err := s.hostCall(ctx, user, t.HostID, "GET", path, nil, &raw); err != nil {
			return err
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		cache[k] = v
		return json.Unmarshal(raw, out)
	}
	switch t.Kind {
	case "host":
		return tunnelAddr, nil
	case "addr":
		return t.Ref, nil
	case "container":
		var res struct {
			Containers []model.Container `json:"containers"`
		}
		if err := get("/api/containers", &res); err != nil {
			return "", err
		}
		for _, c := range res.Containers {
			if c.Name == t.Ref {
				for _, n := range c.Networks {
					if a, err := netip.ParseAddr(n.IPAddress); err == nil && a.Is4() {
						return a.String(), nil
					}
				}
			}
		}
	case "vm":
		var res struct {
			VMs []model.VirtualMachine `json:"vms"`
		}
		if err := get("/api/vms", &res); err != nil {
			return "", err
		}
		for _, v := range res.VMs {
			if v.Name == t.Ref {
				for _, n := range v.Networks {
					if a, err := netip.ParseAddr(n.IP); err == nil && a.Is4() {
						return a.String(), nil
					}
				}
			}
		}
	case "lxd":
		var res struct {
			Instances []model.LXDInstance `json:"instances"`
		}
		if err := get("/api/lxd/instances", &res); err != nil {
			return "", err
		}
		for _, i := range res.Instances {
			if i.Name == t.Ref {
				for _, ip := range i.IPv4 {
					if a, err := netip.ParseAddr(strings.Fields(ip + " ")[0]); err == nil && a.Is4() {
						return a.String(), nil
					}
				}
			}
		}
	}
	return "", msgs.Errorf("fly.targetNoIP", t.Kind, t.Ref)
}

// flyProtos — протоколы служебного порта по виду маршрута.
func flyProtos(kind string) []string {
	switch kind {
	case fly.KindUDP:
		return []string{"udp"}
	case fly.KindTCPUDP:
		return []string{"tcp", "udp"}
	}
	return []string{"tcp"}
}
