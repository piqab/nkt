// Package analyze turns a parsed host snapshot into a list of concrete
// problems: port conflicts, firewall holes, dead upstreams, weak TLS and
// containers that do not match what compose declares.
package analyze

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/althq/netknownsthat/internal/model"
)

// sensitivePorts are services that must never face the public internet.
var sensitivePorts = map[int]string{
	1433:  "MS SQL Server",
	3306:  "MySQL/MariaDB",
	5432:  "PostgreSQL",
	5984:  "CouchDB",
	6379:  "Redis",
	9042:  "Cassandra",
	9200:  "Elasticsearch",
	11211: "Memcached",
	27017: "MongoDB",
	2375:  "Docker API без TLS",
	2379:  "etcd",
}

// weakTLSVersions are protocol versions no longer considered safe.
var weakTLSVersions = []string{"SSLv2", "SSLv3", "TLSv1", "TLSv1.1"}

type collector struct {
	findings []model.Finding
	seen     map[string]bool
}

func (c *collector) add(f model.Finding) {
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if f.ID == "" {
		f.ID = fmt.Sprintf("%s:%s", f.Rule, f.Object)
	}
	if c.seen[f.ID] {
		return
	}
	c.seen[f.ID] = true
	c.findings = append(c.findings, f)
}

// Run executes every rule against the snapshot and returns findings ordered by
// severity, then by service and object.
func Run(s *model.Snapshot) []model.Finding {
	c := &collector{}
	idx := buildIndex(s)

	rulePortConflicts(c, s)
	ruleDeclaredNotListening(c, s, idx)
	ruleListeningNotDeclared(c, s, idx)
	ruleFirewallDefaultPolicy(c, s, idx)
	rulePublicPortWithoutRule(c, s, idx)
	ruleDockerBypassesFirewall(c, s, idx)
	ruleStaleFirewallRules(c, s, idx)
	ruleSensitivePortsPublic(c, s, idx)
	ruleTLS(c, s)
	rulePlaintextProxy(c, s)
	ruleUpstreams(c, s, idx)
	ruleHealthChecks(c, s)
	ruleContainers(c, s)
	ruleAdminInterfaces(c, s)

	sort.SliceStable(c.findings, func(i, j int) bool {
		a, b := c.findings[i], c.findings[j]
		if ra, rb := model.SeverityRank(a.Severity), model.SeverityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.Object < b.Object
	})
	return c.findings
}

// --------------------------------------------------------------------- index

type index struct {
	listenersByPort map[int][]model.Listener
	endpointsByPort map[int][]model.Endpoint
	upstreamNames   map[string]*model.Upstream
	upstreamUsed    map[string]bool
	inputPolicy     string
	allowedFrom     map[int][]string // port -> list of sources allowed to reach it
	dockerDNAT      map[int]bool
	serviceActive   map[string]bool

	// listenersComparable is false when the socket table was read somewhere the
	// inspected services do not live; every rule contrasting the two must then
	// stay quiet rather than report the whole host as dead.
	listenersComparable bool
}

func buildIndex(s *model.Snapshot) *index {
	idx := &index{
		listenersByPort: map[int][]model.Listener{},
		endpointsByPort: map[int][]model.Endpoint{},
		upstreamNames:   map[string]*model.Upstream{},
		upstreamUsed:    map[string]bool{},
		allowedFrom:     map[int][]string{},
		dockerDNAT:      map[int]bool{},
		serviceActive:   map[string]bool{},
		inputPolicy:     "unknown",
	}

	for _, l := range s.Listeners {
		idx.listenersByPort[l.Port] = append(idx.listenersByPort[l.Port], l)
	}
	for _, e := range s.Endpoints {
		idx.endpointsByPort[e.Port] = append(idx.endpointsByPort[e.Port], e)
		for _, name := range e.Upstream {
			idx.upstreamUsed[e.Service+"|"+name] = true
		}
	}
	for i := range s.Upstreams {
		u := &s.Upstreams[i]
		idx.upstreamNames[u.Service+"|"+u.Name] = u
	}
	for _, svc := range s.Services {
		idx.serviceActive[svc.Name] = svc.ActiveState == "active"
	}

	for _, p := range s.Firewall.Policies {
		if p.Backend == "iptables" && p.Table == "filter" && p.Chain == "INPUT" {
			idx.inputPolicy = p.Policy
		}
	}
	for _, r := range s.Firewall.Rules {
		if r.Action == "DNAT" && r.DNATTo != "" {
			for _, p := range r.Ports {
				idx.dockerDNAT[p] = true
			}
			continue
		}
		if !isAcceptAction(r.Action) {
			continue
		}
		if !isInputChain(r.Chain) {
			continue
		}
		src := r.Source
		if src == "" {
			src = "Anywhere"
		}
		for _, p := range r.Ports {
			idx.allowedFrom[p] = append(idx.allowedFrom[p], src)
		}
	}

	idx.listenersComparable = len(s.Listeners) > 0 && idx.listenersAreComparable(s)
	return idx
}

func isAcceptAction(a string) bool {
	switch strings.ToUpper(a) {
	case "ACCEPT", "ALLOW", "LIMIT":
		return true
	}
	return false
}

func isInputChain(chain string) bool {
	c := strings.ToLower(chain)
	return c == "input" || strings.Contains(c, "user-input") || strings.Contains(c, "ufw")
}

// allowedFromAnywhere reports whether a port is reachable from any source.
func (i *index) allowedFromAnywhere(port int) bool {
	for _, src := range i.allowedFrom[port] {
		switch src {
		case "", "Anywhere", "0.0.0.0/0", "::/0", "anywhere":
			return true
		}
	}
	return false
}

func (i *index) hasListener(port int) bool { return len(i.listenersByPort[port]) > 0 }

// listenersAreComparable reports whether the observed sockets describe the same
// machine view as the parsed configuration. When several endpoints are declared
// and not a single one is corroborated by a listener, the two sides are not
// comparable and every rule that contrasts them must stay quiet.
func (i *index) listenersAreComparable(s *model.Snapshot) bool {
	declared, corroborated := 0, 0
	for _, e := range s.Endpoints {
		if e.Protocol == "udp" {
			continue
		}
		declared++
		if i.hasListener(e.Port) {
			corroborated++
		}
	}
	return declared < 3 || corroborated > 0
}

// listenerFor finds a listener that would receive traffic for an address:port.
func (i *index) listenerFor(addr string, port int) (model.Listener, bool) {
	for _, l := range i.listenersByPort[port] {
		if l.Address == addr || l.Public() || addr == "0.0.0.0" {
			return l, true
		}
	}
	return model.Listener{}, false
}

// --------------------------------------------------------------------- rules

func rulePortConflicts(c *collector, s *model.Snapshot) {
	byPort := map[int][]model.Endpoint{}
	for _, e := range s.Endpoints {
		if e.Protocol == "udp" {
			continue
		}
		byPort[e.Port] = append(byPort[e.Port], e)
	}
	for port, list := range byPort {
		if len(list) < 2 {
			continue
		}
		// Two declarations clash only when their bind addresses overlap.
		for a := 0; a < len(list); a++ {
			for b := a + 1; b < len(list); b++ {
				x, y := list[a], list[b]
				if !addressesOverlap(x, y) {
					continue
				}
				// nginx routinely declares several server{} blocks on the same
				// socket and picks one by server_name — that is not a conflict.
				if x.Service == model.ServiceNginx && y.Service == model.ServiceNginx {
					continue
				}
				if sameSocketTwoLayers(x, y) {
					continue
				}
				c.add(model.Finding{
					Rule:     "port-conflict",
					ID:       fmt.Sprintf("port-conflict:%d:%s:%s", port, x.ID, y.ID),
					Severity: model.SeverityHigh,
					Service:  x.Service,
					Object:   fmt.Sprintf("%s:%d", x.Address, port),
					Title:    fmt.Sprintf("Конфликт порта %d между %s и %s", port, x.Service, y.Service),
					Detail: fmt.Sprintf("%s (%s, %s:%d) и %s (%s, %s:%d) объявляют один и тот же порт. "+
						"Второй сервис не сможет занять сокет и будет падать при старте.",
						x.Service, x.Label, x.Address, x.Port, y.Service, y.Label, y.Address, y.Port),
					File:       x.File,
					Line:       x.Line,
					Suggestion: "Разведите сервисы по разным портам или адресам привязки.",
					Refs:       []string{x.File, y.File},
				})
			}
		}
	}
}

func addressesOverlap(a, b model.Endpoint) bool {
	if a.Public() || b.Public() {
		return true
	}
	return a.Address == b.Address
}

// sameSocketTwoLayers reports whether a pair describes one socket observed at
// two levels rather than two services fighting over a port.
//
// Two cases occur in practice: two published ports of the same container, and a
// container publishing a port straight through to the service configured inside
// it (8404 → 8404), which happens whenever a config directory is mounted into a
// container and inspected from outside.
func sameSocketTwoLayers(a, b model.Endpoint) bool {
	if a.Service == model.ServiceDocker && b.Service == model.ServiceDocker {
		return a.Extra["container"] != "" && a.Extra["container"] == b.Extra["container"]
	}

	docker, other := a, b
	if b.Service == model.ServiceDocker {
		docker, other = b, a
	} else if a.Service != model.ServiceDocker {
		return false
	}
	if other.Service == model.ServiceDocker {
		return false
	}
	// The published port forwards to the very port the other service binds.
	containerPort, err := strconv.Atoi(docker.Extra["container_port"])
	return err == nil && containerPort == other.Port
}

func ruleDeclaredNotListening(c *collector, s *model.Snapshot, idx *index) {
	if !idx.listenersComparable {
		return
	}
	for _, e := range s.Endpoints {
		if e.Protocol == "udp" {
			continue
		}
		// Published container ports are deliberately excluded. The Docker API
		// already states whether a mapping exists, and the socket table is the
		// wrong witness for it: with userland-proxy disabled docker forwards by
		// DNAT alone and no process listens on the host at all. A container that
		// is not actually up is reported by the container rules instead.
		if e.Service == model.ServiceDocker {
			continue
		}
		// Only complain when the owning service is supposed to be running.
		if !idx.serviceActive[e.Service] {
			continue
		}
		if _, ok := idx.listenerFor(e.Address, e.Port); ok {
			continue
		}
		c.add(model.Finding{
			Rule:     "declared-not-listening",
			ID:       "declared-not-listening:" + e.ID,
			Severity: model.SeverityHigh,
			Service:  e.Service,
			Object:   e.Socket(),
			Title:    fmt.Sprintf("Порт %d объявлен в конфиге, но никто его не слушает", e.Port),
			Detail: fmt.Sprintf("%s (%s) объявляет %s, но в выводе ss такого слушателя нет. "+
				"Либо конфиг не применён (нужен reload), либо сервис не смог занять порт.",
				e.Service, e.Label, e.Socket()),
			File:       e.File,
			Line:       e.Line,
			Suggestion: fmt.Sprintf("Проверьте, применён ли конфиг: перезагрузите %s и посмотрите журнал.", e.Service),
		})
	}
}

func ruleListeningNotDeclared(c *collector, s *model.Snapshot, idx *index) {
	for _, l := range s.Listeners {
		if len(idx.endpointsByPort[l.Port]) > 0 {
			continue
		}
		if l.Process == "docker-proxy" {
			continue // covered by the container rules
		}
		severity := model.SeverityInfo
		detail := fmt.Sprintf("Процесс %s слушает %s:%d, но этот порт не описан ни в одном из разобранных конфигов.",
			l.Process, l.Address, l.Port)
		if l.Public() {
			severity = model.SeverityMedium
			detail += " Сокет открыт на всех интерфейсах."
		}
		c.add(model.Finding{
			Rule:       "listening-not-declared",
			ID:         fmt.Sprintf("listening-not-declared:%s:%d", l.Address, l.Port),
			Severity:   severity,
			Service:    model.ServiceHost,
			Object:     fmt.Sprintf("%s:%d", l.Address, l.Port),
			Title:      fmt.Sprintf("Неучтённый слушатель на порту %d (%s)", l.Port, l.Process),
			Detail:     detail,
			Suggestion: "Убедитесь, что сервис нужен, и опишите его в конфигурации или закройте порт.",
		})
	}
}

func ruleFirewallDefaultPolicy(c *collector, s *model.Snapshot, idx *index) {
	if len(s.Firewall.Rules) == 0 && len(s.Firewall.Policies) == 0 {
		return
	}
	if idx.inputPolicy == "ACCEPT" && !s.Firewall.UFWActive {
		c.add(model.Finding{
			Rule:     "no-default-deny",
			ID:       "no-default-deny:INPUT",
			Severity: model.SeverityHigh,
			Service:  model.ServiceIptables,
			Object:   "filter/INPUT",
			Title:    "Политика INPUT по умолчанию — ACCEPT, ufw выключен",
			Detail: "Входящий трафик разрешён по умолчанию: любой открытый порт доступен извне " +
				"вне зависимости от того, планировали вы это или нет.",
			Suggestion: "Включите ufw (ufw default deny incoming) либо задайте iptables -P INPUT DROP " +
				"и явно разрешите нужные порты.",
		})
	}
}

func rulePublicPortWithoutRule(c *collector, s *model.Snapshot, idx *index) {
	if idx.inputPolicy != "DROP" && idx.inputPolicy != "REJECT" && !s.Firewall.UFWActive {
		return // no default-deny: nothing to be blocked by
	}
	for _, e := range s.Endpoints {
		if !e.Public() || e.Protocol == "udp" {
			continue
		}
		if e.Service == model.ServiceDocker {
			continue // docker publishes through DNAT, handled separately
		}
		if len(idx.allowedFrom[e.Port]) > 0 {
			continue
		}
		c.add(model.Finding{
			Rule:     "public-port-blocked",
			ID:       "public-port-blocked:" + e.ID,
			Severity: model.SeverityMedium,
			Service:  e.Service,
			Object:   e.Socket(),
			Title:    fmt.Sprintf("Порт %d открыт сервисом, но закрыт firewall", e.Port),
			Detail: fmt.Sprintf("%s (%s) слушает %s на всех интерфейсах, но правил, разрешающих "+
				"входящий трафик на этот порт, нет, а политика INPUT — %s. Снаружи сервис недоступен.",
				e.Service, e.Label, e.Socket(), idx.inputPolicy),
			File:       e.File,
			Line:       e.Line,
			Suggestion: fmt.Sprintf("Если сервис должен быть доступен: ufw allow %d/tcp.", e.Port),
		})
	}
}

func ruleDockerBypassesFirewall(c *collector, s *model.Snapshot, idx *index) {
	if !s.Firewall.UFWActive && idx.inputPolicy != "DROP" && idx.inputPolicy != "REJECT" {
		return
	}
	for _, ct := range s.Container {
		for _, p := range ct.Ports {
			if !p.PublicallyBound() {
				continue
			}
			severity := model.SeverityHigh
			extra := ""
			if name, ok := sensitivePorts[p.HostPort]; ok {
				severity = model.SeverityCritical
				extra = fmt.Sprintf(" На этом порту работает %s — публиковать его наружу почти наверняка не нужно.", name)
			}
			c.add(model.Finding{
				Rule:     "docker-bypasses-firewall",
				ID:       fmt.Sprintf("docker-bypasses-firewall:%s:%d", ct.Name, p.HostPort),
				Severity: severity,
				Service:  model.ServiceDocker,
				Object:   fmt.Sprintf("%s:%d", ct.Name, p.HostPort),
				Title: fmt.Sprintf("Контейнер %s публикует порт %d на 0.0.0.0 в обход firewall",
					ct.Name, p.HostPort),
				Detail: fmt.Sprintf("Docker добавляет правила DNAT в цепочку PREROUTING/FORWARD, "+
					"поэтому опубликованный порт %d не проходит через INPUT и не закрывается правилами ufw.%s",
					p.HostPort, extra),
				File: ct.ComposeFile,
				Suggestion: fmt.Sprintf("Привяжите публикацию к localhost (\"127.0.0.1:%d:%d\") "+
					"или используйте цепочку DOCKER-USER для фильтрации.", p.HostPort, p.ContainerPort),
			})
		}
	}
}

func ruleStaleFirewallRules(c *collector, s *model.Snapshot, idx *index) {
	if !idx.listenersComparable {
		return
	}
	for _, r := range s.Firewall.Rules {
		if !isAcceptAction(r.Action) || !isInputChain(r.Chain) || r.Backend == "ufw6" {
			continue
		}
		for _, port := range r.Ports {
			if idx.hasListener(port) || idx.dockerDNAT[port] {
				continue
			}
			detail := fmt.Sprintf("Правило разрешает входящий трафик на порт %d, но на хосте нет "+
				"процесса, который его слушает.", port)
			if r.Packets == 0 {
				detail += " Счётчик правила равен нулю — трафика по нему не было."
			}
			c.add(model.Finding{
				Rule:       "stale-firewall-rule",
				ID:         fmt.Sprintf("stale-firewall-rule:%s:%d", r.Backend, port),
				Severity:   model.SeverityLow,
				Service:    model.ServiceIptables,
				Object:     fmt.Sprintf("%s %d/tcp", r.Backend, port),
				Title:      fmt.Sprintf("Правило firewall для порта %d не используется", port),
				Detail:     detail,
				Suggestion: fmt.Sprintf("Удалите правило, если сервис больше не нужен: ufw delete allow %d/tcp.", port),
				Refs:       []string{r.Raw},
			})
		}
	}
}

func ruleSensitivePortsPublic(c *collector, s *model.Snapshot, idx *index) {
	for _, l := range s.Listeners {
		if !l.Public() {
			continue
		}
		name, ok := sensitivePorts[l.Port]
		if !ok {
			continue
		}
		reachable := idx.allowedFromAnywhere(l.Port) || idx.dockerDNAT[l.Port] ||
			(idx.inputPolicy == "ACCEPT" && !s.Firewall.UFWActive)
		severity := model.SeverityHigh
		suffix := ""
		if reachable {
			severity = model.SeverityCritical
			suffix = " Порт при этом не закрыт правилами firewall."
		}
		c.add(model.Finding{
			Rule:     "sensitive-port-public",
			ID:       fmt.Sprintf("sensitive-port-public:%d", l.Port),
			Severity: severity,
			Service:  model.ServiceHost,
			Object:   fmt.Sprintf("0.0.0.0:%d", l.Port),
			Title:    fmt.Sprintf("%s слушает порт %d на всех интерфейсах", name, l.Port),
			Detail: fmt.Sprintf("Процесс %s принимает подключения на 0.0.0.0:%d.%s Такие сервисы обычно "+
				"не рассчитаны на публичный доступ и не имеют собственной защиты от перебора.",
				l.Process, l.Port, suffix),
			Suggestion: "Привяжите сервис к 127.0.0.1 или к внутренней сети и закройте порт на firewall.",
		})
	}
}

func ruleTLS(c *collector, s *model.Snapshot) {
	for _, e := range s.Endpoints {
		protocols := e.Extra["ssl_protocols"]
		if protocols == "" {
			continue
		}
		var weak []string
		for _, v := range weakTLSVersions {
			for _, field := range strings.Fields(protocols) {
				if field == v {
					weak = append(weak, v)
				}
			}
		}
		if len(weak) > 0 {
			c.add(model.Finding{
				Rule:       "weak-tls",
				ID:         "weak-tls:" + e.ID,
				Severity:   model.SeverityMedium,
				Service:    e.Service,
				Object:     e.Socket(),
				Title:      "Включены устаревшие версии TLS: " + strings.Join(weak, ", "),
				Detail:     fmt.Sprintf("ssl_protocols = %q. Эти версии считаются небезопасными и отключены в современных браузерах.", protocols),
				File:       e.File,
				Line:       e.Line,
				Suggestion: "Оставьте только TLSv1.2 и TLSv1.3: ssl_protocols TLSv1.2 TLSv1.3;",
			})
		}
	}
	for _, e := range s.Endpoints {
		if !e.TLS || e.Service != model.ServiceNginx {
			continue
		}
		if e.Extra["hsts"] == "" {
			c.add(model.Finding{
				Rule:       "missing-hsts",
				ID:         "missing-hsts:" + e.ID,
				Severity:   model.SeverityLow,
				Service:    e.Service,
				Object:     e.Socket(),
				Title:      fmt.Sprintf("Нет заголовка HSTS на %s", e.Label),
				Detail:     "TLS-сервер не отдаёт Strict-Transport-Security, поэтому клиент может быть возвращён на http.",
				File:       e.File,
				Line:       e.Line,
				Suggestion: `add_header Strict-Transport-Security "max-age=31536000" always;`,
			})
		}
		if e.Extra["tls_cert_missing"] == "yes" {
			c.add(model.Finding{
				Rule:       "tls-cert-missing",
				ID:         "tls-cert-missing:" + e.ID,
				Severity:   model.SeverityHigh,
				Service:    e.Service,
				Object:     e.Socket(),
				Title:      fmt.Sprintf("listen ... ssl без ssl_certificate на %s", e.Label),
				Detail:     "Слушатель объявлен как TLS, но сертификат в блоке не задан — nginx не запустится.",
				File:       e.File,
				Line:       e.Line,
				Suggestion: "Добавьте ssl_certificate и ssl_certificate_key.",
			})
		}
	}
}

func rulePlaintextProxy(c *collector, s *model.Snapshot) {
	for _, e := range s.Endpoints {
		if e.TLS || !e.Public() || e.Mode == "tcp" || e.Service == model.ServiceDocker {
			continue
		}
		// A plain-HTTP listener that only redirects to https is the correct pattern.
		proxies := false
		for _, r := range e.Routes {
			if r.TargetKind == "upstream" || r.TargetKind == "address" {
				proxies = true
			}
		}
		if !proxies {
			continue
		}
		c.add(model.Finding{
			Rule:     "public-plaintext-proxy",
			ID:       "public-plaintext-proxy:" + e.ID,
			Severity: model.SeverityMedium,
			Service:  e.Service,
			Object:   e.Socket(),
			Title:    fmt.Sprintf("%s проксирует трафик по HTTP без TLS", e.Label),
			Detail: fmt.Sprintf("Слушатель %s принимает запросы на всех интерфейсах без шифрования "+
				"и передаёт их дальше. Заголовки, cookie и токены идут открытым текстом.", e.Socket()),
			File:       e.File,
			Line:       e.Line,
			Suggestion: "Переведите сервис на https или оставьте на 80 только редирект на https.",
		})
	}
}

func ruleUpstreams(c *collector, s *model.Snapshot, idx *index) {
	// Routes pointing at an upstream that does not exist.
	for _, e := range s.Endpoints {
		for _, r := range e.Routes {
			if r.TargetKind != "upstream" {
				continue
			}
			if idx.upstreamNames[e.Service+"|"+r.Target] != nil {
				continue
			}
			c.add(model.Finding{
				Rule:     "upstream-undefined",
				ID:       fmt.Sprintf("upstream-undefined:%s:%s", e.ID, r.Target),
				Severity: model.SeverityHigh,
				Service:  e.Service,
				Object:   r.Target,
				Title:    fmt.Sprintf("Ссылка на несуществующий upstream %q", r.Target),
				Detail: fmt.Sprintf("Маршрут %q в %s указывает на пул %q, но такой пул не определён.",
					r.Match, e.Label, r.Target),
				File:       r.File,
				Line:       r.Line,
				Suggestion: "Проверьте имя пула или добавьте соответствующий блок upstream/backend.",
			})
		}
	}

	for i := range s.Upstreams {
		u := s.Upstreams[i]

		if !idx.upstreamUsed[u.Service+"|"+u.Name] {
			c.add(model.Finding{
				Rule:       "upstream-orphan",
				ID:         "upstream-orphan:" + u.ID,
				Severity:   model.SeverityLow,
				Service:    u.Service,
				Object:     u.Name,
				Title:      fmt.Sprintf("Пул %q объявлен, но нигде не используется", u.Name),
				Detail:     "Ни один маршрут не ссылается на этот пул — вероятно, остаток от прошлой конфигурации.",
				File:       u.File,
				Line:       u.Line,
				Suggestion: "Удалите неиспользуемый блок или подключите его к нужному маршруту.",
			})
		}

		// Local backend members that nothing is listening on.
		for _, srv := range u.Servers {
			if !isLocalHost(srv.Host) || !idx.listenersComparable {
				continue
			}
			if idx.hasListener(srv.Port) {
				continue
			}
			c.add(model.Finding{
				Rule:     "upstream-member-down",
				ID:       fmt.Sprintf("upstream-member-down:%s:%s", u.ID, srv.Socket()),
				Severity: model.SeverityHigh,
				Service:  u.Service,
				Object:   fmt.Sprintf("%s -> %s", u.Name, srv.Socket()),
				Title:    fmt.Sprintf("Backend %s пула %q не слушает порт", srv.Socket(), u.Name),
				Detail: fmt.Sprintf("Пул %q отправляет трафик на %s, но локально этот порт никем не занят. "+
					"Запросы будут завершаться ошибкой 502/504.", u.Name, srv.Socket()),
				File:       u.File,
				Line:       u.Line,
				Suggestion: "Поднимите сервис на этом порту или уберите его из пула.",
			})
		}

		active := 0
		for _, srv := range u.Servers {
			if !srv.Backup && !srv.Down {
				active++
			}
		}
		if active == 1 && len(u.Servers) == 1 {
			c.add(model.Finding{
				Rule:       "single-backend",
				ID:         "single-backend:" + u.ID,
				Severity:   model.SeverityInfo,
				Service:    u.Service,
				Object:     u.Name,
				Title:      fmt.Sprintf("В пуле %q один сервер — нет резерва", u.Name),
				Detail:     "Отказ единственного backend приведёт к полной недоступности маршрута.",
				File:       u.File,
				Line:       u.Line,
				Suggestion: "Добавьте второй сервер или явный backup.",
			})
		}
		if active == 0 && len(u.Servers) > 0 {
			c.add(model.Finding{
				Rule:       "all-backends-disabled",
				ID:         "all-backends-disabled:" + u.ID,
				Severity:   model.SeverityCritical,
				Service:    u.Service,
				Object:     u.Name,
				Title:      fmt.Sprintf("Все серверы пула %q помечены down/backup", u.Name),
				Detail:     "Активных серверов не осталось — весь трафик на этот пул будет отвергнут.",
				File:       u.File,
				Line:       u.Line,
				Suggestion: "Верните в строй хотя бы один сервер.",
			})
		}
	}
}

func ruleHealthChecks(c *collector, s *model.Snapshot) {
	for _, u := range s.Upstreams {
		if u.Health != "" || len(u.Servers) < 2 {
			continue
		}
		checked := 0
		for _, srv := range u.Servers {
			if srv.Checked {
				checked++
			}
		}
		if checked == len(u.Servers) {
			continue
		}
		c.add(model.Finding{
			Rule:     "backend-no-healthcheck",
			ID:       "backend-no-healthcheck:" + u.ID,
			Severity: model.SeverityMedium,
			Service:  u.Service,
			Object:   u.Name,
			Title:    fmt.Sprintf("В пуле %q нет проверки здоровья серверов", u.Name),
			Detail: fmt.Sprintf("Из %d серверов проверку имеют %d. Балансировщик будет продолжать "+
				"отправлять запросы на упавший backend.", len(u.Servers), checked),
			File: u.File,
			Line: u.Line,
			Suggestion: map[string]string{
				model.ServiceHAProxy: "Добавьте параметр check к каждой строке server и option httpchk в backend.",
				model.ServiceNginx:   "Задайте max_fails и fail_timeout для пассивной проверки.",
			}[u.Service],
		})
	}
}

func ruleContainers(c *collector, s *model.Snapshot) {
	for _, ct := range s.Container {
		switch {
		case ct.State == "restarting":
			c.add(model.Finding{
				Rule:       "container-restarting",
				ID:         "container-restarting:" + ct.Name,
				Severity:   model.SeverityHigh,
				Service:    model.ServiceDocker,
				Object:     ct.Name,
				Title:      fmt.Sprintf("Контейнер %s в цикле перезапуска", ct.Name),
				Detail:     fmt.Sprintf("Статус: %s. Обычно это конфликт порта, ошибка конфигурации или падение процесса на старте.", ct.Status),
				File:       ct.ComposeFile,
				Suggestion: fmt.Sprintf("Посмотрите журнал: docker logs %s.", ct.Name),
			})
		case ct.Declared && !ct.Running:
			c.add(model.Finding{
				Rule:       "container-not-running",
				ID:         "container-not-running:" + ct.Name,
				Severity:   model.SeverityMedium,
				Service:    model.ServiceDocker,
				Object:     ct.Name,
				Title:      fmt.Sprintf("Контейнер %s описан в compose, но не запущен", ct.Name),
				Detail:     fmt.Sprintf("Сервис %q из файла %s отсутствует среди работающих контейнеров.", ct.ServiceName, ct.ComposeFile),
				File:       ct.ComposeFile,
				Suggestion: "Запустите стек: docker compose up -d.",
			})
		case ct.Running && !ct.Declared:
			c.add(model.Finding{
				Rule:       "container-undeclared",
				ID:         "container-undeclared:" + ct.Name,
				Severity:   model.SeverityLow,
				Service:    model.ServiceDocker,
				Object:     ct.Name,
				Title:      fmt.Sprintf("Контейнер %s запущен вне compose-файлов", ct.Name),
				Detail:     "Контейнер работает, но не описан ни в одном из известных compose-файлов — его состояние не воспроизводимо.",
				Suggestion: "Опишите контейнер в compose или добавьте его файл в NKT_COMPOSE_FILES.",
			})
		}

		if ct.Declared && ct.Restart == "" {
			c.add(model.Finding{
				Rule:       "container-no-restart-policy",
				ID:         "container-no-restart-policy:" + ct.Name,
				Severity:   model.SeverityLow,
				Service:    model.ServiceDocker,
				Object:     ct.Name,
				Title:      fmt.Sprintf("У контейнера %s не задана политика перезапуска", ct.Name),
				Detail:     "После перезагрузки хоста или падения процесса контейнер не поднимется сам.",
				File:       ct.ComposeFile,
				Suggestion: "Добавьте restart: unless-stopped.",
			})
		}
	}
}

func ruleAdminInterfaces(c *collector, s *model.Snapshot) {
	for _, e := range s.Endpoints {
		if e.Extra["stats"] != "enabled" {
			continue
		}
		if e.Extra["stats_auth"] != "none" {
			continue
		}
		severity := model.SeverityMedium
		if e.Public() {
			severity = model.SeverityHigh
		}
		c.add(model.Finding{
			Rule:     "admin-interface-open",
			ID:       "admin-interface-open:" + e.ID,
			Severity: severity,
			Service:  e.Service,
			Object:   e.Socket(),
			Title:    fmt.Sprintf("Панель статистики %s доступна без пароля", e.Label),
			Detail: fmt.Sprintf("Секция %q включает stats и слушает %s, но директивы stats auth нет. "+
				"Любой, кто дотянется до порта, увидит состав backend-ов и состояние сервисов.", e.Label, e.Socket()),
			File:       e.File,
			Line:       e.Line,
			Suggestion: "Добавьте stats auth <user>:<password> и привяжите bind к внутреннему адресу.",
		})
	}
}

func isLocalHost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}
