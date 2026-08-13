package analyze

import (
	"testing"

	"github.com/althq/netknownsthat/internal/model"
)

func rules(findings []model.Finding) map[string][]model.Finding {
	out := map[string][]model.Finding{}
	for _, f := range findings {
		out[f.Rule] = append(out[f.Rule], f)
	}
	return out
}

func dockerEndpoint(container, addr string, hostPort, containerPort int) model.Endpoint {
	return model.Endpoint{
		ID:       "docker:" + container + ":" + addr,
		Service:  model.ServiceDocker,
		Kind:     "published-port",
		Address:  addr,
		Port:     hostPort,
		Protocol: "tcp",
		Mode:     "tcp",
		Label:    container,
		Extra: map[string]string{
			"container":      container,
			"container_port": itoa(containerPort),
		},
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// A container publishing straight through to the service configured inside it
// is one socket seen at two layers, not two services fighting over a port.
func TestPortConflictIgnoresPublishThrough(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{
			dockerEndpoint("balancer", "127.0.0.1", 8404, 8404),
			{
				ID: "haproxy:stats", Service: model.ServiceHAProxy, Kind: "listen",
				Address: "0.0.0.0", Port: 8404, Protocol: "tcp", Mode: "http", Label: "stats",
			},
		},
	}
	if got := rules(Run(snap))["port-conflict"]; len(got) != 0 {
		t.Errorf("ложный конфликт порта: %+v", got)
	}
}

// The same container reported twice, once per address family, is one rule.
func TestPortConflictIgnoresSameContainer(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{
			dockerEndpoint("cache", "0.0.0.0", 6380, 6379),
			dockerEndpoint("cache", "::", 6380, 6379),
		},
	}
	if got := rules(Run(snap))["port-conflict"]; len(got) != 0 {
		t.Errorf("ложный конфликт между двумя семействами адресов одного контейнера: %+v", got)
	}
}

// A genuine clash between different services must still be reported.
func TestPortConflictStillCatchesRealClash(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{
			// Публикует 8443 наружу, но внутрь контейнера ведёт на 9000.
			dockerEndpoint("minio", "0.0.0.0", 8443, 9000),
			{
				ID: "nginx:grafana", Service: model.ServiceNginx, Kind: "server",
				Address: "0.0.0.0", Port: 8443, Protocol: "tcp", Mode: "http",
				TLS: true, Label: "grafana.internal",
			},
		},
	}
	if got := rules(Run(snap))["port-conflict"]; len(got) != 1 {
		t.Errorf("настоящий конфликт 8443 должен быть найден, получено %d", len(got))
	}
}

// When the socket table corroborates nothing, the two sides are not comparable
// (the reader lives in another network namespace) and the rule must stay quiet.
func TestDeclaredNotListeningSilentWhenNamespacesDiffer(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{
			dockerEndpoint("edge", "127.0.0.1", 8081, 80),
			dockerEndpoint("balancer", "127.0.0.1", 8082, 8090),
			dockerEndpoint("cache", "0.0.0.0", 6380, 6379),
			{
				ID: "nginx:80", Service: model.ServiceNginx, Kind: "server",
				Address: "0.0.0.0", Port: 80, Protocol: "tcp", Mode: "http", Label: "stand.local",
			},
		},
		// Только внутренний DNS докера — ни один объявленный порт не подтверждён.
		Listeners: []model.Listener{
			{Protocol: "tcp", Address: "127.0.0.11", Port: 36035},
		},
	}
	got := rules(Run(snap))
	if len(got["declared-not-listening"]) != 0 {
		t.Errorf("правило не должно срабатывать в чужом namespace: %+v", got["declared-not-listening"])
	}
	if len(got["listening-not-declared"]) == 0 {
		t.Error("обратное правило должно продолжать работать: неучтённый сокет виден")
	}
}

// Published container ports are never judged by the socket table: with
// userland-proxy disabled docker forwards by DNAT and nothing listens on the
// host, so a listener check would fail on a perfectly healthy machine.
func TestDeclaredNotListeningSkipsPublishedPorts(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{
			dockerEndpoint("cache", "0.0.0.0", 6380, 6379),
			{
				ID: "nginx:80", Service: model.ServiceNginx, Kind: "server",
				Address: "0.0.0.0", Port: 80, Protocol: "tcp", Mode: "http", Label: "site",
			},
		},
		// nginx подтверждён, докерский порт — нет; таблица сопоставима.
		Listeners: []model.Listener{
			{Protocol: "tcp", Address: "0.0.0.0", Port: 80, Process: "nginx"},
		},
		Services: []model.ServiceUnit{{Name: model.ServiceNginx, ActiveState: "active"}},
	}
	if got := rules(Run(snap))["declared-not-listening"]; len(got) != 0 {
		t.Errorf("опубликованный порт контейнера не должен проверяться через ss: %+v", got)
	}
}

// With a socket table that does corroborate the configuration, the rule works.
func TestDeclaredNotListeningFiresWhenComparable(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{
			{
				ID: "nginx:80", Service: model.ServiceNginx, Kind: "server",
				Address: "0.0.0.0", Port: 80, Protocol: "tcp", Mode: "http", Label: "site",
			},
			{
				ID: "nginx:443", Service: model.ServiceNginx, Kind: "server",
				Address: "0.0.0.0", Port: 443, Protocol: "tcp", Mode: "http", TLS: true, Label: "site",
			},
			{
				ID: "haproxy:9000", Service: model.ServiceHAProxy, Kind: "frontend",
				Address: "0.0.0.0", Port: 9000, Protocol: "tcp", Mode: "tcp", Label: "fe",
			},
		},
		Listeners: []model.Listener{
			{Protocol: "tcp", Address: "0.0.0.0", Port: 80, Process: "nginx"},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 443, Process: "nginx"},
		},
		Services: []model.ServiceUnit{
			{Name: model.ServiceNginx, ActiveState: "active"},
			{Name: model.ServiceHAProxy, ActiveState: "active"},
		},
	}
	got := rules(Run(snap))["declared-not-listening"]
	if len(got) != 1 || got[0].Object != "0.0.0.0:9000" {
		t.Errorf("ожидалась ровно одна находка по порту 9000, получено %+v", got)
	}
}

// TestRunNeverReturnsNilOnCleanHost guards against a real production crash:
// a clean host with zero findings must still get back a real empty slice,
// not nil — encoding/json marshals a nil slice as `null` (Snapshot.Findings
// has no `omitempty`), and every frontend page that calls .filter/.map on
// the findings list crashes on `null`.
func TestRunNeverReturnsNilOnCleanHost(t *testing.T) {
	got := Run(&model.Snapshot{})
	if got == nil {
		t.Fatal("Run(пустой снапшот) вернул nil, ожидался непустой (даже если пустой) срез")
	}
	if len(got) != 0 {
		t.Errorf("ожидалось 0 находок на пустом снапшоте, получено %d", len(got))
	}
}

// TestUndeclaredListeners covers the plain-list entry point /api/misc uses
// (analyze.UndeclaredListeners): it must agree exactly with what
// ruleListeningNotDeclared turns into findings — same set, same exclusions
// (a port with a matching endpoint, and docker-proxy, both stay out) — just
// without the Finding wrapper.
func TestUndeclaredListeners(t *testing.T) {
	snap := &model.Snapshot{
		Endpoints: []model.Endpoint{
			{
				ID: "nginx:80", Service: model.ServiceNginx, Kind: "server",
				Address: "0.0.0.0", Port: 80, Protocol: "tcp", Mode: "http", Label: "site",
			},
		},
		Listeners: []model.Listener{
			{Protocol: "tcp", Address: "0.0.0.0", Port: 80, Process: "nginx"},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 9000, Process: "some-daemon"},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 5432, Process: "docker-proxy"},
		},
	}

	got := UndeclaredListeners(snap)
	if len(got) != 1 || got[0].Port != 9000 || got[0].Process != "some-daemon" {
		t.Fatalf("UndeclaredListeners = %+v, want exactly the port-9000 listener", got)
	}

	// Must match ruleListeningNotDeclared's own set exactly (same source of
	// truth) — this is the guarantee that makes /api/misc and the
	// "listening-not-declared" findings never disagree.
	findings := rules(Run(snap))["listening-not-declared"]
	if len(findings) != len(got) {
		t.Errorf("ruleListeningNotDeclared produced %d findings, UndeclaredListeners %d entries — should match",
			len(findings), len(got))
	}
}
