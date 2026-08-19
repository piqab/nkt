package control

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
		CertbotTimeout:  20 * time.Second,
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
	// Every renewal always goes through --standalone now, regardless of what
	// authenticator app.example.com's own renewal.conf records.
	if !containsArg(res.Argv, "--standalone") {
		t.Errorf("--standalone должен передаваться для каждого продления: %v", res.Argv)
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

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
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
// must overwrite it with the certificate+key pair actually on disk today.
// Every renewal now stops and restarts haproxy unconditionally (see
// renewCertbot), so the restart itself — not a separate reload — is what
// picks the recombined file up.
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
	var sawRecombine, sawRestart bool
	for _, e := range entries {
		if e.Action == "cert.recombine" && e.Target == derivedPath && e.Result == "ok" {
			sawRecombine = true
		}
		if e.Action == "service.start" && e.Target == "haproxy" && e.Result == "ok" {
			sawRestart = true
		}
	}
	if !sawRecombine {
		t.Error("в журнале нет записи cert.recombine для " + derivedPath)
	}
	if !sawRestart {
		t.Error("в журнале нет записи service.start для haproxy")
	}
}

// TestRenewCertbotStopsAndRestartsForStandalone covers the lineage in
// fixtures/host/etc/letsencrypt/renewal/standalone.example.com.conf, which
// declares authenticator = standalone: certbot needs :80/:443 free for
// itself, so nginx, haproxy and caddy must all be stopped before the
// renewal and restarted afterward regardless of outcome.
func TestRenewCertbotStopsAndRestartsForStandalone(t *testing.T) {
	m, db := renewSetup(t)

	res, err := m.RenewCertbot(context.Background(), "test", "standalone.example.com")
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !res.OK() {
		t.Fatalf("ожидался успешный exit code, получено %d: %s", res.ExitCode, res.Output())
	}
	// Relying solely on renewal.conf's stored authenticator is not enough on
	// every certbot version to actually invoke the standalone plugin during
	// renew — the flag must be re-asserted on the command line explicitly.
	if !containsArg(res.Argv, "--standalone") {
		t.Errorf("--standalone не передан явно в команде certbot: %v", res.Argv)
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
		{"service.stop", "nginx"}, {"service.stop", "haproxy"}, {"service.stop", "caddy"},
		{"cert.renew", "standalone.example.com"},
		{"service.start", "nginx"}, {"service.start", "haproxy"}, {"service.start", "caddy"},
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

// TestStopForStandaloneSkipsUninstalledServices is the regression test for
// a bug shipped in a709f33: adding caddy to standaloneServices made
// stopForStandalone unconditionally try to stop it, on every host —
// including ones that never installed caddy at all, where "systemctl stop
// caddy" fails outright ("Unit caddy.service not loaded") and used to abort
// the entire certificate renewal before certbot ever ran. A host running
// nginx or haproxy alone (the overwhelmingly common case) must still be
// able to renew a standalone certificate.
func TestStopForStandaloneSkipsUninstalledServices(t *testing.T) {
	root := copyFixturesRoot(t)
	removeFixtureCommand(t, root, []string{"sh", "-c", "command -v caddy"})

	cfg := &config.Config{
		Mode:            config.ModeFixtures,
		FixturesRoot:    root,
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		ComposeFiles:    []string{"/srv/docker/docker-compose.yml"},
		CommandTimeout:  5 * time.Second,
		CertbotTimeout:  20 * time.Second,
	}
	c := collect.NewFixtures(root)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("скан: %v", err)
	}
	if snap := scanner.Latest(); snap != nil {
		for _, svc := range snap.Services {
			if svc.Name == "caddy" && svc.Installed {
				t.Fatal("тестовая подготовка не удалась: caddy всё ещё выглядит установленным")
			}
		}
	}

	m := NewCertManager(cfg, c, db, NewServiceManager(cfg, c, db), scanner)
	stopped, err := m.stopForStandalone(context.Background(), "test")
	if err != nil {
		t.Fatalf("stopForStandalone: %v", err)
	}
	for _, svc := range stopped {
		if svc == "caddy" {
			t.Errorf("stopped = %v, не должен включать caddy — он не установлен", stopped)
		}
	}
	if len(stopped) != 2 {
		t.Errorf("stopped = %v, want ровно nginx и haproxy", stopped)
	}
}

// removeFixtureCommand deletes one entry from a copied fixtures tree's
// .commands/index.json, matched by its exact "match" argv — used to make a
// binary look uninstalled (collect.Which falls through to fixtures' own
// "no canned output" 127 exit) without hand-rolling a second fixtures tree.
func removeFixtureCommand(t *testing.T, root string, match []string) {
	t.Helper()
	path := filepath.Join(root, ".commands", "index.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение index.json: %v", err)
	}
	var idx struct {
		Commands []map[string]any `json:"commands"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("разбор index.json: %v", err)
	}
	out := idx.Commands[:0]
	for _, cmd := range idx.Commands {
		argv, _ := cmd["match"].([]any)
		if argvEquals(argv, match) {
			continue
		}
		out = append(out, cmd)
	}
	idx.Commands = out
	newRaw, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("сериализация index.json: %v", err)
	}
	if err := os.WriteFile(path, newRaw, 0o644); err != nil {
		t.Fatalf("запись index.json: %v", err)
	}
}

func argvEquals(argv []any, match []string) bool {
	if len(argv) != len(match) {
		return false
	}
	for i, v := range argv {
		s, ok := v.(string)
		if !ok || s != match[i] {
			return false
		}
	}
	return true
}

// recordingCollector wraps a Collector and records every RunTimeout call's
// requested timeout, so a test can catch a regression back to Run() — which
// would silently reapply the collector's short, fast-command-tuned
// CommandTimeout to certbot and kill it mid-renewal, exactly the "код -1"
// bug this was built to fix.
type recordingCollector struct {
	collect.Collector
	timeouts []time.Duration
}

func (r *recordingCollector) RunTimeout(
	ctx context.Context, timeout time.Duration, name string, args ...string,
) (collect.CommandResult, error) {
	r.timeouts = append(r.timeouts, timeout)
	return r.Collector.RunTimeout(ctx, timeout, name, args...)
}

func TestRenewCertbotUsesCertbotTimeout(t *testing.T) {
	root := copyFixturesRoot(t)
	cfg := &config.Config{
		Mode:            config.ModeFixtures,
		FixturesRoot:    root,
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		ComposeFiles:    []string{"/srv/docker/docker-compose.yml"},
		CommandTimeout:  5 * time.Second,
		CertbotTimeout:  90 * time.Second,
	}
	rec := &recordingCollector{Collector: collect.NewFixtures(root)}
	dbPath := filepath.Join(t.TempDir(), "nkt.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, rec, db)
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("скан: %v", err)
	}
	services := NewServiceManager(cfg, rec, db)
	m := NewCertManager(cfg, rec, db, services, scanner)

	if _, err := m.RenewCertbot(context.Background(), "test", "app.example.com"); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if len(rec.timeouts) != 1 || rec.timeouts[0] != cfg.CertbotTimeout {
		t.Errorf("RunTimeout вызван с таймаутами %v, ожидался один вызов с CertbotTimeout=%v",
			rec.timeouts, cfg.CertbotTimeout)
	}
}

// waitForJob polls RenewJobStatus until done, for tests — the background
// goroutine finishes in milliseconds against fixtures, so a short deadline
// is enough without making the test flaky under load.
func waitForJob(t *testing.T, m *CertManager, id string) ([]RenewEvent, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		events, done, errMsg, ok := m.RenewJobStatus(id)
		if !ok {
			t.Fatalf("задача %s не найдена", id)
		}
		if done {
			return events, errMsg
		}
		if time.Now().After(deadline) {
			t.Fatalf("задача %s не завершилась за 5с, событий пока: %v", id, events)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func eventTexts(events []RenewEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Text
	}
	return out
}

// TestStartRenewCertbotReportsStandaloneStepsInOrder covers exactly what
// prompted StartRenewCertbot: the "продлить" button used to block on the
// whole operation with nothing but a spinner. This checks the progress feed
// an open modal would poll shows stop → certbot → restart in the right
// order, restart happening (and being reported) only after certbot is done.
func TestStartRenewCertbotReportsStandaloneStepsInOrder(t *testing.T) {
	m, _ := renewSetup(t)

	id, err := m.StartRenewCertbot("test", "standalone.example.com")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, errMsg := waitForJob(t, m, id)
	if errMsg != "" {
		t.Fatalf("задача завершилась с ошибкой: %s (события: %v)", errMsg, eventTexts(events))
	}

	texts := eventTexts(events)
	wantInOrder := []string{
		"Начинаю продление standalone.example.com",
		"Останавливаю nginx и haproxy для --standalone",
		"nginx: остановлен",
		"haproxy: остановлен",
		"Запускаю: certbot renew --cert-name standalone.example.com --non-interactive --standalone",
		"certbot: сертификат продлён",
		"nginx: запущен",
		"haproxy: запущен",
		"Готово",
	}
	pos := -1
	for _, want := range wantInOrder {
		next := indexOfSubstring(texts, want, pos+1)
		if next == -1 {
			t.Fatalf("не нашёл %q после позиции %d в событиях: %v", want, pos, texts)
		}
		pos = next
	}
}

// TestStartRenewCertbotReportsRecombinedHAProxyFile covers the user-facing
// point of showing this step at all: naming the exact haproxy file that got
// rewritten, not just "a copy was updated somewhere".
func TestStartRenewCertbotReportsRecombinedHAProxyFile(t *testing.T) {
	m, _ := renewSetup(t)

	id, err := m.StartRenewCertbot("test", "app.example.com")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	events, errMsg := waitForJob(t, m, id)
	if errMsg != "" {
		t.Fatalf("задача завершилась с ошибкой: %s (события: %v)", errMsg, eventTexts(events))
	}

	const want = "Пересобран файл для haproxy: /etc/haproxy/certs-le/app.example.com.pem"
	if indexOfSubstring(eventTexts(events), want, 0) == -1 {
		t.Errorf("не нашёл %q в событиях: %v", want, eventTexts(events))
	}
}

func indexOfSubstring(haystack []string, substr string, from int) int {
	for i := from; i < len(haystack); i++ {
		if strings.Contains(haystack[i], substr) {
			return i
		}
	}
	return -1
}
