package topology

import (
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/model"
)

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
