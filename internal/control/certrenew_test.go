package control

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/store"
)

// copyFixturesRoot copies the repo's fixtures/host tree into a throwaway
// directory. RenewCertbot's recombine step writes through the Collector —
// pointing a test straight at the repo's own fixtures/host would let it
// mutate tracked files the rest of the suite (and every other contributor's
// working tree) relies on.
func copyFixturesRoot(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("fixtures root: %v", err)
	}
	dst := t.TempDir()
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("копирование фикстур: %v", err)
	}
	return dst
}

// renewSetup wires a CertManager against an isolated copy of the repo's
// fixtures tree, which carries a canned "certbot renew --cert-name
// app.example.com" response — unlike certgenSetup's empty throwaway
// directory, this lets a test exercise the actual command path instead of
// only validation. The scanner is pre-scanned so RenewCertbot's recombine
// step can find the fixture's known haproxy-derived certificate.
func renewSetup(t *testing.T) (*CertManager, *store.DB) {
	t.Helper()
	root := copyFixturesRoot(t)
	cfg := &config.Config{
		Mode:            config.ModeFixtures,
		FixturesRoot:    root,
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		ComposeFiles:    []string{"/srv/docker/docker-compose.yml"},
		CommandTimeout:  5 * time.Second,
	}
	c := collect.NewFixtures(root)
	dbPath := filepath.Join(t.TempDir(), "nkt.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("скан: %v", err)
	}
	services := NewServiceManager(cfg, c, db)
	return NewCertManager(cfg, c, db, services, scanner), db
}

func TestRenewCertbotRejectsBadLineage(t *testing.T) {
	m, _ := renewSetup(t)
	for _, lineage := range []string{"", "../../etc/passwd", "site;rm -rf /", "site with spaces"} {
		if _, err := m.RenewCertbot(context.Background(), "test", lineage); err == nil {
			t.Errorf("lineage %q: ожидалась ошибка валидации", lineage)
		}
	}
}

func TestRenewCertbotSuccess(t *testing.T) {
	m, db := renewSetup(t)
	res, err := m.RenewCertbot(context.Background(), "test", "app.example.com")
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !res.OK() {
		t.Fatalf("ожидался успешный exit code, получено %d: %s", res.ExitCode, res.Output())
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "cert.renew", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("записей в журнале: %d, ожидалась 1", len(entries))
	}
	if entries[0].Result != "ok" || entries[0].Target != "app.example.com" || entries[0].Username != "test" {
		t.Errorf("неожиданная запись журнала: %+v", entries[0])
	}
}

func TestRenewCertbotFailureIsAudited(t *testing.T) {
	m, db := renewSetup(t)
	// No canned command for this lineage: Fixtures.Run returns exit 127.
	_, err := m.RenewCertbot(context.Background(), "test", "unknown-lineage.example.com")
	if err == nil {
		t.Fatal("ожидалась ошибка для lineage без заготовленного вывода")
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "cert.renew", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "error" {
		t.Fatalf("ожидалась одна запись с результатом error, получено: %+v", entries)
	}
}

// TestRenewCertbotRecombinesDerivedHAProxyCert covers the point of
// recombineDerivedCerts: the fixture's /etc/haproxy/certs-le/app.example.com.pem
// was hand-built with a different throwaway key than the one now sitting in
// /etc/letsencrypt/live/app.example.com/privkey.pem, so a successful renewal
// must overwrite it with the certificate+key pair actually on disk today,
// then reload haproxy (the fixture's "webroot" authenticator never stops
// it) to pick the new file up.
func TestRenewCertbotRecombinesDerivedHAProxyCert(t *testing.T) {
	m, db := renewSetup(t)

	const derivedPath = "/etc/haproxy/certs-le/app.example.com.pem"
	wantKey, err := m.c.ReadFile("/etc/letsencrypt/live/app.example.com/privkey.pem")
	if err != nil {
		t.Fatalf("чтение исходного ключа: %v", err)
	}
	wantCert, err := m.c.ReadFile("/etc/letsencrypt/live/app.example.com/fullchain.pem")
	if err != nil {
		t.Fatalf("чтение исходного сертификата: %v", err)
	}

	if _, err := m.RenewCertbot(context.Background(), "test", "app.example.com"); err != nil {
		t.Fatalf("renew: %v", err)
	}

	got, err := m.c.ReadFile(derivedPath)
	if err != nil {
		t.Fatalf("чтение пересобранного файла: %v", err)
	}
	want := append(append([]byte{}, wantCert...), wantKey...)
	if string(got) != string(want) {
		t.Errorf("пересобранный файл не совпадает с cert+key из /etc/letsencrypt/live/app.example.com")
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	var sawRecombine, sawReload bool
	for _, e := range entries {
		if e.Action == "cert.recombine" && e.Target == derivedPath && e.Result == "ok" {
			sawRecombine = true
		}
		if e.Action == "service.reload" && e.Target == "haproxy" && e.Result == "ok" {
			sawReload = true
		}
	}
	if !sawRecombine {
		t.Error("в журнале нет записи cert.recombine для " + derivedPath)
	}
	if !sawReload {
		t.Error("в журнале нет записи service.reload для haproxy")
	}
}

// TestRenewCertbotStopsAndRestartsForStandalone covers the lineage in
// fixtures/host/etc/letsencrypt/renewal/standalone.example.com.conf, which
// declares authenticator = standalone: certbot needs :80/:443 free for
// itself, so nginx and haproxy must be stopped before the renewal and
// restarted afterward regardless of outcome.
func TestRenewCertbotStopsAndRestartsForStandalone(t *testing.T) {
	m, db := renewSetup(t)

	res, err := m.RenewCertbot(context.Background(), "test", "standalone.example.com")
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !res.OK() {
		t.Fatalf("ожидался успешный exit code, получено %d: %s", res.ExitCode, res.Output())
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	// ListAudit returns newest first; reverse into chronological order so
	// the sequence stop → renew → start can be checked directly.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	type step struct{ action, target string }
	var got []step
	for _, e := range entries {
		switch e.Action {
		case "service.stop", "service.start", "cert.renew":
			if e.Result != "ok" {
				t.Errorf("%s %s: результат %q, ожидался ok", e.Action, e.Target, e.Result)
			}
			got = append(got, step{e.Action, e.Target})
		}
	}
	want := []step{
		{"service.stop", "nginx"}, {"service.stop", "haproxy"},
		{"cert.renew", "standalone.example.com"},
		{"service.start", "nginx"}, {"service.start", "haproxy"},
	}
	if len(got) != len(want) {
		t.Fatalf("последовательность действий: %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("шаг %d: %+v, ожидалось %+v (вся последовательность: %v)", i, got[i], want[i], got)
		}
	}
}
