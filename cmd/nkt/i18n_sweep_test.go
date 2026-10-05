package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/hub"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/monitor"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// cyrillic — русская буква в ответе на английском.
var cyrillic = regexp.MustCompile(`[А-Яа-яЁё]`)

// i18nSweepSkip — маршруты, где русский — это данные хоста, а не текст
// nkt: содержимое файлов и журналов фикстур, а также потоки и скачивания.
var i18nSweepSkip = []string{
	"/configs/file", "/configs/versions", "/logs/", "/files/", "/terminal/",
	"/ws", "/download", "/backups", "/k8s/pf/", "/updates", "/btop",
	// Инструкции ИИ хранятся на обоих языках и правятся оператором.
	"/hub/ai/prompts",
}

// TestAPIEnglishHasNoRussian поднимает API хоста на фикстурах ровно так,
// как runServer, и обходит все GET-маршруты без параметров с языком en:
// кириллица в ответе — это строка, которую забыли провести через каталог
// msgs. Новые маршруты попадают в проверку сами.
func TestAPIEnglishHasNoRussian(t *testing.T) {
	if testing.Short() {
		t.Skip("поднимает полный сервер на фикстурах")
	}
	root, err := filepath.Abs("../../fixtures/host")
	if err != nil {
		t.Fatal(err)
	}
	const password = "sweep-password-0123456789"
	t.Setenv("NKT_MODE", "fixtures")
	t.Setenv("NKT_FIXTURES_ROOT", root)
	t.Setenv("NKT_DATA_DIR", t.TempDir())
	t.Setenv("NKT_BOOTSTRAP_ADMIN_USER", "admin")
	t.Setenv("NKT_BOOTSTRAP_ADMIN_PASSWORD", password)
	t.Setenv("NKT_COOKIE_SECURE", "false")
	t.Setenv("NKT_SCHEDULER_ENABLED", "false")

	r, err := newRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.NewService(r.db, r.cfg)
	if _, err := authSvc.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	scheduler := monitor.NewScheduler(r.cfg, r.db, r.scanner, r.certs, log)
	if _, err := r.scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := r.apiServer(authSvc, scheduler, nil, log).Handler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	res, err := client.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", err, res)
	}
	_ = res.Body.Close()

	sweepEnglish(t, handler, client, ts.URL, 50)
}

// TestHubEnglishHasNoRussian — то же для маршрутов самого хаба (/hub/*).
// Сканер машины хаба не подключается: «localhost» в тесте не нужен, а
// сканировать настоящую машину в тесте нельзя. В базу кладутся хост с
// ошибкой установки и событие журнала — их тексты хранятся ключами и
// должны выйти по-английски.
func TestHubEnglishHasNoRussian(t *testing.T) {
	if testing.Short() {
		t.Skip("поднимает хаб")
	}
	const password = "sweep-password-0123456789"
	dir := t.TempDir()
	t.Setenv("NKT_MODE", "hub")
	t.Setenv("NKT_DATA_DIR", dir)
	t.Setenv("NKT_BOOTSTRAP_ADMIN_USER", "admin")
	t.Setenv("NKT_BOOTSTRAP_ADMIN_PASSWORD", password)
	t.Setenv("NKT_COOKIE_SECURE", "false")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	key, err := secretbox.ResolveKey(cfg.HubMasterKey, cfg.HubKeyFile())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.NewService(db, cfg)
	if _, err := authSvc.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := db.CreateHost(ctx, "web-1", "192.0.2.10", 22, "root", "password", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetHostStatus(ctx, id, store.HostStatusError, store.HostError(msgs.Errorf("hub.installInterruptedByRestart"))); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddHostEvent(ctx, store.HostEvent{TS: store.Now(), HostID: id, HostName: "web-1", Kind: store.EventUnreachable,
		Detail: "x", DetailKey: "hub.installInterruptedByRestart"}); err != nil {
		t.Fatal(err)
	}
	jm := jobs.New(db, log)
	defer jm.Close()
	manager := hub.NewManager(cfg, db, key, version, log)
	handler := hub.New(hub.Deps{Cfg: cfg, DB: db, Auth: authSvc, Hub: manager, Log: log, Jobs: jm}).Handler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	res, err := client.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", err, res)
	}
	_ = res.Body.Close()
	sweepEnglish(t, handler, client, ts.URL, 30)
}

// russianSnippets — места с кириллицей и немного текста вокруг, не больше
// пяти на ответ: этого хватает понять, откуда строка.
func russianSnippets(s string) []string {
	var out []string
	next := 0
	for _, loc := range cyrillic.FindAllStringIndex(s, -1) {
		if loc[0] < next {
			continue
		}
		from, to := max(0, loc[0]-40), min(len(s), loc[1]+80)
		next = to
		out = append(out, strings.ToValidUTF8(s[from:to], ""))
		if len(out) == 5 {
			break
		}
	}
	return out
}

// sweepEnglish обходит все GET-маршруты handler без параметров с языком
// en и сообщает о каждой кириллице в ответе.
func sweepEnglish(t *testing.T, handler http.Handler, client *http.Client, base string, minRoutes int) {
	t.Helper()
	var routes []string
	err := chi.Walk(handler.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != http.MethodGet || strings.Contains(route, "{") || strings.Contains(route, "*") {
			return nil
		}
		for _, s := range i18nSweepSkip {
			if strings.Contains(route, s) {
				return nil
			}
		}
		routes = append(routes, route)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(routes)
	if len(routes) < minRoutes {
		t.Fatalf("обошли подозрительно мало маршрутов: %d", len(routes))
	}
	for _, route := range routes {
		req, _ := http.NewRequest(http.MethodGet, base+route, nil)
		req.Header.Set("X-NKT-Lang", "en")
		res, err := client.Do(req)
		if err != nil {
			t.Errorf("%s: %v", route, err)
			continue
		}
		data, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		for _, hit := range russianSnippets(string(data)) {
			t.Errorf("%s: %s", route, hit)
		}
	}
}
