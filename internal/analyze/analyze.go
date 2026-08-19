// Package analyze turns a parsed host snapshot into a list of concrete
// problems: port conflicts, firewall holes, dead upstreams, weak TLS and
// containers that do not match what compose declares.
package analyze

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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
	// A clean host with zero findings is the goal, not an edge case — the
	// zero value of collector.findings is nil, and encoding/json marshals a
	// nil slice as `null` rather than `[]` (Findings has no `omitempty`),
	// which would crash every frontend page that calls .filter/.map on it.
	c := &collector{findings: []model.Finding{}}
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
	ruleCertificates(c, s)
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
		if !isInputChain(r) {
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

// isInputChain reports whether a rule governs incoming traffic. For
// iptables/ufw that's a property of the chain name; firewalld has no
// equivalent chain concept at all — every port/service/rich-rule entry in a
// zone listing IS an inbound-allow rule by construction, so its Backend
// alone is enough, regardless of what its Chain (the zone name) says.
func isInputChain(r model.FirewallRule) bool {
	if r.Backend == "firewalld" {
		return true
	}
	c := strings.ToLower(r.Chain)
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

// undeclaredListeners returns every s.Listeners entry idx has no matching
// endpoint for — the "разное"/misc set, shared between ruleListeningNotDeclared
// (turns each into a Finding) and UndeclaredListeners (a plain list for
// callers, namely the /api/misc handler, that just want the services
// themselves rather than a problem report about them).
func undeclaredListeners(s *model.Snapshot, idx *index) []model.Listener {
	var out []model.Listener
	for _, l := range s.Listeners {
		if len(idx.endpointsByPort[l.Port]) > 0 {
			continue
		}
		if l.Process == "docker-proxy" {
			continue // covered by the container rules
		}
		out = append(out, l)
	}
	return out
}

// UndeclaredListeners is undeclaredListeners for callers outside this
// package that only have a Snapshot, not an already-built index — building
// one here is cheap (a handful of map inserts over the snapshot's own
// listeners/endpoints), so there is no need to expose the index type itself.
func UndeclaredListeners(s *model.Snapshot) []model.Listener {
	return undeclaredListeners(s, buildIndex(s))
}

func ruleListeningNotDeclared(c *collector, s *model.Snapshot, idx *index) {
	for _, l := range undeclaredListeners(s, idx) {
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
	if idx.inputPolicy == "ACCEPT" && !s.Firewall.AnyManagerActive() {
		c.add(model.Finding{
			Rule:     "no-default-deny",
			ID:       "no-default-deny:INPUT",
			Severity: model.SeverityHigh,
			Service:  model.ServiceIptables,
			Object:   "filter/INPUT",
			Title:    "Политика INPUT по умолчанию — ACCEPT, менеджер firewall не активен",
			Detail: "Входящий трафик разрешён по умолчанию: любой открытый порт доступен извне " +
				"вне зависимости от того, планировали вы это или нет.",
			Suggestion: "Включите ufw (ufw default deny incoming) или firewalld, либо задайте " +
				"iptables -P INPUT DROP и явно разрешите нужные порты.",
		})
	}
}

func rulePublicPortWithoutRule(c *collector, s *model.Snapshot, idx *index) {
	if idx.inputPolicy != "DROP" && idx.inputPolicy != "REJECT" && !s.Firewall.AnyManagerActive() {
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
	if !s.Firewall.AnyManagerActive() && idx.inputPolicy != "DROP" && idx.inputPolicy != "REJECT" {
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

	// A ufw rule and the iptables rules it generates describe one thing. Report
	// each unused port once, preferring the ufw view — that is where an operator
	// would actually delete it.
	type candidate struct {
		rule model.FirewallRule
		port int
	}
	best := map[int]candidate{}

	for _, r := range s.Firewall.Rules {
		if !isAcceptAction(r.Action) || !isInputChain(r) || r.Backend == "ufw6" {
			continue
		}
		for _, port := range r.Ports {
			if idx.hasListener(port) || idx.dockerDNAT[port] {
				continue
			}
			existing, seen := best[port]
			if !seen || (r.Backend == "ufw" && existing.rule.Backend != "ufw") {
				best[port] = candidate{rule: r, port: port}
			}
		}
	}

	ports := make([]int, 0, len(best))
	for port := range best {
		ports = append(ports, port)
	}
	sort.Ints(ports)

	for _, port := range ports {
		r := best[port].rule
		detail := fmt.Sprintf("Правило разрешает входящий трафик на порт %d, но на хосте нет "+
			"процесса, который его слушает.", port)
		if r.Packets == 0 {
			detail += " Счётчик правила равен нулю — трафика по нему не было."
		}
		c.add(model.Finding{
			Rule:       "stale-firewall-rule",
			ID:         fmt.Sprintf("stale-firewall-rule:%d", port),
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
			(idx.inputPolicy == "ACCEPT" && !s.Firewall.AnyManagerActive())
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
				Rule:     "tls-cert-missing",
				ID:       "tls-cert-missing:" + e.ID,
				Severity: model.SeverityHigh,
				Service:  e.Service,
				Object:   e.Socket(),
				Title:    fmt.Sprintf("listen ... ssl без ssl_certificate на %s", e.Label),
				Detail:   "Слушатель объявлен как TLS, но сертификат в блоке не задан — nginx не запустится.",
				File:     e.File,
				Line:     e.Line,
				Suggestion: "Добавьте ssl_certificate и ssl_certificate_key — быстрый способ получить " +
					"файлы: сгенерировать самоподписанный сертификат на странице «Сертификаты».",
			})
		}
	}
}

// Expiry thresholds. Let's Encrypt renews at 30 days left, so anything below
// that means automation has already had its chance and did not take it.
const (
	certWarnDays     = 30
	certCriticalDays = 7
)

// minRSABits is the smallest RSA key still considered adequate.
const minRSABits = 2048

func ruleCertificates(c *collector, s *model.Snapshot) {
	for _, cert := range s.Certs {
		where := strings.Join(cert.Endpoints, ", ")
		if where == "" {
			where = cert.Service
		}

		if cert.Error != "" {
			c.add(model.Finding{
				Rule:     "tls-cert-unreadable",
				ID:       "tls-cert-unreadable:" + cert.Path,
				Severity: model.SeverityHigh,
				Service:  cert.Service,
				Object:   cert.Path,
				Title:    "Сертификат не читается: " + certLabel(cert),
				Detail: fmt.Sprintf("%s указан в конфигурации для %s, но прочитать его не удалось: %s. "+
					"Если файла действительно нет, сервис не поднимет TLS-слушатель.",
					cert.Path, where, cert.Error),
				File:       cert.Path,
				Suggestion: "Проверьте путь и права доступа, при необходимости выпустите сертификат заново.",
			})
			continue
		}

		switch {
		case cert.DaysLeft < 0:
			c.add(model.Finding{
				Rule:     "tls-cert-expired",
				ID:       "tls-cert-expired:" + cert.Path,
				Severity: model.SeverityCritical,
				Service:  cert.Service,
				Object:   cert.Path,
				Title: fmt.Sprintf("Сертификат %s просрочен на %d дн.",
					strings.Join(cert.Names, ", "), -cert.DaysLeft),
				Detail: fmt.Sprintf("Срок действия истёк %s. Обслуживает %s. "+
					"Браузеры показывают ошибку и не пускают пользователей дальше.",
					cert.NotAfter.Local().Format("02.01.2006"), where),
				File:       cert.Path,
				Suggestion: renewalSuggestion(cert),
			})
		case cert.DaysLeft <= certCriticalDays:
			c.add(model.Finding{
				Rule:     "tls-cert-expiring",
				ID:       "tls-cert-expiring:" + cert.Path,
				Severity: model.SeverityHigh,
				Service:  cert.Service,
				Object:   cert.Path,
				Title: fmt.Sprintf("Сертификат %s истекает через %d дн.",
					strings.Join(cert.Names, ", "), cert.DaysLeft),
				Detail: fmt.Sprintf("Действителен до %s, обслуживает %s. %s",
					cert.NotAfter.Local().Format("02.01.2006 15:04"), where, renewalState(cert)),
				File:       cert.Path,
				Suggestion: renewalSuggestion(cert),
			})
		case cert.DaysLeft <= certWarnDays:
			c.add(model.Finding{
				Rule:     "tls-cert-expiring",
				ID:       "tls-cert-expiring:" + cert.Path,
				Severity: model.SeverityMedium,
				Service:  cert.Service,
				Object:   cert.Path,
				Title: fmt.Sprintf("Сертификат %s истекает через %d дн.",
					strings.Join(cert.Names, ", "), cert.DaysLeft),
				Detail: fmt.Sprintf("Действителен до %s, обслуживает %s. %s",
					cert.NotAfter.Local().Format("02.01.2006"), where, renewalState(cert)),
				File:       cert.Path,
				Suggestion: renewalSuggestion(cert),
			})
		}

		if time.Now().Before(cert.NotBefore) {
			c.add(model.Finding{
				Rule:     "tls-cert-not-yet-valid",
				ID:       "tls-cert-not-yet-valid:" + cert.Path,
				Severity: model.SeverityHigh,
				Service:  cert.Service,
				Object:   cert.Path,
				Title:    "Сертификат ещё не вступил в силу: " + certLabel(cert),
				Detail: fmt.Sprintf("Действителен только с %s. Обычно это значит, что часы на хосте "+
					"отстают или сертификат выпущен «на будущее».",
					cert.NotBefore.Local().Format("02.01.2006 15:04")),
				File:       cert.Path,
				Suggestion: "Проверьте системное время и дату выпуска сертификата.",
			})
		}

		// Automation that nothing triggers is the failure mode that produces an
		// expired certificate on a host where everyone believed it was handled.
		if cert.Renewal.Managed && !cert.Renewal.Automatic {
			c.add(model.Finding{
				Rule:     "tls-cert-renewal-not-automatic",
				ID:       "tls-cert-renewal-not-automatic:" + cert.Path,
				Severity: model.SeverityMedium,
				Service:  cert.Service,
				Object:   cert.Path,
				Title:    "Автообновление сертификата не запускается: " + certLabel(cert),
				Detail:   cert.Renewal.Detail,
				File:     cert.Path,
				Suggestion: "Включите таймер: systemctl enable --now certbot.timer — " +
					"либо добавьте задание cron.",
			})
		}
		if !cert.Renewal.Managed && cert.Renewal.Tool == "certbot" {
			c.add(model.Finding{
				Rule:     "tls-cert-orphan-lineage",
				ID:       "tls-cert-orphan-lineage:" + cert.Path,
				Severity: model.SeverityHigh,
				Service:  cert.Service,
				Object:   cert.Path,
				Title:    "Сертификат certbot остался без файла обновления",
				Detail:   cert.Renewal.Detail,
				File:     cert.Path,
				Suggestion: "Выпустите сертификат заново через certbot certonly, " +
					"чтобы восстановить запись обновления.",
			})
		}

		// The most useful check in this whole rule set: not "is the file on
		// disk healthy" but "is what clients actually receive the same file".
		// certbot renewing a certificate that nginx never reloaded produces a
		// perfectly healthy-looking file next to an expiring live connection.
		if cert.Serving.Checked && cert.Serving.Error == "" && !cert.Serving.Match {
			c.add(model.Finding{
				Rule:     "tls-cert-not-reloaded",
				ID:       "tls-cert-not-reloaded:" + cert.Path,
				Severity: model.SeverityHigh,
				Service:  cert.Service,
				Object:   cert.Path,
				Title:    "На сокете отдаётся другой сертификат, чем указан в конфиге",
				Detail: fmt.Sprintf("Файл %s не совпадает с тем, что реально отдаёт %s при TLS-подключении: "+
					"на сокете сертификат с серийным номером %s, действителен до %s. Обычно это значит, "+
					"что файл на диске обновили (например, certbot renew), а сервис не перечитал конфигурацию.",
					cert.Path, cert.Serving.Endpoint, cert.Serving.ServedSerial,
					cert.Serving.ServedNotAfter.Local().Format("02.01.2006")),
				File:       cert.Path,
				Suggestion: fmt.Sprintf("Перезагрузите %s, чтобы он подхватил актуальный сертификат.", cert.Service),
			})
		}

		if cert.SelfSigned {
			c.add(model.Finding{
				Rule:     "tls-cert-self-signed",
				ID:       "tls-cert-self-signed:" + cert.Path,
				Severity: model.SeverityLow,
				Service:  cert.Service,
				Object:   cert.Path,
				Title:    "Самоподписанный сертификат на " + where,
				Detail: fmt.Sprintf("Издатель совпадает с субъектом (%s). Такому сертификату не доверяет "+
					"ни один браузер; для внутренних сервисов это допустимо, для публичных — нет.",
					cert.Subject),
				File:       cert.Path,
				Suggestion: "Для публичного сервиса выпустите сертификат в доверенном центре.",
			})
		}

		if cert.KeyAlgorithm == "RSA" && cert.KeyBits > 0 && cert.KeyBits < minRSABits {
			c.add(model.Finding{
				Rule:     "tls-cert-weak-key",
				ID:       "tls-cert-weak-key:" + cert.Path,
				Severity: model.SeverityMedium,
				Service:  cert.Service,
				Object:   cert.Path,
				Title:    fmt.Sprintf("Слабый ключ RSA %d бит", cert.KeyBits),
				Detail: fmt.Sprintf("Сертификат %s использует ключ короче %d бит. "+
					"Современные клиенты такие соединения отклоняют.", cert.Path, minRSABits),
				File:       cert.Path,
				Suggestion: "Перевыпустите сертификат с ключом RSA 2048+ или ECDSA P-256.",
			})
		}

		if isWeakSignature(cert.SigAlgorithm) {
			c.add(model.Finding{
				Rule:     "tls-cert-weak-signature",
				ID:       "tls-cert-weak-signature:" + cert.Path,
				Severity: model.SeverityMedium,
				Service:  cert.Service,
				Object:   cert.Path,
				Title:    "Устаревший алгоритм подписи: " + cert.SigAlgorithm,
				Detail: "Подписи на основе SHA-1 и MD5 считаются небезопасными и не принимаются " +
					"современными браузерами.",
				File:       cert.Path,
				Suggestion: "Перевыпустите сертификат с подписью SHA-256 или сильнее.",
			})
		}

		// A certificate that does not cover the name it is served under produces
		// exactly the browser warning it was bought to avoid.
		for _, site := range cert.Sites {
			if cert.CoversName(site) {
				continue
			}
			c.add(model.Finding{
				Rule:     "tls-cert-name-mismatch",
				ID:       fmt.Sprintf("tls-cert-name-mismatch:%s:%s", cert.Path, site),
				Severity: model.SeverityHigh,
				Service:  cert.Service,
				Object:   site,
				Title:    fmt.Sprintf("Сертификат не покрывает имя %s", site),
				Detail: fmt.Sprintf("Сервер отвечает на %s, но сертификат %s выписан на %s. "+
					"Клиент увидит предупреждение о несоответствии имени.",
					site, cert.Path, strings.Join(cert.Names, ", ")),
				File:       cert.Path,
				Suggestion: fmt.Sprintf("Добавьте %s в SAN сертификата или используйте отдельный сертификат.", site),
			})
		}
	}
}

// renewalState describes, in one sentence, whether anything will renew this.
func renewalState(cert model.Certificate) string {
	switch {
	case cert.Renewal.Automatic:
		return "Автообновление настроено: " + cert.Renewal.Detail
	case cert.Renewal.Detail != "":
		return cert.Renewal.Detail
	default:
		return "Автообновление не обнаружено."
	}
}

func renewalSuggestion(cert model.Certificate) string {
	if cert.Renewal.Tool == "certbot" {
		return "Продлите сейчас: certbot renew --cert-name " + lineageOf(cert.Path) +
			", затем перезагрузите сервис."
	}
	return "Выпустите и установите новый сертификат, затем перезагрузите сервис."
}

// lineageOf extracts the certbot lineage name from a live path.
func lineageOf(path string) string {
	const live = "/etc/letsencrypt/live/"
	if !strings.HasPrefix(path, live) {
		return "<имя>"
	}
	rest := strings.TrimPrefix(path, live)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

func isWeakSignature(alg string) bool {
	lower := strings.ToLower(alg)
	return strings.Contains(lower, "sha1") || strings.Contains(lower, "md5") ||
		strings.Contains(lower, "md2")
}

// certLabel names a certificate the way an operator thinks of it — by the site
// it serves, falling back to the file name when the certificate is unreadable.
func certLabel(cert model.Certificate) string {
	if len(cert.Names) > 0 {
		return strings.Join(cert.Names, ", ")
	}
	if len(cert.Sites) > 0 {
		return strings.Join(cert.Sites, ", ")
	}
	if i := strings.LastIndexByte(cert.Path, '/'); i >= 0 && i+1 < len(cert.Path) {
		return cert.Path[i+1:]
	}
	return cert.Path
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
			File: e.File,
			Line: e.Line,
			Suggestion: "Переведите сервис на https (для быстрого теста можно сгенерировать " +
				"самоподписанный сертификат на странице «Сертификаты») или оставьте на 80 только " +
				"редирект на https.",
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
