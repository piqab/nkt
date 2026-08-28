package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/config"
)

// TestHandleTerminalWSGates locks in the two guards handleTerminalWS checks
// before ever touching the network: NKT_TERMINAL_ENABLED must be on, and
// ModeFixtures is refused outright regardless of that flag — a demo/
// fixtures instance must never spawn a real shell on whatever machine
// happens to be running it. Both checks return before websocket.Accept, so
// a bare httptest.NewRecorder() is enough — no real WebSocket handshake or
// PTY needed to cover the part most likely to regress silently (e.g. a
// future refactor of the default flags).
func TestHandleTerminalWSGates(t *testing.T) {
	cases := []struct {
		name     string
		cfg      *config.Config
		wantCode int
	}{
		{
			name:     "disabled by default",
			cfg:      &config.Config{Mode: config.ModeLocal, TerminalEnabled: false},
			wantCode: http.StatusForbidden,
		},
		{
			name:     "refused in fixtures mode even if enabled",
			cfg:      &config.Config{Mode: config.ModeFixtures, TerminalEnabled: true},
			wantCode: http.StatusForbidden,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{cfg: c.cfg}
			req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws", nil)
			rec := httptest.NewRecorder()

			s.handleTerminalWS(rec, req)

			if rec.Code != c.wantCode {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, c.wantCode, rec.Body.String())
			}
		})
	}
}

// TestLoginShellPrefersBash locks in that the terminal always opens bash
// when it's available, regardless of what $SHELL happens to be set to in
// nkt's own process environment — often unset entirely under systemd, but
// not guaranteed, and picking up something unexpected there (a service
// account's /usr/sbin/nologin, a stray Environment= in the unit, etc.)
// must not silently change what shell the operator gets.
func TestLoginShellPrefersBash(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH in this environment")
	}

	t.Setenv("SHELL", "/definitely/not/a/real/shell")

	got := loginShell()
	if got != bashPath {
		t.Errorf("loginShell() = %q, want %q (bash, ignoring $SHELL)", got, bashPath)
	}
}

// TestLoginShellFallsBackToShellEnv confirms $SHELL is still honoured when
// bash genuinely isn't installed — a minimal, non-Debian image is exactly
// the case loginShell's fallback chain exists for.
func TestLoginShellFallsBackToShellEnv(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir: no bash to find
	t.Setenv("SHELL", "/bin/zsh")

	if got := loginShell(); got != "/bin/zsh" {
		t.Errorf("loginShell() = %q, want %q", got, "/bin/zsh")
	}
}

// TestLoginShellFallsBackToPOSIXBaseline is the last resort: neither bash
// nor $SHELL available at all.
func TestLoginShellFallsBackToPOSIXBaseline(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "")

	if got := loginShell(); got != "/bin/sh" {
		t.Errorf("loginShell() = %q, want %q", got, "/bin/sh")
	}
}

// TestHandleTerminalConfig is the regression test for the frontend's idle
// countdown (PtyToolbar): confirms the reported idle_timeout_s is this
// host's own TerminalIdleTimeout in seconds, and that it's reported
// regardless of TerminalEnabled/Mode — unlike handleTerminalWS, this value
// is shared by every WS session runPTYSession/runUpdateSession's idle
// timer governs (packages/ufw/firewalld/dbus/tmux install too), not just
// the interactive shell.
func TestHandleTerminalConfig(t *testing.T) {
	s := &Server{cfg: &config.Config{
		Mode:                config.ModeFixtures,
		TerminalEnabled:     false,
		TerminalIdleTimeout: 90 * time.Second,
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/config", nil)
	rec := httptest.NewRecorder()

	s.handleTerminalConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		IdleTimeoutS int `json:"idle_timeout_s"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if got.IdleTimeoutS != 90 {
		t.Errorf("idle_timeout_s = %d, want 90", got.IdleTimeoutS)
	}
}

// TestEnsureTmuxSession confirms it both creates a session that doesn't
// exist yet and is idempotent against one that already does — the two
// cases handleTerminalWS's tmux mode actually hits (first open, and every
// reattach after it).
func TestEnsureTmuxSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH in this environment")
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", tmuxSessionName).Run() })
	_ = exec.Command("tmux", "kill-session", "-t", tmuxSessionName).Run() // in case a prior run leaked one

	s := &Server{cfg: &config.Config{Mode: config.ModeLocal, TerminalEnabled: true}}
	ctx := context.Background()

	if err := s.ensureTmuxSession(ctx); err != nil {
		t.Fatalf("ensureTmuxSession (create): %v", err)
	}
	if err := exec.Command("tmux", "has-session", "-t", tmuxSessionName).Run(); err != nil {
		t.Fatalf("session %q was not actually created: %v", tmuxSessionName, err)
	}

	if err := s.ensureTmuxSession(ctx); err != nil {
		t.Fatalf("ensureTmuxSession (already exists): %v", err)
	}
}
