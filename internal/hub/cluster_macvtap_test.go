package hub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// macvtap: машина включается в устройство хоста напрямую, мимо мостов и
// сетей libvirt. Цена — хост не говорит со своими macvtap-гостями, и
// адрес machine сообщает только гостевой агент. Отсюда три правила, которые
// здесь и проверяются: устройство обязано быть названным для каждой строки
// с машинами (на каждом хосте своё), проброс с хоста на его же машину не
// обещается, и адрес узла хаб рекламирует сам.

func macvtapErrKey(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("ожидался отказ, принято без ошибки")
	}
	var e *msgs.Err
	if !errors.As(err, &e) {
		t.Fatalf("отказ не ключом: %T %v", err, err)
	}
	return e.Key
}

func TestValidatePlacementsMacvtap(t *testing.T) {
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", NetworkMode: NetworkMacvtap, ImageID: "ubuntu-24.04", Expose: false, Placements: []Placement{
		{HostID: 1, Role: RoleControlPlane, Kind: KindVM, Count: 1, Nic: "eth0", Bridge: "br0", Network: "default"},
		{HostID: 2, Role: RoleWorker, Kind: KindVM, Count: 2, Nic: " ens1 "},
		{HostID: 2, Role: RoleWorker, Kind: KindHost, Nic: "eth1"},
	}}
	if err := spec.Validate(); err != nil {
		t.Fatalf("верное размещение с устройствами отклонено: %v", err)
	}
	// Мост и сеть libvirt при macvtap ни при чём — они снимаются, а не
	// лежат в spec мёртвым грузом, который кто-нибудь прочтёт всерьёз.
	if spec.Placements[0].Bridge != "" || spec.Placements[0].Network != "" {
		t.Errorf("bridge/network не сняты: %+v", spec.Placements[0])
	}
	if spec.Placements[1].Nic != "ens1" {
		t.Errorf("имя устройства не обрезано: %q", spec.Placements[1].Nic)
	}
	// Узел — сам хост: в его устройство включить машину некуда.
	if spec.Placements[2].Nic != "" {
		t.Errorf("у узла-хоста осталось устройство: %q", spec.Placements[2].Nic)
	}
	if spec.HostID != 1 {
		t.Errorf("HostID — первый control plane: %d", spec.HostID)
	}

	for name, mutate := range map[string]func(*ClusterSpec){
		"машина без устройства":       func(s *ClusterSpec) { s.Placements[0].Nic = "" },
		"устройство с косой":          func(s *ClusterSpec) { s.Placements[0].Nic = "eth0/1" },
		"устройство с пробелом":       func(s *ClusterSpec) { s.Placements[0].Nic = "eth 0" },
		"проброс на хосте размещения": func(s *ClusterSpec) { s.Expose = true },
	} {
		s := spec
		s.Placements = append([]Placement(nil), spec.Placements...)
		mutate(&s)
		err := s.Validate()
		if err == nil {
			t.Errorf("%s: принято без ошибки", name)
			continue
		}
		key := macvtapErrKey(t, err)
		want := "hub.clusterNicNeeded"
		if name == "проброс на хосте размещения" {
			want = "hub.clusterExposeMacvtap"
		}
		if name == "устройство с косой" || name == "устройство с пробелом" {
			want = "hub.clusterBadNic"
		}
		if key != want {
			t.Errorf("%s: отказ %s, ожидалось %s", name, key, want)
		}
		// Отказ называет устройство там, где его выбирал человек.
		if want == "hub.clusterBadNic" && !strings.Contains(err.Error(), "eth") {
			t.Errorf("%s: в отказе нет имени устройства: %v", name, err)
		}
	}

	// Режим выбирается на каждом хосте: в топологии «на один хост» его
	// назвать негде.
	plain := ClusterSpec{Name: "lab", Flavor: "kubeadm", Topology: TopologyCP1, Workers: 1,
		ImageID: "ubuntu-24.04", NetworkMode: NetworkMacvtap, HostID: 1}
	if key := macvtapErrKey(t, plain.Validate()); key != "hub.clusterMacvtapNeedsPlacements" {
		t.Errorf("macvtap без размещений: %s", key)
	}
}

// Узлы разворачиваются из размещения — устройство доезжает до каждого
// узла своей строки: имена машин считаются по порядку строк.
func TestClusterNodesCarryNic(t *testing.T) {
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", NetworkMode: NetworkMacvtap, ImageID: "ubuntu-24.04", Placements: []Placement{
		{HostID: 1, Role: RoleControlPlane, Kind: KindVM, Count: 1, Nic: "eth0"},
		{HostID: 1, Role: RoleWorker, Kind: KindVM, Count: 1, Nic: "eth0"},
		{HostID: 2, Role: RoleWorker, Kind: KindVM, Count: 1, Nic: "ens1f0"},
	}}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	nodes := spec.nodes(func(int64) string { return "hv" })
	if len(nodes) != 3 {
		t.Fatalf("узлов: %d", len(nodes))
	}
	for i, want := range []struct {
		name string
		nic  string
		host int64
	}{{"lab-cp-1", "eth0", 1}, {"lab-w-1", "eth0", 1}, {"lab-w-2", "ens1f0", 2}} {
		if nodes[i].Name != want.name || nodes[i].Nic != want.nic || nodes[i].HostID != want.host {
			t.Errorf("узел %d: %+v, ожидалось %s на хосте %d в %s", i, nodes[i], want.name, want.host, want.nic)
		}
	}
}

// Кому узел обязан назвать свой адрес: машине в macvtap — тот, что
// сообщил гостевой агент, узлу-хосту в туннеле — адрес в туннеле, остальным
// ничего (пусть берут сами, как умели до этого режима).
func TestNodeIPAdvertise(t *testing.T) {
	vm := store.Host{ID: 10, Addr: "192.168.122.115", ParentID: 3}
	hv := store.Host{ID: 12, Addr: "10.0.0.5", ParentID: 0}
	cases := []struct {
		name       string
		h          store.Host
		tunnelAddr string
		macvtap    bool
		want       string
	}{
		{"macvtap-машина: адрес гостевого агента", vm, vm.Addr, true, "192.168.122.115"},
		{"macvtap-машина без адреса", store.Host{ID: 11, ParentID: 3}, "", true, ""},
		{"обычная машина в NAT: не лезем", vm, vm.Addr, false, ""},
		{"хост-узел в туннеле: адрес туннеля", hv, "10.8.0.2", false, "10.8.0.2"},
		{"хост-узел не в туннеле: не лезем", hv, hv.Addr, false, ""},
		{"хост-узел в туннеле и с устройством: туннель важнее", hv, "10.8.0.2", true, "10.8.0.2"},
	}
	for _, c := range cases {
		if got := nodeIPAdvertise(c.h, c.tunnelAddr, c.macvtap); got != c.want {
			t.Errorf("%s: %q, ожидалось %q", c.name, got, c.want)
		}
	}
}

// Пригодность устройства хаб пересказывает кодом, который прислал агент, —
// своих догадок тут быть не должно: тот же вердикт, то же слово в чек-листе.
func TestPreflightNicChecks(t *testing.T) {
	nodes := []clusterNode{
		{Name: "lab-cp-1", Kind: KindVM, Nic: "eth0"},
		{Name: "lab-w-1", Kind: KindVM, Nic: "eth0"},
		{Name: "lab-w-2", Kind: KindVM, Nic: "eth9"},
		{Name: "hv2", Kind: KindHost},
		{Name: "lab-w-3", Kind: KindVM},
	}
	// Агент старше проверки: не отказ, предупреждение — создание машины
	// проверит устройство на месте.
	for _, c := range preflightNicChecks(hostPreflight{}, nodes) {
		if !c.ok || !c.warn || c.key != "hub.preflightNicUnknown" {
			t.Errorf("агент без direct: %+v", c)
		}
	}
	// Одно устройство на двух машинах проверяется один раз.
	pf := hostPreflight{Direct: map[string]string{
		"eth0": vmcreate.DeviceOK,
		"eth1": vmcreate.DeviceDown,
		"eth2": vmcreate.DeviceMacvlan,
		"lo":   vmcreate.DeviceLoopback,
	}}
	out := preflightNicChecks(pf, nodes)
	if len(out) != 2 {
		t.Fatalf("пунктов: %d (%+v)", len(out), out)
	}
	if out[0].key != "hub.preflightNicOK" || !out[0].ok || out[0].warn || out[0].args[0] != "eth0" {
		t.Errorf("годное устройство: %+v", out[0])
	}
	if out[1].key != "hub.preflightNicMissing" || out[1].ok || out[1].args[0] != "eth9" {
		t.Errorf("устройства нет: %+v", out[1])
	}
	// Каждый вердикт агента имеет свой пункт: опущенное, loopback и
	// чужой macvtap различимы для человека, который выбирает интерфейс.
	for dev, want := range map[string]string{"eth1": "hub.preflightNicDown", "eth2": "hub.preflightNicBad", "lo": "hub.preflightNicBad"} {
		got := preflightNicChecks(hostPreflight{Direct: map[string]string{dev: pf.Direct[dev]}},
			[]clusterNode{{Name: "lab-cp-1", Kind: KindVM, Nic: dev}})
		if len(got) != 1 || got[0].key != want || got[0].ok {
			t.Errorf("%s: %+v, ожидалось %s", dev, got, want)
		}
		line := (&msgs.Err{Key: got[0].key, Args: got[0].args}).Error()
		if !strings.Contains(line, dev) {
			t.Errorf("%s: в пункте нет имени устройства: %s", dev, line)
		}
	}
}

// Слушатель общего адреса на хосте, который размещает macvtap-машины,
// бессмысленен: хост с ними не говорит. Это пропуск с объяснением, а не
// отказ всему заданию, и он касается только того хоста, где стоит шлюз.
func TestClusterListenerDecideMacvtap(t *testing.T) {
	ctx := context.Background()
	s, gwEdge, hosts := listenerFixture(t, "8.8.8.8", "9.9.9.9")
	r := NewClusterRunner(s)
	// Шлюз стоит на первом хосте — его размещение и решает.
	if _, err := s.putEdge(ctx, EdgeSettings{ID: gwEdge, Enabled: true,
		Address: "203.0.113.10:8444", Roles: []string{EdgeRoleFly}, HostID: hosts[0]}); err != nil {
		t.Fatal(err)
	}
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Endpoint: "203.0.113.10", NetworkMode: NetworkMacvtap, Placements: []Placement{
		{HostID: hosts[0], Role: RoleControlPlane, Kind: KindVM, Count: 1, Nic: "eth0"},
	}}
	if d := r.clusterListenerDecide(ctx, spec); d.need || d.key != "hub.clusterListenerSkipMacvtapHost" {
		t.Errorf("шлюз на хосте с macvtap-машинами: %+v", d)
	}
	// Узлы на другом хосте — слушать по-прежнему есть где.
	other := spec
	other.Placements = append([]Placement(nil), spec.Placements...)
	other.Placements[0].HostID = hosts[1]
	if d := r.clusterListenerDecide(ctx, other); !d.need {
		t.Errorf("macvtap на другом хосте: %+v", d)
	}
	// Узел-хост в macvtap не превращается в запрет: устройства у него нет.
	hostNode := spec
	hostNode.Placements = []Placement{{HostID: hosts[0], Role: RoleControlPlane, Kind: KindHost}}
	if d := r.clusterListenerDecide(ctx, hostNode); !d.need {
		t.Errorf("узел — сам хост шлюза: %+v", d)
	}
	// Тот же хост в режиме моста: хост со своими мостовыми гостями говорит.
	bridged := spec
	bridged.NetworkMode = NetworkBridge
	bridged.Placements = []Placement{{HostID: hosts[0], Role: RoleControlPlane, Kind: KindVM, Count: 1, Bridge: "br0"}}
	if d := r.clusterListenerDecide(ctx, bridged); !d.need {
		t.Errorf("режим моста на том же хосте: %+v", d)
	}
}
