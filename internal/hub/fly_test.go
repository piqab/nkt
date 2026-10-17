package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/store"
)

func flyTestServer(t *testing.T) (*Server, int64, int64) {
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db}
	ctx := context.Background()
	gw, err := s.putEdge(ctx, EdgeSettings{Enabled: true, Address: "203.0.113.10:8444", Roles: []string{EdgeRoleFly}})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.putEdge(ctx, EdgeSettings{Enabled: true, Address: "203.0.113.11:8444", Domain: "hooks.example.com", Roles: []string{EdgeRoleHooks}})
	h, err := db.CreateHost(ctx, "web-1", "192.0.2.5", 22, "root", store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	return s, gw.ID, h
}

func TestFlyDocSaveAndAlloc(t *testing.T) {
	s, gw, host := flyTestServer(t)
	ctx := context.Background()
	routes := []FlyRoute{
		{ID: "app", Gateway: gw, Kind: fly.KindHTTPS, Names: []string{"app.example.com"}, Port: 443, ProxyProtocol: true, Algorithm: fly.AlgoLeastConn,
			Check: fly.Check{Kind: fly.CheckTCP}, Targets: []FlyTarget{{ID: "a", HostID: host, Kind: "container", Ref: "app", Port: 8080}, {ID: "b", HostID: localHostID, Kind: "host", Port: 8080}}},
		{ID: "mc", Gateway: gw, Kind: fly.KindTCPUDP, Port: 25565, Algorithm: fly.AlgoSourceHash, Check: fly.Check{Kind: fly.CheckTCP},
			Targets: []FlyTarget{{ID: "m", HostID: host, Kind: "vm", Ref: "mc-1", Port: 25565}}},
	}
	d, err := s.flySave(ctx, FlyDoc{Routes: routes}, "admin", "first")
	if err != nil {
		t.Fatal(err)
	}
	if d.Revision != 1 || len(d.Gateways) != 1 || d.Gateways[0].Index != 1 || d.Gateways[0].ListenPort != 51820 {
		t.Fatalf("%+v", d.Gateways)
	}
	hk := flyHostKey(gw, host)
	if d.Alloc.Hosts[hk] != 2 || d.Alloc.Hosts[flyHostKey(gw, localHostID)] != 3 {
		t.Fatalf("номера хостов: %v", d.Alloc.Hosts)
	}
	pa, pm := d.Alloc.Ports[flyPortKey(gw, host, "app", "a")], d.Alloc.Ports[flyPortKey(gw, host, "mc", "m")]
	if pa == pm || pa < flyPortBase || pm < flyPortBase {
		t.Fatalf("порты: %v", d.Alloc.Ports)
	}
	// Правка (удалили цель b, добавили маршрут): прежние номера на месте.
	routes[0].Targets = routes[0].Targets[:1]
	routes = append(routes, FlyRoute{ID: "ssh", Gateway: gw, Kind: fly.KindTCP, Port: 2222, Algorithm: fly.AlgoRoundRobin, Check: fly.Check{Kind: fly.CheckNone},
		Targets: []FlyTarget{{ID: "s", HostID: host, Kind: "host", Port: 22}}})
	d2, err := s.flySave(ctx, FlyDoc{Routes: routes}, "admin", "second")
	if err != nil {
		t.Fatal(err)
	}
	if d2.Alloc.Hosts[hk] != 2 || d2.Alloc.Ports[flyPortKey(gw, host, "app", "a")] != pa || d2.Alloc.Ports[flyPortKey(gw, host, "mc", "m")] != pm {
		t.Fatalf("номера сдвинулись: %v %v", d2.Alloc.Hosts, d2.Alloc.Ports)
	}
	if _, ok := d2.Alloc.Hosts[flyHostKey(gw, localHostID)]; ok {
		t.Fatal("номер удалённого хоста остался")
	}
	cfg := d2.gatewayConfig(d2.Gateways[0], map[int64]string{}, map[int64]string{host: "web-1"})
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Address != "10.77.1.1/24" || len(cfg.Peers) != 1 || cfg.Peers[0].Address != "10.77.1.2" || cfg.Routes[0].Targets[0].Port != pa {
		t.Fatalf("%+v", cfg)
	}
	if vs := s.flyVersions(ctx); len(vs) != 2 || vs[0].N != 2 || vs[1].Note != "first" {
		t.Fatalf("история: %+v", vs)
	}
	if !strings.Contains(d2.Text(), "app.example.com") || strings.Contains(d2.Text(), "alloc") {
		t.Fatalf("текст:\n%s", d2.Text())
	}
}

func TestFlyValidate(t *testing.T) {
	s, gw, host := flyTestServer(t)
	ctx := context.Background()
	hooksEdge := int64(0)
	for _, e := range s.edges(ctx) {
		if e.Has(EdgeRoleHooks) {
			hooksEdge = e.ID
		}
	}
	base := func() FlyRoute {
		return FlyRoute{ID: "x", Gateway: gw, Kind: fly.KindTCP, Port: 2222, Algorithm: fly.AlgoRoundRobin, Check: fly.Check{Kind: fly.CheckNone},
			Targets: []FlyTarget{{ID: "t", HostID: host, Kind: "host", Port: 22}}}
	}
	bad := []func(*FlyRoute){
		func(r *FlyRoute) { r.Gateway = hooksEdge },     // не шлюз fly
		func(r *FlyRoute) { r.Targets[0].HostID = 999 }, // нет хоста
		func(r *FlyRoute) { r.Targets[0].Kind = "container"; r.Targets[0].Ref = "a b" },
		func(r *FlyRoute) { r.Targets[0].Kind = "addr"; r.Targets[0].Ref = "127.0.0.1" },
		func(r *FlyRoute) { r.ID = "Bad ID" },
		func(r *FlyRoute) { r.Kind = fly.KindHTTPS },                                        // https без имён
		func(r *FlyRoute) { r.Kind = fly.KindUDP; r.Check = fly.Check{Kind: fly.CheckTCP} }, // UDP без порта проверки
	}
	for i, f := range bad {
		r := base()
		f(&r)
		if _, err := s.flySave(ctx, FlyDoc{Routes: []FlyRoute{r}}, "admin", ""); err == nil {
			t.Errorf("случай %d принят", i)
		}
	}
	if _, err := s.flySave(ctx, FlyDoc{Routes: []FlyRoute{base()}}, "admin", ""); err != nil {
		t.Fatal(err)
	}
}

func TestFlyTargetsKindAndCache(t *testing.T) {
	s, _, host := flyTestServer(t)
	get := func(q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleFlyTargets(rec, httptest.NewRequest(http.MethodGet, "/hub/fly/targets?"+q, nil))
		return rec
	}
	if rec := get(fmt.Sprintf("host_id=%d&kind=docker", host)); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: %d", rec.Code)
	}
	// Хост не отвечает — пустой список, и неудача не кэшируется.
	if rec := get(fmt.Sprintf("host_id=%d&kind=container", host)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"targets":[]`) {
		t.Fatalf("unreachable host: %d %s", rec.Code, rec.Body)
	}
	flyTargetsCache.Lock()
	_, cached := flyTargetsCache.m[fmt.Sprintf("%d/container", host)]
	flyTargetsCache.Unlock()
	if cached {
		t.Fatal("failure cached")
	}
}

func TestFlyTunnelStates(t *testing.T) {
	s, gw, host := flyTestServer(t)
	ctx := context.Background()
	d, err := s.flySave(ctx, FlyDoc{Routes: []FlyRoute{{ID: "mc", Gateway: gw, Kind: fly.KindTCP, Port: 25565, Algorithm: fly.AlgoSourceHash,
		Check: fly.Check{Kind: fly.CheckTCP}, Targets: []FlyTarget{{ID: "m", HostID: host, Kind: "host", Port: 25565}}}}}, "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	state := func(list []flyGatewayStatusJSON) string {
		ts := s.flyTunnels(ctx, "admin", list)
		if len(ts) != 1 {
			t.Fatalf("tunnels: %+v", ts)
		}
		return ts[0].State
	}
	if got := state(nil); got != "not_applied" {
		t.Fatalf("before apply: %s", got)
	}
	raw, _ := json.Marshal(flyApplied{Revision: d.Revision, Hosts: []int64{host}, Gateways: []int64{gw}})
	_ = s.db.KVSet(ctx, flyAppliedKey, string(raw))
	if got := state([]flyGatewayStatusJSON{{EdgeID: gw}}); got != "unknown" {
		t.Fatalf("no gateway summary: %s", got)
	}
	now := time.Now().Unix()
	st := &fly.GatewayStatus{Now: now}
	if got := state([]flyGatewayStatusJSON{{EdgeID: gw, Status: st}}); got != "gateway_missing" {
		t.Fatalf("no peer: %s", got)
	}
	addr := d.hostAddr(d.Gateways[0], host)
	st.Peers = []fly.PeerStats{{Address: addr, Handshake: now - 10}}
	if got := state([]flyGatewayStatusJSON{{EdgeID: gw, Status: st}}); got != "up" {
		t.Fatalf("fresh handshake: %s", got)
	}
	// Рукопожатия нет, а хост не отвечает — сводки хоста нет, остаётся
	// no_handshake.
	st.Peers = []fly.PeerStats{{Address: addr}}
	if got := state([]flyGatewayStatusJSON{{EdgeID: gw, Status: st}}); got != "no_handshake" {
		t.Fatalf("no handshake: %s", got)
	}
}

func TestFlyVersionAtLeast(t *testing.T) {
	min := [3]int{1, 13, 25}
	for v, want := range map[string]bool{"": false, "1.13.24": false, "1.12.9": false, "1.13.25": true, "1.13.30": true, "1.14.0": true, "2.0.0": true, "dev": true} {
		if got := flyVersionAtLeast(v, min); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
}
