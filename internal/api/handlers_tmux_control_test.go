package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/config"
)

// TestHandleTmuxWindowsGates confirms the same two guards handleTerminalWS
// itself checks (NKT_TERMINAL_ENABLED, ModeFixtures) also cover the window
// list — reporting running:false rather than an error, since "tmux mode
// isn't available on this host" isn't a failure the frontend needs to
// treat differently from "no session running yet".
func TestHandleTmuxWindowsGates(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
	}{
		{"disabled by default", &config.Config{Mode: config.ModeLocal, TerminalEnabled: false}},
		{"refused in fixtures mode even if enabled", &config.Config{Mode: config.ModeFixtures, TerminalEnabled: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{cfg: c.cfg}
			req := httptest.NewRequest(http.MethodGet, "/api/terminal/tmux/windows", nil)
			rec := httptest.NewRecorder()

			s.handleTmuxWindows(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			var got struct {
				Running bool `json:"running"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Running {
				t.Error("running = true, want false")
			}
		})
	}
}

// TestHandleTmuxWindowsNoSession confirms the common case on a host that
// has tmux installed but nobody has opened "Открыть в tmux" yet (or the
// session has since ended): tmux itself exits non-zero for "can't find
// session", which must read as running:false, not a 500.
func TestHandleTmuxWindowsNoSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH in this environment")
	}
	t.Setenv("INVOCATION_ID", "") // force the plain-exec path, no systemd/nsenter escape hatch

	s := &Server{cfg: &config.Config{Mode: config.ModeLocal, TerminalEnabled: true}}
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/tmux/windows", nil)
	rec := httptest.NewRecorder()

	s.handleTmuxWindows(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Running bool         `json:"running"`
		Windows []tmuxWindow `json:"windows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Running {
		t.Error("running = true for a session that (almost certainly) doesn't exist in this test environment")
	}
	if len(got.Windows) != 0 {
		t.Errorf("windows = %v, want empty", got.Windows)
	}
}

// TestHandleTmuxActionUnknownAction confirms an unrecognised action is
// rejected outright with a 400 before ever touching tmux — the frontend
// only ever sends the fixed set handleTmuxAction knows about, so reaching
// this means a request was hand-crafted or the two have drifted apart.
func TestHandleTmuxActionUnknownAction(t *testing.T) {
	s := &Server{cfg: &config.Config{Mode: config.ModeLocal, TerminalEnabled: true}}
	body := strings.NewReader(`{"action":"rm -rf /"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/tmux/action", body)
	rec := httptest.NewRecorder()

	s.handleTmuxAction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
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

// TestHandleTmuxActionGatedLikeTerminal confirms handleTmuxAction refuses
// outright under the same two conditions handleTerminalWS itself does,
// rather than attempting (and failing) a tmux call against a feature the
// host never had enabled in the first place.
func TestHandleTmuxActionGatedLikeTerminal(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
	}{
		{"disabled by default", &config.Config{Mode: config.ModeLocal, TerminalEnabled: false}},
		{"refused in fixtures mode even if enabled", &config.Config{Mode: config.ModeFixtures, TerminalEnabled: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{cfg: c.cfg}
			body := strings.NewReader(`{"action":"new-window"}`)
			req := httptest.NewRequest(http.MethodPost, "/api/terminal/tmux/action", body)
			rec := httptest.NewRecorder()

			s.handleTmuxAction(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}
}
