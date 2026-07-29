package inventory

import (
	"context"
	"path/filepath"
	"strings"
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
		// Reported through the ufw view: that is where an operator deletes it,
		// and the underlying iptables rule describes the same thing.
		{"stale-firewall-rule", model.SeverityLow, "ufw 25/tcp"},
		{"port-conflict", model.SeverityHigh, ""},

		// Certificates planted in the snapshot.
		{"tls-cert-expired", model.SeverityCritical, "/etc/letsencrypt/live/api.example.com/fullchain.pem"},
		{"tls-cert-orphan-lineage", model.SeverityHigh, "/etc/letsencrypt/live/api.example.com/fullchain.pem"},
		{"tls-cert-renewal-not-automatic", model.SeverityMedium, "/etc/letsencrypt/live/app.example.com/fullchain.pem"},
		{"tls-cert-self-signed", model.SeverityLow, "/etc/ssl/certs/internal.pem"},
		{"tls-cert-weak-key", model.SeverityMedium, "/etc/ssl/certs/internal.pem"},
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

	// One unused port produces one finding, not one per firewall backend.
	if got := len(byRule["stale-firewall-rule"]); got != 1 {
		t.Errorf("неиспользуемое правило должно сообщаться один раз, получено %d: %s",
			got, objects(byRule["stale-firewall-rule"]))
	}

	// The healthy certificate must not be reported as expiring or broken.
	for _, f := range append(byRule["tls-cert-expired"], byRule["tls-cert-expiring"]...) {
		if strings.Contains(f.Object, "app.example.com") {
			t.Errorf("здоровый сертификат app.example.com помечен как проблемный: %s", f.Title)
		}
	}

	// And nothing should be reported against the correctly configured pieces.
	for _, f := range snap.Findings {
		if f.Rule == "upstream-undefined" {
			t.Errorf("ложное срабатывание upstream-undefined: %+v", f)
		}
	}
}

// The snapshot certificates must be parsed, not just counted.
func TestScanReadsCertificates(t *testing.T) {
	snap, err := fixtureScanner(t).Scan(context.Background())
	if err != nil {
		t.Fatalf("скан завершился ошибкой: %v", err)
	}
	if len(snap.Certs) != 3 {
		t.Fatalf("сертификатов: %d, ожидалось 3", len(snap.Certs))
	}

	byPath := map[string]model.Certificate{}
	for _, c := range snap.Certs {
		byPath[c.Path] = c
	}

	app := byPath["/etc/letsencrypt/live/app.example.com/fullchain.pem"]
	if app.Error != "" {
		t.Fatalf("app.example.com: %s", app.Error)
	}
	if app.DaysLeft <= 0 {
		t.Errorf("app.example.com должен быть действующим, осталось дней: %d", app.DaysLeft)
	}
	if app.ChainLength != 2 {
		t.Errorf("app.example.com: цепочка из %d сертификатов, ожидалось 2", app.ChainLength)
	}
	if !app.CoversName("www.app.example.com") {
		t.Errorf("app.example.com: SAN не разобраны, имена = %v", app.Names)
	}
	if app.SelfSigned {
		t.Error("app.example.com выпущен тестовым CA и не должен считаться самоподписанным")
	}
	if !app.Renewal.Managed || app.Renewal.Automatic {
		t.Errorf("app.example.com: ожидалось управление certbot без активного таймера, получено %+v",
			app.Renewal)
	}
	if len(app.Fingerprint) != 64 {
		t.Errorf("app.example.com: отпечаток %q не похож на SHA-256 в hex", app.Fingerprint)
	}
	// Fixtures mode has no real sockets: the live check must be skipped
	// entirely rather than fabricate a match.
	if app.Serving.Checked {
		t.Error("в режиме fixtures проверка живого сертификата не должна запускаться")
	}

	api := byPath["/etc/letsencrypt/live/api.example.com/fullchain.pem"]
	if api.DaysLeft >= 0 {
		t.Errorf("api.example.com должен быть просрочен, осталось дней: %d", api.DaysLeft)
	}
	if api.Renewal.Managed {
		t.Error("для api.example.com нет файла обновления — он не должен считаться управляемым")
	}

	internal := byPath["/etc/ssl/certs/internal.pem"]
	if !internal.SelfSigned {
		t.Error("internal.pem должен быть распознан как самоподписанный")
	}
	if internal.KeyBits != 1024 {
		t.Errorf("internal.pem: ключ %d бит, ожидалось 1024", internal.KeyBits)
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
