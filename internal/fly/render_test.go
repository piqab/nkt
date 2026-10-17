package fly

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	keyA = "YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	keyB = "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="
	keyC = "HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw="
)

func sampleGateway() GatewayConfig {
	return GatewayConfig{
		Revision: 3, Iface: "nkt-fly", Address: "10.77.1.1/24", ListenPort: 51820,
		Peers: []Peer{{Name: "web-1", PublicKey: keyA, Address: "10.77.1.2"}, {Name: "game", PublicKey: keyB, Address: "10.77.1.3"}},
		Routes: []Route{
			{ID: "app", Kind: KindHTTPS, Names: []string{"app.example.com", "*.app.example.com"}, Port: 443, ProxyProtocol: true, Algorithm: AlgoLeastConn,
				Check: Check{Kind: CheckHTTP, Path: "/healthz"}, Targets: []Target{{ID: "a1", Host: "10.77.1.2", Port: 40001}, {ID: "a2", Host: "10.77.1.3", Port: 40001, Weight: 2}}},
			{ID: "app80", Kind: KindHTTP, Names: []string{"app.example.com"}, Port: 80, Algorithm: AlgoRoundRobin, Check: Check{Kind: CheckTCP}, Targets: []Target{{ID: "a1", Host: "10.77.1.2", Port: 40003}}},
			{ID: "ssh", Kind: KindTCP, Port: 2222, Algorithm: AlgoRoundRobin, Check: Check{Kind: CheckTCP}, Targets: []Target{{ID: "s", Host: "10.77.1.2", Port: 40002}}},
			{ID: "mc", Kind: KindTCPUDP, Port: 25565, Algorithm: AlgoSourceHash, Check: Check{Kind: CheckTCP},
				Targets: []Target{{ID: "m1", Host: "10.77.1.3", Port: 40010}, {ID: "m2", Host: "10.77.1.2", Port: 40010, Weight: 2}, {ID: "m3", Host: "10.77.1.2", Port: 40011, Drain: true}}},
		},
	}
}

func sampleHost() HostIface {
	return HostIface{
		Name: "nkt-fly1", Address: "10.77.1.2/24", Gateway: Peer{Name: "gw", PublicKey: keyC, Address: "10.77.1.1"},
		Endpoint: "203.0.113.10:51820", Mark: HostMarkBase + 1, Table: HostTableBase + 1,
		Services: []Service{{Port: 40001, Proto: "tcp", Addr: "172.17.0.5", ToPort: 8080}, {Port: 40010, Proto: "udp", Addr: "192.168.122.10", ToPort: 25565}, {Port: 40010, Proto: "tcp", Addr: "10.77.1.2", ToPort: 25565}},
	}
}

func TestValidate(t *testing.T) {
	g := sampleGateway()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (HostConfig{Ifaces: []HostIface{sampleHost()}}).Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(*GatewayConfig){
		func(c *GatewayConfig) { c.Iface = "eth0" },
		func(c *GatewayConfig) { c.Routes[0].Names = []string{"app.example.com\"; drop"} },
		func(c *GatewayConfig) { c.Routes[2].Port = 443 },                    // порт занят маршрутами по имени
		func(c *GatewayConfig) { c.Routes[3].Port = 2222 },                   // tcp 2222 дважды
		func(c *GatewayConfig) { c.Routes[2].Algorithm = AlgoLeastConn },     // leastconn — только 80/443
		func(c *GatewayConfig) { c.Routes[2].Targets[0].Host = "10.77.1.9" }, // не хост туннеля
		func(c *GatewayConfig) { c.Routes[0].Check.Path = "/x y" },           // путь с пробелом
		func(c *GatewayConfig) { c.Peers[1].PublicKey = "not-a-key" },
		func(c *GatewayConfig) { c.Routes[2].ProxyProtocol = true }, // PROXY — только http/https
		func(c *GatewayConfig) { c.Routes[1].ID = "app" },           // id дважды
	}
	for i, f := range bad {
		g := sampleGateway()
		f(&g)
		if g.Validate() == nil {
			t.Errorf("случай %d пропущен", i)
		}
	}
	h := sampleHost()
	h.Services = append(h.Services, Service{Port: 40020, Proto: "tcp", Addr: "127.0.0.1", ToPort: 22})
	if (HostConfig{Ifaces: []HostIface{h}}).Validate() == nil {
		t.Error("loopback-цель пропущена")
	}
}

func TestGatewayNFT(t *testing.T) {
	g := sampleGateway()
	alive := func(r, id string) bool { return !(r == "mc" && id == "m1") }
	out := GatewayNFT(g, alive)
	for _, want := range []string{
		`tcp dport 2222 ct mark set 0x464c1003 counter dnat to 10.77.1.2:40002 comment "fly:ssh"`,
		// m1 упал, m3 в «сливе» — остался m2 с весом 2: одна цель.
		`udp dport 25565 ct mark set 0x464c1004 counter dnat to 10.77.1.2:40010 comment "fly:mc"`,
		`tcp dport 25565 ct mark set 0x464c1004`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет %q в\n%s", want, out)
		}
	}
	if strings.Contains(out, "dport 443") || strings.Contains(out, "dport 80 ") {
		t.Error("маршруты по имени попали в ядро")
	}
	all := GatewayNFT(g, nil)
	if !strings.Contains(all, "jhash ip saddr mod 3 map { 0 : 10.77.1.3 . 40010, 1 : 10.77.1.2 . 40010, 2 : 10.77.1.2 . 40010 }") {
		t.Errorf("веса/хэш:\n%s", all)
	}
	nftLoads(t, all)
	nftLoads(t, GatewayNFT(g, func(string, string) bool { return false }))
}

func TestHost(t *testing.T) {
	h := sampleHost()
	nft := HostNFT(h)
	for _, want := range []string{
		`iifname "nkt-fly1" ct mark set 0x464c0001`,
		`iifname "nkt-fly1" tcp dport 40001 counter dnat to 172.17.0.5:8080`,
		`iifname != "nkt-fly1" ct mark 0x464c0001 meta mark set ct mark`,
	} {
		if !strings.Contains(nft, want) {
			t.Errorf("нет %q в\n%s", want, nft)
		}
	}
	nftLoads(t, nft)
	wg := HostWG(h, "/etc/nkt/fly/nkt-fly1.key", "/etc/nkt/fly/nkt-fly1.nft")
	for _, want := range []string{"AllowedIPs = 0.0.0.0/0", "Table = off", "Endpoint = 203.0.113.10:51820", "ip rule add fwmark 0x464c0001 table 7701", "PersistentKeepalive = 25"} {
		if !strings.Contains(wg, want) {
			t.Errorf("нет %q в\n%s", want, wg)
		}
	}
	if strings.Contains(wg, "PrivateKey") {
		t.Error("закрытый ключ в файле")
	}
	gw := GatewayWG(sampleGateway(), "/etc/nkt-edge/fly/wg.key")
	if !strings.Contains(gw, "AllowedIPs = 10.77.1.2/32") || strings.Contains(gw, "PrivateKey") {
		t.Errorf("%s", gw)
	}
}

// nftLoads — правила действительно загружаются (в своём сетевом
// пространстве имён, без прав и без последствий для системы).
func nftLoads(t *testing.T, rules string) {
	t.Helper()
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("нет nft")
	}
	f := filepath.Join(t.TempDir(), "r.nft")
	if err := os.WriteFile(f, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("unshare", "-rn", "nft", "-f", f).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Operation not permitted") || strings.Contains(string(out), "unshare") {
			t.Skipf("нет пространств имён: %s", out)
		}
		t.Fatalf("nft не принял правила: %v\n%s\n%s", err, out, rules)
	}
}
