package fly

import (
	"fmt"
	"sort"
	"strings"
)

// Метки соединений. На шлюзе — метка маршрута (по ней conntrack отдаёт
// соединения маршрута для сводки); на хосте — метка туннеля (по ней ответы
// уходят назад в туннель, а не в интернет: так сохраняется IP клиента).
const (
	GatewayMarkBase uint32 = 0x464c1000
	HostMarkBase    uint32 = 0x464c0000
	HostTableBase          = 7700
)

// RouteMark — метка соединений маршрута на шлюзе (index — с 1).
func RouteMark(index int) uint32 { return GatewayMarkBase + uint32(index) }

// GatewayWG — /etc/wireguard/<iface>.conf шлюза. Закрытый ключ в файл не
// пишется: PostUp берёт его из keyFile (0600, создаётся на самом шлюзе).
// Table = off — wg-quick не трогает маршруты. FORWARD в iptables открыт
// для туннеля: ufw и Docker держат политику DROP.
func GatewayWG(c GatewayConfig, keyFile string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# nkt Traffic Fly — шлюз; файл пишет nkt-fly, правки руками затрутся\n[Interface]\nAddress = %s\nListenPort = %d\nTable = off\n", c.Address, c.ListenPort)
	fmt.Fprintf(&b, "PostUp = wg set %%i private-key %s\n", keyFile)
	b.WriteString("PostUp = sysctl -qw net.ipv4.ip_forward=1 net.ipv4.conf.%i.rp_filter=2; sysctl -qw net.netfilter.nf_conntrack_acct=1 || true\n")
	b.WriteString("PostUp = iptables -I FORWARD 1 -i %i -j ACCEPT || true; iptables -I FORWARD 1 -o %i -j ACCEPT || true\n")
	b.WriteString("PostDown = iptables -D FORWARD -i %i -j ACCEPT || true; iptables -D FORWARD -o %i -j ACCEPT || true\n")
	peers := append([]Peer(nil), c.Peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].Address < peers[j].Address })
	for _, p := range peers {
		fmt.Fprintf(&b, "\n[Peer]\n# %s\nPublicKey = %s\nAllowedIPs = %s/32\n", p.Name, p.PublicKey, p.Address)
	}
	return b.String()
}

// GatewayNFT — таблица ip nkt_fly шлюза: маршруты по порту (TCP/UDP) —
// DNAT на служебные порты хостов; адрес клиента не подменяется. alive —
// какие цели живы (по проверкам); цели без жизни и в «сливе» из правила
// уходят; пул пуст — правила нет, соединения не принимаются. Маршруты по
// имени здесь не участвуют: их слушает маршрутизатор.
func GatewayNFT(c GatewayConfig, alive func(routeID, targetID string) bool) string {
	var b strings.Builder
	b.WriteString("# nkt Traffic Fly — шлюз\ntable ip nkt_fly\ndelete table ip nkt_fly\ntable ip nkt_fly {\n")
	b.WriteString("\tchain prerouting {\n\t\ttype nat hook prerouting priority dstnat; policy accept;\n")
	fmt.Fprintf(&b, "\t\tiifname %q return\n", c.Iface)
	for i, r := range c.Routes {
		if r.Named() {
			continue
		}
		var live []Target
		for _, t := range r.Targets {
			if !t.Drain && (alive == nil || alive(r.ID, t.ID)) {
				live = append(live, t)
			}
		}
		if len(live) == 0 {
			continue
		}
		for _, proto := range r.protos() {
			fmt.Fprintf(&b, "\t\t%s dport %d ct mark set 0x%x counter dnat %s comment %q\n",
				proto, r.Port, RouteMark(i+1), dnatTarget(r.Algorithm, live), "fly:"+r.ID)
		}
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

// dnatTarget — куда DNAT: одна цель — адрес; несколько — карта по хэшу IP
// клиента или по кругу, с весами (цель повторяется weight раз).
func dnatTarget(algo string, ts []Target) string {
	if len(ts) == 1 {
		return fmt.Sprintf("to %s:%d", ts[0].Host, ts[0].Port)
	}
	var slots []string
	for _, t := range ts {
		w := t.Weight
		if w <= 0 {
			w = 1
		}
		for k := 0; k < w; k++ {
			slots = append(slots, fmt.Sprintf("%s . %d", t.Host, t.Port))
		}
	}
	sel := fmt.Sprintf("numgen inc mod %d", len(slots))
	if algo == AlgoSourceHash {
		sel = fmt.Sprintf("jhash ip saddr mod %d", len(slots))
	}
	parts := make([]string, len(slots))
	for k, s := range slots {
		parts[k] = fmt.Sprintf("%d : %s", k, s)
	}
	return fmt.Sprintf("ip addr . port to %s map { %s }", sel, strings.Join(parts, ", "))
}

// HostWG — /etc/wireguard/<iface>.conf хоста. AllowedIPs 0.0.0.0/0: из
// туннеля приходят пакеты с адресами клиентов (Table = off — маршрут по
// умолчанию хоста не меняется). Ответы с меткой туннеля уходят по своей
// таблице назад в туннель. Закрытый ключ — keyFile на самом хосте.
func HostWG(i HostIface, keyFile, nftFile string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# nkt Traffic Fly — туннель к шлюзу; файл пишет nkt, правки руками затрутся\n[Interface]\nAddress = %s\nTable = off\n", i.Address)
	fmt.Fprintf(&b, "PostUp = wg set %%i private-key %s\n", keyFile)
	b.WriteString("PostUp = sysctl -qw net.ipv4.ip_forward=1 net.ipv4.conf.%i.rp_filter=2\n")
	fmt.Fprintf(&b, "PostUp = nft -f %s\n", nftFile)
	fmt.Fprintf(&b, "PostUp = ip rule del fwmark 0x%x table %d 2>/dev/null || true; ip rule add fwmark 0x%x table %d priority %d\n", i.Mark, i.Table, i.Mark, i.Table, i.Table)
	// Шлюз указан явно: без него ядро считает адрес клиента соседним и
	// ищет его ARP-запросом (WireGuard это не нужно, но так верно везде).
	fmt.Fprintf(&b, "PostUp = ip route replace default via %s dev %%i onlink table %d\n", i.Gateway.Address, i.Table)
	b.WriteString("PostUp = iptables -I FORWARD 1 -i %i -j ACCEPT || true; iptables -I FORWARD 1 -o %i -j ACCEPT || true\n")
	b.WriteString("PostUp = iptables -L DOCKER-USER -n >/dev/null 2>&1 && iptables -I DOCKER-USER 1 -i %i -j ACCEPT || true\n")
	fmt.Fprintf(&b, "PostDown = ip rule del fwmark 0x%x table %d 2>/dev/null || true; ip route flush table %d 2>/dev/null || true\n", i.Mark, i.Table, i.Table)
	fmt.Fprintf(&b, "PostDown = nft delete table ip %s 2>/dev/null || true\n", hostTable(i.Name))
	b.WriteString("PostDown = iptables -D FORWARD -i %i -j ACCEPT || true; iptables -D FORWARD -o %i -j ACCEPT || true\n")
	b.WriteString("PostDown = iptables -L DOCKER-USER -n >/dev/null 2>&1 && iptables -D DOCKER-USER -i %i -j ACCEPT || true\n")
	fmt.Fprintf(&b, "\n[Peer]\n# шлюз Traffic Fly\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 25\n", i.Gateway.PublicKey, i.Endpoint)
	return b.String()
}

func hostTable(iface string) string { return strings.ReplaceAll(iface, "-", "_") }

// HostNFT — таблица хоста для туннеля: служебные порты → цели (DNAT, адрес
// клиента не трогается), метка соединения из туннеля; ответы с этой меткой
// (от контейнеров и машин — prerouting, от процессов хоста — output)
// получают метку пакета и уходят по таблице туннеля.
func HostNFT(i HostIface) string {
	t := hostTable(i.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "# nkt Traffic Fly — хост, туннель %s\ntable ip %s\ndelete table ip %s\ntable ip %s {\n", i.Name, t, t, t)
	b.WriteString("\tchain to_targets {\n\t\ttype nat hook prerouting priority dstnat; policy accept;\n")
	fmt.Fprintf(&b, "\t\tiifname %q ct mark set 0x%x\n", i.Name, i.Mark)
	svcs := append([]Service(nil), i.Services...)
	sort.Slice(svcs, func(a, c int) bool {
		if svcs[a].Port != svcs[c].Port {
			return svcs[a].Port < svcs[c].Port
		}
		return svcs[a].Proto < svcs[c].Proto
	})
	for _, s := range svcs {
		fmt.Fprintf(&b, "\t\tiifname %q %s dport %d counter dnat to %s:%d\n", i.Name, s.Proto, s.Port, s.Addr, s.ToPort)
	}
	b.WriteString("\t}\n")
	fmt.Fprintf(&b, "\tchain reply_mark {\n\t\ttype filter hook prerouting priority mangle; policy accept;\n\t\tiifname != %q ct mark 0x%x meta mark set ct mark\n\t}\n", i.Name, i.Mark)
	fmt.Fprintf(&b, "\tchain local_mark {\n\t\ttype route hook output priority mangle; policy accept;\n\t\tct mark 0x%x meta mark set ct mark\n\t}\n", i.Mark)
	b.WriteString("}\n")
	return b.String()
}
