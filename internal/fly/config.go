// Package fly — Traffic Fly: проброс трафика из интернета через VPS-шлюз
// к сервисам на хостах (контейнеры, машины, сам хост), с настоящим IP
// клиента и балансировкой.
//
//	интернет ─80/443─▶ шлюз: маршрутизатор по имени (SNI/Host) + PROXY protocol ─┐
//	         ─прочие TCP/UDP─▶ шлюз: nftables DNAT (балансировка в ядре) ─────────┤ WireGuard
//	                                                       (соединение открывает хост)
//	                     хост: DNAT служебного порта на цель; ответы — назад в туннель
//
// Пакет ничего не импортирует из nkt (его берёт и маленький nkt-edge): тут
// типы конфигурации, проверка, генерация wg-quick и nftables, маршрутизатор,
// проверки целей, PROXY protocol и статистика.
package fly

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// Виды маршрутов.
const (
	KindHTTPS  = "https"  // 443 по имени (SNI, без расшифровки)
	KindHTTP   = "http"   // 80 по имени (заголовок Host)
	KindTCP    = "tcp"    // порт целиком, ядро
	KindUDP    = "udp"    // порт целиком, ядро
	KindTCPUDP = "tcpudp" // TCP и UDP на одном порту, ядро
)

// Алгоритмы балансировки.
const (
	AlgoLeastConn  = "leastconn"  // наименьшее число соединений (только 80/443)
	AlgoRoundRobin = "roundrobin" // по кругу
	AlgoSourceHash = "source"     // по IP клиента: один клиент — одна цель
)

// Проверки целей.
const (
	CheckTCP  = "tcp"
	CheckHTTP = "http"
	CheckNone = "none"
)

// Target — цель пула в конфигурации шлюза: служебный порт хоста в
// туннеле (хост сам ведёт его дальше — на контейнер, машину или себя).
type Target struct {
	ID     string `json:"id"`
	Label  string `json:"label,omitempty"`
	Host   string `json:"host"` // WireGuard-адрес хоста, 10.77.G.x
	Port   int    `json:"port"` // служебный порт на хосте
	Weight int    `json:"weight,omitempty"`
	Drain  bool   `json:"drain,omitempty"`
}

// Check — проверка целей пула.
type Check struct {
	Kind string `json:"kind"` // tcp | http | none
	Path string `json:"path,omitempty"`
	// Expect — ожидаемый код HTTP (0 — любой 2xx/3xx).
	Expect int `json:"expect,omitempty"`
	// Port — другой TCP-порт цели для проверки (UDP-сервисы); 0 — тот же.
	Port      int `json:"port,omitempty"`
	IntervalS int `json:"interval_s,omitempty"`
}

// Route — маршрут шлюза: имя или порт → пул.
type Route struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"`
	Names []string `json:"names,omitempty"` // https, http
	Port  int      `json:"port"`            // публичный порт шлюза
	// PortTo — конец диапазона портов (tcp, udp, tcpudp): порт сохраняется
	// насквозь (шлюз → хост → цель, N → N), цель — одна активная: первая
	// живая по порядку (выделенный coturn relay-порт живёт в памяти того,
	// кто его выделил). 0 — один порт.
	PortTo int `json:"port_to,omitempty"`
	// ProxyProtocol — PROXY protocol v2 к цели (https, http): настоящий IP
	// клиента; прочие виды сохраняют его ядром.
	ProxyProtocol bool `json:"proxy_protocol,omitempty"`
	// TLS — https с расшифровкой на шлюзе: сертификат Let's Encrypt на
	// имена маршрута получает и продлевает шлюз (проверка — по его же
	// порту 80), цели уходит HTTP с X-Forwarded-For/X-Real-IP. Только
	// точные имена (без *.) и без PROXY protocol.
	TLS bool `json:"tls,omitempty"`
	// Allow — с каких адресов клиентов маршрут принимает соединения
	// (IPv4-адреса и сети); пусто — со всех. Остальных отсекает шлюз:
	// маршруты по порту — ядро (их пакеты не идут на цели), по имени —
	// маршрутизатор. Проверки Let's Encrypt на шлюз проходят всегда.
	Allow     []string `json:"allow,omitempty"`
	Algorithm string   `json:"algorithm"`
	Check     Check    `json:"check"`
	Targets   []Target `json:"targets"`
}

// Peer — хост в туннеле шлюза.
type Peer struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	Address   string `json:"address"` // 10.77.G.x
}

// GatewayConfig — всё, что шлюз делает (хаб присылает, шлюз хранит в
// /etc/nkt-edge/fly.json и поднимает из него после перезагрузки сам).
type GatewayConfig struct {
	Revision   int64   `json:"revision"`
	Iface      string  `json:"iface"`   // nkt-fly
	Address    string  `json:"address"` // 10.77.G.1/24
	ListenPort int     `json:"listen_port"`
	Peers      []Peer  `json:"peers"`
	Routes     []Route `json:"routes"`
}

// Service — служебный порт хоста: из туннеля на цель.
type Service struct {
	Port   int    `json:"port"`
	PortTo int    `json:"port_to,omitempty"` // диапазон Port..PortTo, порт сохраняется
	Proto  string `json:"proto"`             // tcp | udp
	Addr   string `json:"addr"`              // куда: IP контейнера, машины или свой WG-адрес
	ToPort int    `json:"to_port"`
	// Kind и Ref — что это (container, vm, lxd и имя): хост сам сверяет
	// адрес по своему инвентарю и переписывает правило, если он сменился,
	// — и без хаба.
	Kind string `json:"kind,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

// HostIface — туннель хоста к одному шлюзу.
type HostIface struct {
	Name     string    `json:"name"`    // nkt-fly1
	Address  string    `json:"address"` // 10.77.G.x/24
	Gateway  Peer      `json:"gateway"` // ключ и адрес шлюза в туннеле
	Endpoint string    `json:"endpoint"`
	Mark     uint32    `json:"mark"`  // метка соединений из туннеля
	Table    int       `json:"table"` // таблица маршрутов для ответов
	Services []Service `json:"services"`
}

// HostConfig — туннели хоста ко всем шлюзам.
type HostConfig struct {
	Revision int64       `json:"revision"`
	Ifaces   []HostIface `json:"ifaces"`
}

var (
	ifaceRe = regexp.MustCompile(`^nkt-fly[0-9]{0,3}$`)
	idRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	nameRe  = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	keyRe   = regexp.MustCompile(`^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw480]=$`)
	labelRe = regexp.MustCompile(`^[^\x00-\x1f"\\]{0,80}$`)
	pathRe  = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/-]{0,200}$`)
)

// ValidName — имя сайта для маршрута по имени (можно *.example.com).
func ValidName(s string) bool { return nameRe.MatchString(s) }

// ValidKey — публичный ключ WireGuard.
func ValidKey(s string) bool { return keyRe.MatchString(s) }

// ValidIface — имя интерфейса Traffic Fly.
func ValidIface(s string) bool { return ifaceRe.MatchString(s) }

func validPort(p int) bool { return p > 0 && p < 65536 }

// tunnelAddr — адрес внутри туннеля (10.77.0.0/16), без маски.
func tunnelAddr(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Is4() && netip.MustParsePrefix("10.77.0.0/16").Contains(a)
}

func tunnelPrefix(s string) bool {
	p, err := netip.ParsePrefix(s)
	return err == nil && p.Addr().Is4() && netip.MustParsePrefix("10.77.0.0/16").Contains(p.Addr()) && p.Bits() >= 16
}

// Validate — всё, что пойдёт в wg-quick, nftables и маршрутизатор, должно
// пройти здесь: строки попадают в файлы правил.
func (c GatewayConfig) Validate() error {
	if !ifaceRe.MatchString(c.Iface) {
		return fmt.Errorf("fly: iface %q", c.Iface)
	}
	if !tunnelPrefix(c.Address) || !validPort(c.ListenPort) {
		return fmt.Errorf("fly: address %q / port %d", c.Address, c.ListenPort)
	}
	addrs := map[string]bool{}
	for _, p := range c.Peers {
		if !labelRe.MatchString(p.Name) || !keyRe.MatchString(p.PublicKey) || !tunnelAddr(p.Address) || addrs[p.Address] {
			return fmt.Errorf("fly: peer %q", p.Name)
		}
		addrs[p.Address] = true
	}
	ports := map[string]bool{}
	names := map[string]bool{}
	portKinds := map[int]string{}
	ids := map[string]bool{}
	for _, r := range c.Routes {
		if err := r.validate(addrs); err != nil {
			return err
		}
		if ids[r.ID] {
			return fmt.Errorf("fly: route id %q twice", r.ID)
		}
		ids[r.ID] = true
		switch r.Kind {
		case KindHTTPS, KindHTTP:
			if k, ok := portKinds[r.Port]; ok && k != r.Kind {
				return fmt.Errorf("fly: port %d is both http and https", r.Port)
			}
			portKinds[r.Port] = r.Kind
			for _, n := range r.Names {
				k := fmt.Sprint(r.Port, "/", n)
				if names[k] {
					return fmt.Errorf("fly: name %s on port %d twice", n, r.Port)
				}
				names[k] = true
			}
			if ports[fmt.Sprint("tcp/", r.Port)] {
				return fmt.Errorf("fly: port %d is a whole-port route already", r.Port)
			}
		default:
			for _, proto := range r.protos() {
				for _, p := range r.ports() {
					k := fmt.Sprint(proto, "/", p)
					if ports[k] || proto == "tcp" && namedPort(c.Routes, p) {
						return fmt.Errorf("fly: port %s twice", k)
					}
					ports[k] = true
				}
			}
		}
	}
	// Сертификат на шлюзе проверяется Let's Encrypt по порту 80 шлюза:
	// он должен быть его — свободен или под маршрутами HTTP по имени.
	for _, r := range c.Routes {
		if r.TLS && (ports["tcp/80"] || (portKinds[80] != "" && portKinds[80] != KindHTTP)) {
			return fmt.Errorf("fly: route %s: gateway TLS needs port 80 for Let's Encrypt", r.ID)
		}
	}
	return nil
}

// TLSNames — имена маршрутов с сертификатом на шлюзе.
func (c GatewayConfig) TLSNames() []string {
	var out []string
	for _, r := range c.Routes {
		if r.TLS {
			out = append(out, r.Names...)
		}
	}
	return out
}

func namedPort(routes []Route, port int) bool {
	for _, r := range routes {
		if (r.Kind == KindHTTPS || r.Kind == KindHTTP) && r.Port == port {
			return true
		}
	}
	return false
}

// protos — протоколы ядра у маршрута по порту.
func (r Route) protos() []string {
	switch r.Kind {
	case KindTCP:
		return []string{"tcp"}
	case KindUDP:
		return []string{"udp"}
	case KindTCPUDP:
		return []string{"tcp", "udp"}
	}
	return nil
}

// Named — маршрут по имени (обслуживает маршрутизатор, не ядро).
func (r Route) Named() bool { return r.Kind == KindHTTPS || r.Kind == KindHTTP }

// maxAllow — сколько адресов и сетей в белом списке маршрута.
const maxAllow = 200

// parseAllow — адрес или сеть IPv4 белого списка.
func parseAllow(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil && p.Addr().Is4() {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return netip.Prefix{}, fmt.Errorf("fly: bad address %q", s)
	}
	return netip.PrefixFrom(a, 32), nil
}

// Allowed — клиент с адресом a допущен к маршруту.
func (r Route) Allowed(a netip.Addr) bool {
	if len(r.Allow) == 0 {
		return true
	}
	a = a.Unmap()
	for _, s := range r.Allow {
		if p, err := parseAllow(s); err == nil && p.Contains(a) {
			return true
		}
	}
	return false
}

// allowNFT — условие nft на адрес клиента; пусто — без условия.
func (r Route) allowNFT() string {
	if len(r.Allow) == 0 {
		return ""
	}
	parts := make([]string, 0, len(r.Allow))
	for _, s := range r.Allow {
		if p, err := parseAllow(s); err == nil {
			parts = append(parts, p.String())
		}
	}
	return "ip saddr { " + strings.Join(parts, ", ") + " } "
}

// Range — маршрут занимает диапазон портов.
func (r Route) Range() bool { return r.PortTo > r.Port }

// maxRangePorts — самый широкий диапазон одного маршрута (coturn:
// 49152–65535).
const maxRangePorts = 20000

// ports — порты маршрута (один или диапазон).
func (r Route) ports() []int {
	if !r.Range() {
		return []int{r.Port}
	}
	out := make([]int, 0, r.PortTo-r.Port+1)
	for p := r.Port; p <= r.PortTo; p++ {
		out = append(out, p)
	}
	return out
}

// portSpec — порт или диапазон для nft.
func portSpec(from, to int) string {
	if to > from {
		return fmt.Sprintf("%d-%d", from, to)
	}
	return fmt.Sprint(from)
}

func (r Route) validate(peers map[string]bool) error {
	if !idRe.MatchString(r.ID) {
		return fmt.Errorf("fly: route id %q", r.ID)
	}
	if !validPort(r.Port) {
		return fmt.Errorf("fly: route %s port %d", r.ID, r.Port)
	}
	if r.PortTo != 0 {
		if r.Named() || !validPort(r.PortTo) || r.PortTo <= r.Port || r.PortTo-r.Port > maxRangePorts {
			return fmt.Errorf("fly: route %s port range %d-%d", r.ID, r.Port, r.PortTo)
		}
		// Проверка — на порту внутри диапазона (другие порты хоста в
		// туннеле не пробрасываются).
		if r.Check.Port != 0 && (r.Check.Port < r.Port || r.Check.Port > r.PortTo) {
			return fmt.Errorf("fly: route %s: check port %d is outside %d-%d", r.ID, r.Check.Port, r.Port, r.PortTo)
		}
		for _, t := range r.Targets {
			if t.Port != r.Port {
				return fmt.Errorf("fly: route %s: a port range keeps ports, target %s port must be %d", r.ID, t.ID, r.Port)
			}
		}
	}
	switch r.Kind {
	case KindHTTPS, KindHTTP:
		if len(r.Names) == 0 {
			return fmt.Errorf("fly: route %s has no names", r.ID)
		}
		for _, n := range r.Names {
			if !nameRe.MatchString(n) {
				return fmt.Errorf("fly: route %s name %q", r.ID, n)
			}
			if r.TLS && strings.HasPrefix(n, "*.") {
				return fmt.Errorf("fly: route %s: a gateway certificate needs exact names, not %q", r.ID, n)
			}
		}
		if r.TLS && (r.Kind != KindHTTPS || r.ProxyProtocol) {
			return fmt.Errorf("fly: route %s: gateway TLS is for https without PROXY protocol", r.ID)
		}
	case KindTCP, KindUDP, KindTCPUDP:
		if len(r.Names) > 0 || r.ProxyProtocol {
			return fmt.Errorf("fly: route %s: names and PROXY protocol are for http/https", r.ID)
		}
	default:
		return fmt.Errorf("fly: route %s kind %q", r.ID, r.Kind)
	}
	if len(r.Allow) > maxAllow {
		return fmt.Errorf("fly: route %s: more than %d allowed addresses", r.ID, maxAllow)
	}
	for _, a := range r.Allow {
		if _, err := parseAllow(a); err != nil {
			return fmt.Errorf("fly: route %s: allowed address %q", r.ID, a)
		}
	}
	switch r.Algorithm {
	case AlgoRoundRobin, AlgoSourceHash:
	case AlgoLeastConn:
		if !r.Named() {
			return fmt.Errorf("fly: route %s: leastconn is for http/https", r.ID)
		}
	default:
		return fmt.Errorf("fly: route %s algorithm %q", r.ID, r.Algorithm)
	}
	switch r.Check.Kind {
	case CheckTCP, CheckNone:
	case CheckHTTP:
		if !pathRe.MatchString(r.Check.Path) {
			return fmt.Errorf("fly: route %s check path %q", r.ID, r.Check.Path)
		}
	default:
		return fmt.Errorf("fly: route %s check %q", r.ID, r.Check.Kind)
	}
	// UDP-сервису TCP-проверка — только на другом порту (у самого сервиса
	// TCP нет: проверка всегда падала бы).
	if r.Kind == KindUDP && r.Check.Kind != CheckNone && r.Check.Port == 0 {
		return fmt.Errorf("fly: route %s: a udp route needs check port or no check", r.ID)
	}
	if r.Check.Port != 0 && !validPort(r.Check.Port) || r.Check.Expect != 0 && (r.Check.Expect < 100 || r.Check.Expect > 599) {
		return fmt.Errorf("fly: route %s check", r.ID)
	}
	if len(r.Targets) == 0 || len(r.Targets) > 64 {
		return fmt.Errorf("fly: route %s targets", r.ID)
	}
	seen := map[string]bool{}
	for _, t := range r.Targets {
		if !idRe.MatchString(t.ID) || seen[t.ID] || !labelRe.MatchString(t.Label) || !tunnelAddr(t.Host) || !peers[t.Host] || !validPort(t.Port) || t.Weight < 0 || t.Weight > 100 {
			return fmt.Errorf("fly: route %s target %q", r.ID, t.ID)
		}
		seen[t.ID] = true
	}
	return nil
}

// Validate — конфигурация хоста.
func (c HostConfig) Validate() error {
	names := map[string]bool{}
	for _, i := range c.Ifaces {
		if !ifaceRe.MatchString(i.Name) || names[i.Name] || !tunnelPrefix(i.Address) || !keyRe.MatchString(i.Gateway.PublicKey) || !tunnelAddr(i.Gateway.Address) {
			return fmt.Errorf("fly: iface %q", i.Name)
		}
		names[i.Name] = true
		ap, err := netip.ParseAddrPort(i.Endpoint)
		if err != nil || !ap.Addr().IsGlobalUnicast() {
			return fmt.Errorf("fly: iface %s endpoint %q", i.Name, i.Endpoint)
		}
		if i.Mark == 0 || i.Table < 100 || i.Table > 60000 {
			return fmt.Errorf("fly: iface %s mark/table", i.Name)
		}
		seen := map[string]bool{}
		for _, s := range i.Services {
			a, err := netip.ParseAddr(s.Addr)
			k := fmt.Sprint(s.Proto, "/", s.Port)
			clash := seen[k]
			if s.PortTo != 0 {
				if s.PortTo <= s.Port || s.PortTo-s.Port > maxRangePorts || !validPort(s.PortTo) {
					return fmt.Errorf("fly: iface %s service range %d-%d", i.Name, s.Port, s.PortTo)
				}
				for p := s.Port + 1; p <= s.PortTo; p++ {
					pk := fmt.Sprint(s.Proto, "/", p)
					clash = clash || seen[pk]
					seen[pk] = true
				}
			}
			if err != nil || !a.Is4() || a.IsLoopback() || a.IsUnspecified() || !validPort(s.Port) || !validPort(s.ToPort) || (s.Proto != "tcp" && s.Proto != "udp") || clash ||
				!refKind(s.Kind) || len(s.Ref) > 200 || strings.ContainsAny(s.Ref, "\x00\n\r") {
				return fmt.Errorf("fly: iface %s service %d/%s", i.Name, s.Port, s.Proto)
			}
			seen[k] = true
		}
	}
	return nil
}

func refKind(k string) bool {
	switch k {
	case "", "container", "vm", "lxd", "host", "addr":
		return true
	}
	return false
}

// sortedRoutes — маршруты в порядке, не зависящем от правок (стабильные
// файлы правил: меньше лишних перезагрузок).
func sortedRoutes(rs []Route) []Route {
	out := append([]Route(nil), rs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func joinNames(n []string) string { return strings.Join(n, ",") }
