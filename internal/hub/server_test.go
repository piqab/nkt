package hub

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// TestProxyHostRequiresAdmin is a regression test for a broken-access-control
// bug: /api/hosts/{id}/* used to sit behind RequireAuth only, not
// RequireAdmin, on the theory that the proxied host's own RequireAdmin check
// would still apply. That theory was wrong — Manager.cookieFor always logs
// into the target host as *that host's* saved bootstrap-admin account (see
// proxy.go), never as whoever is actually authenticated to the hub, so the
// host-side check only ever sees "admin". A hub "viewer" account — who
// cannot add, install or remove a host — could still open any already-
// connected one and get full admin control over it. Fixed by moving the
// route inside the RequireAdmin group in server.go; this test logs in as
// both roles and confirms only admin gets past the hub's own gate (a viewer
// must get 403 without ever reaching the SSH proxy at all — this test uses a
// host id that doesn't exist, so an admin request failing for any reason
// other than 403 is still proof the gate itself let it through).
func TestProxyHostRequiresAdmin(t *testing.T) {
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

	srv := New(Deps{Cfg: cfg, DB: db, Auth: authSvc, Hub: manager, Log: slog.Default()})
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

	// A host id that doesn't exist — a viewer must never even reach the
	// point where that matters.
	const path = "/api/hosts/999/inventory"

	t.Run("viewer forbidden", func(t *testing.T) {
		cookie := login(t, "viewer")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("viewer proxy request: status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("admin passes the gate", func(t *testing.T) {
		cookie := login(t, "admin")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		handler.ServeHTTP(rec, req)
		// The host doesn't exist, so this can never succeed — but it must
		// fail for that reason (Manager.clientFor's own error, surfaced as
		// 502), not because the hub's own authorization gate stopped it.
		if rec.Code == http.StatusForbidden {
			t.Errorf("admin proxy request: got 403 — the RequireAdmin gate should not apply to admin")
		}
	})
}
