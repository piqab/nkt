package inventory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/model"
)

func fixtureScanner(t *testing.T) *Scanner {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("fixtures root: %v", err)
	}
	cfg := &config.Config{
		Mode:            config.ModeFixtures,
		FixturesRoot:    root,
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		ComposeFiles:    []string{"/srv/docker/docker-compose.yml"},
		CommandTimeout:  5 * time.Second,
	}
	// A nil store keeps the scan read-only, which is what the assertions need.
	return New(cfg, collect.NewFixtures(root), nil)
}

func TestScanBuildsCompleteSnapshot(t *testing.T) {
	snap, err := fixtureScanner(t).Scan(context.Background())
	if err != nil {
		t.Fatalf("скан завершился ошибкой: %v", err)
	}

	if snap.Host.Hostname != "edge-01" {
		t.Errorf("hostname = %q", snap.Host.Hostname)
	}
	if len(snap.Endpoints) < 8 {
		t.Errorf("endpoints: %d, ожидалось не меньше 8", len(snap.Endpoints))
	}
	if len(snap.Upstreams) < 6 {
		t.Errorf("upstreams: %d, ожидалось не меньше 6", len(snap.Upstreams))
	}
	if len(snap.Container) != 7 {
		t.Errorf("containers: %d, ожидалось 7", len(snap.Container))
	}
	if len(snap.Listeners) < 15 {
		t.Errorf("listeners: %d", len(snap.Listeners))
	}
	if snap.Digest == "" {
		t.Error("digest не посчитан")
	}

	// Every source must report success against the fixture host.
	for _, src := range snap.Sources {
		if src.Error != "" {
			t.Errorf("источник %s: %s", src.Name, src.Error)
		}
	}

	services := map[string]model.ServiceUnit{}
	for _, s := range snap.Services {
		services[s.Name] = s
	}
	if services["nginx"].ActiveState != "active" {
		t.Errorf("nginx: state=%q", services["nginx"].ActiveState)
	}
	if services["fail2ban"].ActiveState != "inactive" {
		t.Errorf("fail2ban: state=%q", services["fail2ban"].ActiveState)
	}
	if services["haproxy"].MainPID != 933 {
		t.Errorf("haproxy: pid=%d", services["haproxy"].MainPID)
	}
}

func TestScanFindsPlantedProblems(t *testing.T) {
	snap, err := fixtureScanner(t).Scan(context.Background())
	if err != nil {
		t.Fatalf("скан завершился ошибкой: %v", err)
	}

	byRule := map[string][]model.Finding{}
	for _, f := range snap.Findings {
		byRule[f.Rule] = append(byRule[f.Rule], f)
	}

	// Each of these problems is deliberately planted in the fixture host.
	want := []struct {
		rule     string
		severity string
		object   string
	}{
		{"docker-bypasses-firewall", model.SeverityCritical, "acme-redis:6379"},
		{"sensitive-port-public", model.SeverityCritical, "0.0.0.0:6379"},
		{"container-restarting", model.SeverityHigh, "acme-minio"},
		{"admin-interface-open", model.SeverityHigh, "0.0.0.0:9001"},
		{"weak-tls", model.SeverityMedium, ""},
		{"public-plaintext-proxy", model.SeverityMedium, "0.0.0.0:80"},
		{"stale-firewall-rule", model.SeverityLow, "iptables 25/tcp"},
		{"port-conflict", model.SeverityHigh, ""},
	}
	for _, w := range want {
		list := byRule[w.rule]
		if len(list) == 0 {
			t.Errorf("правило %s ничего не нашло", w.rule)
			continue
		}
		found := false
		for _, f := range list {
			if w.object == "" || f.Object == w.object {
				found = true
				if f.Severity != w.severity {
					t.Errorf("%s (%s): severity=%s, ожидалось %s", w.rule, f.Object, f.Severity, w.severity)
				}
			}
		}
		if !found {
			t.Errorf("правило %s не сработало для объекта %q; сработало для: %s",
				w.rule, w.object, objects(list))
		}
	}

	// The 8443 clash between nginx and the minio container must be visible.
	conflict := false
	for _, f := range byRule["port-conflict"] {
		if f.Object == "0.0.0.0:8443" {
			conflict = true
		}
	}
	if !conflict {
		t.Errorf("конфликт порта 8443 (nginx vs acme-minio) не обнаружен: %s", objects(byRule["port-conflict"]))
	}

	// And nothing should be reported against the correctly configured pieces.
	for _, f := range snap.Findings {
		if f.Rule == "upstream-undefined" {
			t.Errorf("ложное срабатывание upstream-undefined: %+v", f)
		}
	}
}

func TestDeriveTargetsCoversEndpointsAndBackends(t *testing.T) {
	snap, err := fixtureScanner(t).Scan(context.Background())
	if err != nil {
		t.Fatalf("скан завершился ошибкой: %v", err)
	}
	targets := DeriveTargets(snap)
	if len(targets) < 12 {
		t.Fatalf("целей мониторинга: %d, ожидалось не меньше 12", len(targets))
	}

	byKey := map[string]bool{}
	kinds := map[string]int{}
	for _, tg := range targets {
		byKey[tg.Key] = true
		kinds[tg.Kind]++
		if tg.Host == "0.0.0.0" {
			t.Errorf("цель %s не должна опрашивать 0.0.0.0", tg.Key)
		}
	}
	if kinds["https"] == 0 || kinds["http"] == 0 || kinds["tcp"] == 0 {
		t.Errorf("ожидались цели всех трёх типов, получено %v", kinds)
	}
	if !byKey["up:haproxy:be_postgres:10.10.0.11:5432"] {
		t.Error("backend be_postgres/pg1 не стал целью мониторинга")
	}
}

func objects(list []model.Finding) string {
	out := ""
	for _, f := range list {
		out += f.Object + " "
	}
	if out == "" {
		return "(пусто)"
	}
	return out
}
