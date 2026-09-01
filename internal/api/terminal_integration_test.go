package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/store"
)

// TestTerminalRoundTrip is a real end-to-end exercise of the feature, not a
// mock: a real SQLite-backed server, a real login over HTTP, a real
// WebSocket upgrade through the actual router (including the
// RequireAuth+RequireAdmin chain and the Timeout-middleware split in
// server.go), a real shell spawned via creack/pty, and a real round trip —
// send a shell command, read the echoed output back. This is the only
// thing in the package that would have caught a mistake in the routing
// split (terminal accidentally still under middleware.Timeout, breaking
// the Hijacker the WS upgrade needs) or a wiring bug between the frontend's
// binary/text frame convention and the server's.
func TestTerminalRoundTrip(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const password = "test-password-0123456789"
	cfg := &config.Config{
		Mode:                   config.ModeLocal,
		TerminalEnabled:        true,
		TerminalIdleTimeout:    time.Minute,
		AllowMutations:         true,
		SessionTTL:             time.Hour,
		CookieSecure:           false,
		BootstrapAdminUser:     "admin",
		BootstrapAdminPassword: password,
	}
	authSvc := auth.NewService(db, cfg)
	if _, err := authSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	srv := New(Deps{Cfg: cfg, DB: db, Auth: authSvc, Log: slog.Default()})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	res, err := client.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", res.StatusCode)
	}

	var cookieHeader string
	for _, c := range jar.Cookies(res.Request.URL) {
		if c.Name == auth.SessionCookie {
			cookieHeader = c.Name + "=" + c.Value
		}
	}
	if cookieHeader == "" {
		t.Fatal("no session cookie captured after login")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/terminal/ws"
	header := http.Header{}
	header.Set("Cookie", cookieHeader)
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer conn.CloseNow()

	const marker = "nkt-terminal-roundtrip-ok"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo "+marker+"\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out strings.Builder
	found := false
	for !found {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read (collected so far: %q): %v", out.String(), err)
		}
		if msgType != websocket.MessageBinary {
			continue
		}
		out.Write(data)
		if strings.Contains(out.String(), marker) {
			found = true
		}
	}

	conn.Close(websocket.StatusNormalClosure, "test done")
}

// TestTerminalRoundTripTmux is TestTerminalRoundTrip's tmux-mode
// counterpart — it exercises ensureTmuxSession + `tmux attach-session`
// end to end (real tmux server, real PTY, real WebSocket), which
// TestTerminalRoundTrip itself never touches. Catches a regression in the
// argv/flags handleTerminalWS builds for tmux mode (e.g. a typo in
// tmuxSessionName or the attach-session target) that only a real tmux
// binary would surface.
func TestTerminalRoundTripTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH in this environment")
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", tmuxSessionName).Run() })

	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const password = "test-password-0123456789"
	cfg := &config.Config{
		Mode:                   config.ModeLocal,
		TerminalEnabled:        true,
		TerminalIdleTimeout:    time.Minute,
		AllowMutations:         true,
		SessionTTL:             time.Hour,
		CookieSecure:           false,
		BootstrapAdminUser:     "admin",
		BootstrapAdminPassword: password,
	}
	authSvc := auth.NewService(db, cfg)
	if _, err := authSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	srv := New(Deps{Cfg: cfg, DB: db, Auth: authSvc, Log: slog.Default()})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	res, err := client.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", res.StatusCode)
	}

	var cookieHeader string
	for _, c := range jar.Cookies(res.Request.URL) {
		if c.Name == auth.SessionCookie {
			cookieHeader = c.Name + "=" + c.Value
		}
	}
	if cookieHeader == "" {
		t.Fatal("no session cookie captured after login")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/terminal/ws?tmux=1"
	header := http.Header{}
	header.Set("Cookie", cookieHeader)
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer conn.CloseNow()

	const marker = "nkt-terminal-tmux-roundtrip-ok"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo "+marker+"\r")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out strings.Builder
	found := false
	for !found {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read (collected so far: %q): %v", out.String(), err)
		}
		if msgType != websocket.MessageBinary {
			continue
		}
		out.Write(data)
		if strings.Contains(out.String(), marker) {
			found = true
		}
	}

	conn.Close(websocket.StatusNormalClosure, "test done")
}

// TestTerminalRequiresAuth confirms the route actually sits behind
// RequireAuth+RequireAdmin — a session-less dial must be rejected during
// the HTTP handshake (before any WebSocket upgrade happens at all), not
// silently hand over a shell.
func TestTerminalRequiresAuth(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{Mode: config.ModeLocal, TerminalEnabled: true, AllowMutations: true}
	authSvc := auth.NewService(db, cfg)

	srv := New(Deps{Cfg: cfg, DB: db, Auth: authSvc, Log: slog.Default()})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/terminal/ws"
	_, _, err = websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Fatal("expected the handshake to be rejected without a session cookie")
	}
}
