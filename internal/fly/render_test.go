package fly

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	wg := HostWG(h, "/etc/wireguard/nkt-fly1.key")
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
	// AppArmor wg-quick (Ubuntu 26.04+) не пускает sysctl и nft -f из
	// PostUp — они в службе nkt-fly-rules@.
	for _, conf := range []string{wg, gw} {
		for _, bad := range []string{"sysctl", "nft -f"} {
			if strings.Contains(conf, bad) {
				t.Errorf("%q в PostUp:\n%s", bad, conf)
			}
		}
	}
}

// TunnelVia: connections to the gateway's external addr:port are marked
// into the tunnel table; the mark and ct mark let the reply path and the
// MSS clamp keep working. An empty list renders no via rules.
// Внутренний маршрут: пул принимает соединения из туннеля, пришедшие на
// план-адрес шлюза; снаружи порт не открыт — внешнего правила нет.
func TestGatewayInternalRoute(t *testing.T) {
	g := sampleGateway()
	g.Routes = append(g.Routes, Route{ID: "api", Kind: KindTCP, Port: 6443, Internal: true,
		Algorithm: AlgoSourceHash, Check: Check{Kind: CheckTCP},
		Targets: []Target{{ID: "cp1", Host: "10.77.1.2", Port: 40005}}})
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	nft := GatewayNFT(g, nil)
	internalRule := `iifname "nkt-fly" ip daddr 10.77.1.1 tcp dport 6443 ct mark set`
	if !strings.Contains(nft, internalRule) {
		t.Errorf("нет внутреннего правила в\n%s", nft)
	}
	if !strings.Contains(nft, `dnat to 10.77.1.2:40005 comment "fly:api"`) {
		t.Errorf("нет DNAT в пул внутреннего маршрута в\n%s", nft)
	}
	// Правило из туннеля обязано стоять до раннего return туннельного трафика.
	if i, r := strings.Index(nft, internalRule), strings.Index(nft, `iifname "nkt-fly" return`); i < 0 || r < 0 || i > r {
		t.Errorf("внутреннее правило после return туннеля в\n%s", nft)
	}
	if strings.Contains(nft, "\n\t\ttcp dport 6443 ") {
		t.Errorf("внутренний маршрут открыл порт снаружи в\n%s", nft)
	}
	// Самообращение (клиент == цель, kubelet CP звонит на общий адрес и
	// хэш приводит его на себя): SNAT на план-адрес шлюза в postrouting
	// замыкает обратный путь — без него ответ цели доставляется локально и
	// клиент виснет.
	if !strings.Contains(nft, "chain postrouting {\n\t\ttype nat hook postrouting priority srcnat;") {
		t.Errorf("нет цепочки postrouting в\n%s", nft)
	}
	hairpin := `ip saddr 10.77.1.2 ip daddr 10.77.1.2 counter snat to 10.77.1.1 comment "fly:api"`
	if !strings.Contains(nft, hairpin) {
		t.Errorf("нет SNAT самообращения в\n%s", nft)
	}
	// Мёртвая цель не принимает соединения — и самообращение на неё не
	// замыкается: правила нет, а с ним и всей цепочки.
	dead := GatewayNFT(g, func(r, id string) bool { return !(r == "api" && id == "cp1") })
	if strings.Contains(dead, "postrouting") {
		t.Errorf("цепочка postrouting с мёртвой целью в\n%s", dead)
	}
	nftLoads(t, nft)
	nftLoads(t, dead)
	// Внешние маршруты самообращения не получают: их клиенты приходят
	// снаружи и источниками целей не бывают.
	if strings.Contains(GatewayNFT(sampleGateway(), nil), "postrouting") {
		t.Error("postrouting у шлюза без внутренних маршрутов")
	}
	// Внутренним бывает только маршрут по порту: у маршрутов по имени
	// терминал — сам шлюз, привязывать не к чему.
	named := sampleGateway()
	named.Routes[0].Internal = true
	if err := named.Validate(); err == nil {
		t.Error("проверка пропустила internal у маршрута по имени")
	}
}

// Служба пересылки и правил: вслед за wg-quick того же интерфейса, правила
// — из /etc/wireguard; постоянный sysctl — у шлюза со счётчиками.
func TestRulesUnit(t *testing.T) {
	for _, want := range []string{
		"After=wg-quick@%i.service", "BindsTo=wg-quick@%i.service", "WantedBy=wg-quick@%i.service",
		"net.ipv4.ip_forward=1 net.ipv4.conf.%i.rp_filter=2", "nft -f /etc/wireguard/%i.nft", "RemainAfterExit=yes",
	} {
		if !strings.Contains(RulesUnit, want) {
			t.Errorf("нет %q в\n%s", want, RulesUnit)
		}
	}
	if RulesUnitName("nkt-fly1") != "nkt-fly-rules@nkt-fly1.service" {
		t.Error(RulesUnitName("nkt-fly1"))
	}
	if strings.Contains(SysctlConf(false), "conntrack") || !strings.Contains(SysctlConf(true), "nf_conntrack_acct = 1") || !strings.Contains(SysctlConf(false), "ip_forward = 1") {
		t.Errorf("%s / %s", SysctlConf(false), SysctlConf(true))
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
	// Старые nftables (0.9.x, 1.0.x на Debian 11, Ubuntu 22.04): корни
	// распакованных пакетов через «:» в NKT_NFT_OLD — правила разбираются
	// и ими (nft -c): в 1.0.2 «mss» — ключевое слово, цепочка с таким
	// именем ломала файл на хосте.
	for _, root := range filepath.SplitList(os.Getenv("NKT_NFT_OLD")) {
		if root == "" {
			continue
		}
		cmd := exec.Command("unshare", "-rn", filepath.Join(root, "usr/sbin/nft"), "-c", "-f", f)
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+filepath.Join(root, "usr/lib/x86_64-linux-gnu"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("nft из %s не принял правила: %v\n%s\n%s", root, err, out, rules)
		}
	}
}

// Диапазон портов (coturn relay): порт сохраняется насквозь, цель —
// первая живая; пересечения и неверные диапазоны отвергаются; правила
// шлюза и хоста грузятся в nft.
func TestPortRange(t *testing.T) {
	g := sampleGateway()
	turn := Route{ID: "turn", Kind: KindUDP, Port: 49152, PortTo: 65535, Algorithm: AlgoSourceHash, Check: Check{Kind: CheckNone},
		Targets: []Target{{ID: "c1", Host: "10.77.1.3", Port: 49152}, {ID: "c2", Host: "10.77.1.2", Port: 49152}}}
	g.Routes = append(g.Routes, turn)
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	rules := GatewayNFT(g, func(route, target string) bool { return !(route == "turn" && target == "c1") })
	if !strings.Contains(rules, "udp dport 49152-65535 ct mark set") || !strings.Contains(rules, "dnat to 10.77.1.2 comment \"fly:turn\"") {
		t.Fatalf("range rule:\n%s", rules)
	}
	nftLoads(t, rules)

	bad := map[string]func(*Route){
		"named":       func(r *Route) { r.Kind, r.Names = KindHTTPS, []string{"x.example.com"} },
		"reversed":    func(r *Route) { r.PortTo = r.Port - 1 },
		"too wide":    func(r *Route) { r.Port, r.PortTo = 1024, 65535 },
		"check out":   func(r *Route) { r.Check = Check{Kind: CheckTCP, Port: 3478} },
		"target port": func(r *Route) { r.Targets[0].Port = 3478 },
	}
	for name, f := range bad {
		c := sampleGateway()
		r := turn
		r.Targets = append([]Target(nil), turn.Targets...)
		f(&r)
		c.Routes = append(c.Routes, r)
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Пересечение с одиночным портом.
	c := sampleGateway()
	c.Routes = append(c.Routes, turn, Route{ID: "one", Kind: KindUDP, Port: 50000, Algorithm: AlgoSourceHash, Check: Check{Kind: CheckNone},
		Targets: []Target{{ID: "x", Host: "10.77.1.2", Port: 40020}}})
	if c.Validate() == nil {
		t.Error("overlap accepted")
	}

	h := sampleHost()
	h.Services = append(h.Services, Service{Port: 49152, PortTo: 65535, Proto: "udp", Addr: "172.17.0.9", ToPort: 49152})
	if err := (HostConfig{Ifaces: []HostIface{h}}).Validate(); err != nil {
		t.Fatal(err)
	}
	hr := HostNFT(h)
	if !strings.Contains(hr, "udp dport 49152-65535 counter dnat to 172.17.0.9\n") {
		t.Fatalf("host range rule:\n%s", hr)
	}
	nftLoads(t, hr)
	h.Services = append(h.Services, Service{Port: 50000, Proto: "udp", Addr: "172.17.0.8", ToPort: 50000})
	if (HostConfig{Ifaces: []HostIface{h}}).Validate() == nil {
		t.Error("host overlap accepted")
	}
}

// MSS и ограничения на клиента грузятся в nft.
func TestGatewayLimitsLoad(t *testing.T) {
	g := sampleGateway()
	g.Routes[2].MaxConns, g.Routes[2].Rate = 20, 10
	g.Routes[3].Rate = 50
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	rules := GatewayNFT(g, nil)
	for _, want := range []string{"maxseg size set rt mtu", "ct count over 20", "limit rate over 10/second", "limit rate over 50/second"} {
		if !strings.Contains(rules, want) {
			t.Fatalf("%q missing:\n%s", want, rules)
		}
	}
	nftLoads(t, rules)
}

// Имена цепочек и множеств — не слова nftables: в старых версиях (до
// 1.0.6) «chain mss» — синтаксическая ошибка, и правила хоста не
// грузились. Проверка без nft — в CI старых версий нет.
func TestNFTNamesNotKeywords(t *testing.T) {
	reserved := map[string]bool{"mss": true, "mark": true, "limit": true, "counter": true, "quota": true, "flags": true, "meta": true,
		"ct": true, "log": true, "reject": true, "queue": true, "dnat": true, "snat": true, "masquerade": true, "redirect": true,
		"tproxy": true, "synproxy": true, "secmark": true, "accept": true, "drop": true, "timeout": true, "size": true, "rt": true}
	g := sampleGateway()
	g.Routes[2].MaxConns, g.Routes[2].Rate = 20, 10
	names := regexp.MustCompile(`(?m)^\s*(?:chain|set)\s+([A-Za-z0-9_]+)\s*\{`)
	for _, rules := range []string{GatewayNFT(g, nil), HostNFT(sampleHost())} {
		for _, m := range names.FindAllStringSubmatch(rules, -1) {
			if reserved[m[1]] {
				t.Errorf("name %q is an nftables keyword", m[1])
			}
		}
	}
}
