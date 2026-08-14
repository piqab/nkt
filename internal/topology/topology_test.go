package topology

import (
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/model"
)

// TestBuildNeverReturnsNilEdgesOrFindings guards against a real production
// crash: a minimal or brand-new host can genuinely have zero edges and zero
// findings, and both fields must still be real empty slices, not nil —
// neither has `omitempty`, encoding/json marshals nil as `null`, and the
// resource map crashes calling .filter/.map on `null`.
func TestBuildNeverReturnsNilEdgesOrFindings(t *testing.T) {
	g := Build(&model.Snapshot{})
	if g.Edges == nil {
		t.Error("Edges = nil, ожидался непустой (даже если пустой) срез")
	}
	if g.Findings == nil {
		t.Error("Findings = nil, ожидался непустой (даже если пустой) срез")
	}
	if g.Nodes == nil {
		t.Error("Nodes = nil, ожидался непустой (даже если пустой) срез")
	}
}

func nodeByID(g *Graph, id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

// server_name written in punycode for an IDN domain must carry a readable
// footnote in Meta — the endpoint node's Label/Sublabel show what
// nginx/haproxy actually use, which has to stay ASCII.
func TestEndpointCarriesUnicodeHintForIDNName(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{{
			ID: "nginx:1", Service: model.ServiceNginx, Kind: "server",
			Address: "0.0.0.0", Port: 443, Protocol: "tcp", Mode: "http", TLS: true,
			Names: []string{"xn--80akhbyknj4f.xn--p1ai"},
			Label: "xn--80akhbyknj4f.xn--p1ai",
		}},
	}

	g := Build(snap)
	node := nodeByID(g, "ep:nginx:1")
	if node == nil {
		t.Fatal("узел слушателя не построен")
	}
	if node.Label != "xn--80akhbyknj4f.xn--p1ai" {
		t.Errorf("Label = %q, ожидалось имя ASCII/punycode (то, что реально в конфиге)", node.Label)
	}
	if want := "испытание.рф"; node.Meta["names_unicode"] != want {
		t.Errorf("Meta[names_unicode] = %q, ожидалось %q", node.Meta["names_unicode"], want)
	}
}

// The map's whole point for triage is a top-of-page list of what's actually
// wrong — a coloured dot and a count on the node itself says something is
// broken but not what, so Build must surface the real finding title too.
func TestFindingsSummaryListsNodeAndSeverity(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{{
			ID: "nginx:1", Service: model.ServiceNginx, Kind: "server",
			Address: "0.0.0.0", Port: 443, Protocol: "tcp", Mode: "http", TLS: true,
			Label: "app.example.com",
		}},
		Findings: []model.Finding{
			{Object: "0.0.0.0:443", Severity: model.SeverityHigh, Title: "Устаревший TLS"},
		},
	}

	g := Build(snap)
	if len(g.Findings) != 1 {
		t.Fatalf("Findings = %d, ожидалась 1", len(g.Findings))
	}
	fr := g.Findings[0]
	if fr.NodeID != "ep:nginx:1" || fr.Title != "Устаревший TLS" || fr.Severity != model.SeverityHigh {
		t.Errorf("FindingRef = %+v, не совпадает с ожидаемым", fr)
	}
}

// Sorted worst-first, so the most urgent problem is the one visible without
// scrolling a long list.
func TestFindingsSummarySortsWorstFirst(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{{
			ID: "nginx:1", Service: model.ServiceNginx, Kind: "server",
			Address: "0.0.0.0", Port: 443, Protocol: "tcp", Mode: "http", TLS: true,
			Label: "app.example.com",
		}},
		Findings: []model.Finding{
			{Object: "0.0.0.0:443", Severity: model.SeverityMedium, Title: "Среднее"},
			{Object: "0.0.0.0:443", Severity: model.SeverityCritical, Title: "Критичное"},
		},
	}

	g := Build(snap)
	if len(g.Findings) != 2 || g.Findings[0].Severity != model.SeverityCritical {
		t.Fatalf("Findings = %+v, ожидался critical первым", g.Findings)
	}
}

// A pool referenced by a route must show its real algorithm and health check.
// Building endpoints first used to leave a placeholder node behind, so every
// referenced pool was drawn as undefined and red.
func TestReferencedUpstreamKeepsItsRealLabel(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{{
			ID: "haproxy:fe", Service: model.ServiceHAProxy, Kind: "frontend",
			Address: "0.0.0.0", Port: 8090, Protocol: "tcp", Mode: "http", Label: "fe_http",
			Routes: []model.Route{{Match: "default", Target: "be_app", TargetKind: "upstream"}},
		}},
		Upstreams: []model.Upstream{{
			ID: "haproxy:upstream:be_app", Name: "be_app", Service: model.ServiceHAProxy,
			Algorithm: "leastconn", Health: "httpchk GET /healthz",
			Servers: []model.UpstreamServer{
				{Name: "app1", Host: "127.0.0.1", Port: 8080, Checked: true},
				{Name: "app2", Host: "127.0.0.1", Port: 8081, Checked: true},
			},
		}},
	}

	g := Build(snap)
	node := nodeByID(g, "up:haproxy:be_app")
	if node == nil {
		t.Fatal("узел пула be_app не построен")
	}
	if strings.Contains(node.Sublabel, "не определён") {
		t.Errorf("определённый пул помечен как несуществующий: %q", node.Sublabel)
	}
	if !strings.Contains(node.Sublabel, "leastconn") || !strings.Contains(node.Sublabel, "httpchk") {
		t.Errorf("подпись пула = %q, ожидались алгоритм и проверка здоровья", node.Sublabel)
	}
	if node.Status != StatusOK {
		t.Errorf("статус пула = %q, ожидался %q", node.Status, StatusOK)
	}
}

// A route pointing at a pool that genuinely does not exist must still be
// visible as a gap in the map.
func TestUndefinedUpstreamIsStillMarked(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{{
			ID: "nginx:80", Service: model.ServiceNginx, Kind: "server",
			Address: "0.0.0.0", Port: 80, Protocol: "tcp", Mode: "http", Label: "site",
			Routes: []model.Route{{Match: "/", Target: "ghost_pool", TargetKind: "upstream"}},
		}},
	}

	node := nodeByID(Build(snap), "up:nginx:ghost_pool")
	if node == nil {
		t.Fatal("узел для несуществующего пула не построен")
	}
	if node.Status != StatusError || !strings.Contains(node.Sublabel, "не определён") {
		t.Errorf("несуществующий пул должен быть помечен ошибкой, получено status=%q sublabel=%q",
			node.Status, node.Sublabel)
	}
}

// A host->service "runs" edge used to sit alongside a service's own
// listener, drawing two parallel routes back to "внешняя сеть" for the
// exact same node (the direct internet->listener ingress edge, and
// host->service->listener) — every service already sits in the same
// column as the host, so the extra edge restated something already
// implied instead of adding information, and read as a redundant branch.
// The service node itself must still exist and stay reachable through its
// own listener; only the direct host edge is gone.
func TestServiceHasNoDirectRunsEdgeFromHost(t *testing.T) {
	snap := &model.Snapshot{
		Services: []model.ServiceUnit{{Name: "haproxy", Unit: "haproxy.service", Installed: true, ActiveState: "active"}},
		Endpoints: []model.Endpoint{{
			ID: "haproxy:fe", Service: model.ServiceHAProxy, Kind: "frontend",
			Address: "0.0.0.0", Port: 8443, Protocol: "tcp", Mode: "http", TLS: true, Label: "fe_ssl",
		}},
	}

	g := Build(snap)
	if nodeByID(g, "svc:haproxy") == nil {
		t.Fatal("узел сервиса haproxy не построен")
	}
	for _, e := range g.Edges {
		if e.Kind == "runs" {
			t.Errorf("неожиданное ребро \"runs\": %+v", e)
		}
	}
	found := false
	for _, e := range g.Edges {
		if e.From == "svc:haproxy" && e.To == "ep:haproxy:fe" && e.Kind == "listens" {
			found = true
		}
	}
	if !found {
		t.Error("сервис должен по-прежнему владеть своим слушателем через \"listens\"")
	}
}

// An undeclared listener — nothing in any parsed config accounts for it —
// must show up on the map itself, not only on the separate "Разное" page:
// omitting it left the map claiming to show "everything listening here"
// while quietly leaving out exactly the sockets nobody remembers
// configuring, which is the one case worth seeing on the map most.
func TestUndeclaredListenerAppearsOnTheMap(t *testing.T) {
	snap := &model.Snapshot{
		Listeners: []model.Listener{{
			Protocol: "tcp", Address: "127.0.0.1", Port: 11211,
			Process: "memcached", PID: 655,
			Command: "/usr/bin/memcached -m 64 -p 11211", User: "memcache",
			UptimeS: 3700, Origin: model.OriginService, Unit: "memcached.service",
		}},
	}

	g := Build(snap)
	node := nodeByID(g, "misc:tcp:127.0.0.1:11211")
	if node == nil {
		t.Fatal("узел для необъявленного слушателя не построен")
	}
	if node.Kind != KindUndeclared {
		t.Errorf("Kind = %q, ожидался %q", node.Kind, KindUndeclared)
	}
	if node.Label != "memcached" {
		t.Errorf("Label = %q, ожидалось имя процесса", node.Label)
	}
	if node.Meta["command"] == "" || node.Meta["user"] != "memcache" || node.Meta["unit"] != "memcached.service" {
		t.Errorf("Meta = %+v, ожидались command/user/unit", node.Meta)
	}
	if node.Meta["started"] != "1 ч" {
		t.Errorf("Meta[started] = %q, ожидалось %q", node.Meta["started"], "1 ч")
	}
	// Loopback-only and not public — no reason to draw an internet edge or
	// alarm colour for something nothing outside the host can reach.
	if node.Status != StatusUnknown {
		t.Errorf("статус = %q, ожидался %q (не публичный)", node.Status, StatusUnknown)
	}
	for _, e := range g.Edges {
		if e.From == "internet" && e.To == node.ID {
			t.Errorf("неожиданное ingress-ребро для непубличного слушателя: %+v", e)
		}
	}
}

// A listener bound to all interfaces is the one case worth an actual alarm
// colour and a direct edge from "внешняя сеть" — it is reachable from
// outside and nobody declared it, which is exactly what
// ruleListeningNotDeclared escalates to Medium once Public() is true.
func TestPublicUndeclaredListenerGetsIngressEdgeAndWarning(t *testing.T) {
	snap := &model.Snapshot{
		Listeners: []model.Listener{{
			Protocol: "tcp", Address: "0.0.0.0", Port: 5380,
			Process: "dotnet", PID: 419, User: "root", Origin: model.OriginManual,
		}},
	}

	g := Build(snap)
	node := nodeByID(g, "misc:tcp:0.0.0.0:5380")
	if node == nil {
		t.Fatal("узел не построен")
	}
	if node.Status != StatusWarn {
		t.Errorf("статус = %q, ожидался %q (публичный)", node.Status, StatusWarn)
	}
	if !node.Public {
		t.Error("Public = false, ожидался true для 0.0.0.0")
	}
	found := false
	for _, e := range g.Edges {
		if e.From == "internet" && e.To == node.ID && e.Kind == "ingress" {
			found = true
		}
	}
	if !found {
		t.Error("нет ребра internet -> узел для публичного необъявленного слушателя")
	}
}

// A port a config file actually declares must not also show up as
// "undeclared" — the two views would otherwise contradict each other for
// the exact same socket.
func TestDeclaredPortIsNotAlsoShownAsUndeclared(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{{
			ID: "nginx:1", Service: model.ServiceNginx, Kind: "server",
			Address: "0.0.0.0", Port: 80, Protocol: "tcp", Mode: "http", Label: "site",
		}},
		Listeners: []model.Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 80, Process: "nginx", PID: 1}},
	}

	g := Build(snap)
	for _, n := range g.Nodes {
		if n.Kind == KindUndeclared {
			t.Errorf("порт, описанный в конфиге, не должен также попадать в необъявленные: %+v", n)
		}
	}
}

// docker-proxy is already covered by the container publish edges built from
// the endpoint's own routes — showing it a second time as "undeclared"
// would be a redundant, unexplained duplicate of the same port.
func TestDockerProxyIsExcludedFromUndeclared(t *testing.T) {
	snap := &model.Snapshot{
		Listeners: []model.Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: 8080, Process: "docker-proxy", PID: 1201}},
	}

	g := Build(snap)
	for _, n := range g.Nodes {
		if n.Kind == KindUndeclared {
			t.Errorf("docker-proxy не должен становиться узлом Разного: %+v", n)
		}
	}
}

// A backend that answers by itself carries no servers; that is not a fault.
func TestServerlessBackendIsNotAnError(t *testing.T) {
	snap := &model.Snapshot{
		Upstreams: []model.Upstream{{
			ID: "haproxy:upstream:be_health", Name: "be_health",
			Service: model.ServiceHAProxy, Algorithm: "roundrobin",
		}},
	}
	node := nodeByID(Build(snap), "up:haproxy:be_health")
	if node == nil {
		t.Fatal("узел пула be_health не построен")
	}
	if node.Status != StatusOK {
		t.Errorf("статус = %q, ожидался %q", node.Status, StatusOK)
	}
	if !strings.Contains(node.Sublabel, "без backend-серверов") {
		t.Errorf("подпись = %q, ожидалось пояснение об отсутствии серверов", node.Sublabel)
	}
}
