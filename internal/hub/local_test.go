package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/api"
	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// fixtureLocalScanner builds a real inventory.Scanner over the repo's
// fixtures/host tree (same fixtures internal/inventory's own tests use) and
// runs one scan, so localHostEntry has real findings/host data to fold in —
// not a hand-built model.Snapshot that might drift from what a real scan
// actually produces.
func fixtureLocalScanner(t *testing.T) *inventory.Scanner {
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
		LibvirtURI:      "qemu:///system",
		CommandTimeout:  5 * time.Second,
	}
	scanner := inventory.New(cfg, collect.NewFixtures(root), nil)
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return scanner
}

// TestLocalHostEntryNilWithoutLocal confirms a hub built without an embedded
// local api.Server (Local == nil, the default for every hub before this
// feature and for tests that don't need it) pins no synthetic row at all —
// see localHostEntry's own doc comment for why that's the right behavior
// rather than showing a permanently-broken "localhost" entry.
func TestLocalHostEntryNilWithoutLocal(t *testing.T) {
	m, db := newTestManager(t)
	srv := New(Deps{DB: db, Hub: m})
	if entry := srv.localHostEntry(); entry != nil {
		t.Fatalf("localHostEntry() = %+v, want nil when Local is unset", entry)
	}
}

// TestLocalHostEntryPopulatesFromScanner confirms the synthetic row carries
// the hub's own version (there is nothing else to compare it against — it's
// always whatever build the hub itself is) and the embedded scanner's latest
// findings/timestamp, exactly like a real managed host's Manager.Overview
// path populates its own row in handleListHosts.
func TestLocalHostEntryPopulatesFromScanner(t *testing.T) {
	m, db := newTestManager(t)
	scanner := fixtureLocalScanner(t)
	snap := scanner.Latest()
	if snap == nil {
		t.Fatal("test setup bug: scanner has no latest snapshot")
	}

	srv := New(Deps{DB: db, Hub: m, Local: http.NotFoundHandler(), LocalScanner: scanner})
	entry := srv.localHostEntry()
	if entry == nil {
		t.Fatal("localHostEntry() = nil, want a populated row when Local is set")
	}

	if entry.ID != localHostID {
		t.Errorf("ID = %d, want %d", entry.ID, localHostID)
	}
	if entry.Name != "localhost" {
		t.Errorf("Name = %q, want %q", entry.Name, "localhost")
	}
	if entry.Status != store.HostStatusOnline {
		t.Errorf("Status = %q, want %q", entry.Status, store.HostStatusOnline)
	}
	if entry.Reachable == nil || !*entry.Reachable {
		t.Errorf("Reachable = %v, want a present true", entry.Reachable)
	}
	if entry.RunningVersion != m.Version() {
		t.Errorf("RunningVersion = %q, want the hub's own version %q", entry.RunningVersion, m.Version())
	}
	if !reflect.DeepEqual(entry.Findings, snap.FindingCounts()) {
		t.Errorf("Findings = %+v, want %+v (the scanner's own latest snapshot)", entry.Findings, snap.FindingCounts())
	}
	if entry.LastPolledAt != snap.TS {
		t.Errorf("LastPolledAt = %q, want %q", entry.LastPolledAt, snap.TS)
	}
}

// TestHandleListHostsPrependsLocalhost confirms the synthetic row always
// comes first, ahead of every real (SSH-managed) host, regardless of how the
// real hosts happen to sort.
func TestHandleListHostsPrependsLocalhost(t *testing.T) {
	m, db := newTestManager(t)
	ctx := t.Context()
	if _, err := m.AddHost(ctx, "zzz-real-host", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	scanner := fixtureLocalScanner(t)
	srv := New(Deps{DB: db, Hub: m, Local: http.NotFoundHandler(), LocalScanner: scanner})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hub/hosts", nil)
	srv.handleListHosts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleListHosts: status %d, body %s", rec.Code, rec.Body.String())
	}

	var out []hostWithOverview
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rec.Body.String())
	}
	if len(out) != 2 {
		t.Fatalf("got %d hosts, want 2 (localhost + the one real host)", len(out))
	}
	if out[0].ID != localHostID || out[0].Name != "localhost" {
		t.Errorf("out[0] = %+v, want the synthetic localhost row first", out[0])
	}
	if out[1].Name != "zzz-real-host" {
		t.Errorf("out[1].Name = %q, want the real host", out[1].Name)
	}
}

// TestProxyLocalReturns503WithoutLocal confirms proxyLocal fails cleanly —
// not with a nil-pointer panic — on a hub that wasn't built with an embedded
// local api.Server.
func TestProxyLocalReturns503WithoutLocal(t *testing.T) {
	m, db := newTestManager(t)
	srv := New(Deps{DB: db, Hub: m})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hosts/local/overview", nil)
	srv.proxyLocal(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// TestHostsLocalRouteAuthAndForwarding exercises /api/hosts/local/* through
// the full router (not proxyLocal directly): confirms it forwards to the
// embedded Local handler with the path rewritten from /hosts/local/<rest> to
// /api/<rest> (mirroring proxyHost's own rewrite for a real managed host),
// and — the actual point of this route sitting OUTSIDE the RequireAdmin
// group that guards /hosts/{id}/* (see server.go's comment on why) — that a
// hub "viewer" account can reach it just as freely as an admin can, while an
// unauthenticated request still can't.
func TestHostsLocalRouteAuthAndForwarding(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{AllowMutations: true, SessionTTL: time.Hour, CookieSecure: false}
	authSvc := auth.NewService(db, cfg)

	hash, err := auth.HashPassword("admin-password-1234")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := db.CreateUser(context.Background(), "admin", hash, store.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := db.CreateUser(context.Background(), "viewer", hash, store.RoleViewer); err != nil {
		t.Fatalf("create viewer: %v", err)
	}

	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	manager := NewManager(cfg, db, key, "test", slog.New(slog.DiscardHandler))

	var gotPath string
	local := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	srv := New(Deps{Cfg: cfg, DB: db, Auth: authSvc, Hub: manager, Local: local, Log: slog.Default()})
	handler := srv.Handler()

	login := func(t *testing.T, username string) *http.Cookie {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"username":"`+username+`","password":"admin-password-1234"}`))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login as %s: status %d: %s", username, rec.Code, rec.Body.String())
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.SessionCookie {
				return c
			}
		}
		t.Fatalf("login as %s: no session cookie set", username)
		return nil
	}

	t.Run("unauthenticated is rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/hosts/local/overview", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("viewer reaches the embedded handler", func(t *testing.T) {
		gotPath = ""
		cookie := login(t, "viewer")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/hosts/local/overview", nil)
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden {
			t.Fatal("viewer got 403 — /hosts/local/* must not sit behind RequireAdmin, unlike /hosts/{id}/*")
		}
		if gotPath != "/api/overview" {
			t.Errorf("Local handler saw path %q, want %q", gotPath, "/api/overview")
		}
	})

	t.Run("admin reaches the embedded handler", func(t *testing.T) {
		gotPath = ""
		cookie := login(t, "admin")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/hosts/local/overview", nil)
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		if gotPath != "/api/overview" {
			t.Errorf("Local handler saw path %q, want %q", gotPath, "/api/overview")
		}
	})
}

// TestHostsLocalRouteWithRealEmbeddedAPIServer is the end-to-end version of
// the test above: instead of a hand-written stub http.HandlerFunc standing
// in for Local, this wires up the actual api.New(...) the same way runHub
// does (cmd/nkt/main.go) — same fixtures-backed scanner/collector, the same
// set of control managers, no Scheduler and no UI, exactly what a real hub
// process builds for its embedded "localhost" server — and drives several
// of the pages a browser actually opens (overview, findings, services)
// through the full router, not just a single synthetic path. Catches
// anything the stub-based test above structurally cannot: a real routing or
// wiring mismatch between the hub's own /hosts/local/* prefix and what
// api.Server actually registers under /api/*.
func TestHostsLocalRouteWithRealEmbeddedAPIServer(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("fixtures root: %v", err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{
		Mode:            config.ModeFixtures,
		FixturesRoot:    root,
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		ComposeFiles:    []string{"/srv/docker/docker-compose.yml"},
		LibvirtURI:      "qemu:///system",
		CommandTimeout:  5 * time.Second,
		AllowMutations:  true,
		SessionTTL:      time.Hour,
		CookieSecure:    false,
	}
	authSvc := auth.NewService(db, cfg)
	hash, err := auth.HashPassword("admin-password-1234")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := db.CreateUser(context.Background(), "admin", hash, store.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	// Mirrors newHubRuntime/runHub in cmd/nkt/main.go field for field.
	collector := collect.NewFixtures(root)
	scanner := inventory.New(cfg, collector, db)
	services := control.NewServiceManager(cfg, collector, db)
	localAPI := api.New(api.Deps{
		Cfg: cfg, DB: db, Auth: authSvc, Scanner: scanner,
		Services:  services,
		Configs:   control.NewConfigManager(cfg, collector, db, scanner, services),
		Firewall:  control.NewFirewallManager(cfg, collector, db),
		Firewalld: control.NewFirewalldManager(cfg, collector, db),
		Certs:     control.NewCertManager(cfg, collector, db, services, scanner),
		Podman:    control.NewPodmanManager(collector, db),
		LXD:       control.NewLXDManager(collector, db),
		Libvirt:   control.NewLibvirtManager(cfg, collector, db, scanner),
		Log:       slog.Default(), Version: "test",
	})

	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	manager := NewManager(cfg, db, key, "test", slog.New(slog.DiscardHandler))

	srv := New(Deps{
		Cfg: cfg, DB: db, Auth: authSvc, Hub: manager,
		Local: localAPI.Handler(), LocalScanner: scanner, Log: slog.Default(),
	})
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"admin-password-1234"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d: %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("login: no session cookie set")
	}

	for _, path := range []string{
		"/api/hosts/local/overview",
		"/api/hosts/local/findings",
		"/api/hosts/local/services",
		"/api/hosts/local/inventory",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(cookie)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}
}
