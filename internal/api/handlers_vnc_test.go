package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
)

// newVNCTestServer builds a Server backed by an empty fixtures collector —
// collect.Which("x11vnc")/collect.Which("apt-get") both come back false
// (Fixtures.Run defaults unmatched commands to exit 127), which is exactly
// what every gate test below needs: none of them are meant to reach a real
// x11vnc/apt-get invocation at all.
func newVNCTestServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
	return &Server{cfg: cfg, scanner: scanner}
}

// TestHandleVNCStatusNotInstalled confirms the common case (x11vnc absent)
// reports both installed and running false, plus the fixed port regardless.
func TestHandleVNCStatusNotInstalled(t *testing.T) {
	s := newVNCTestServer(t, &config.Config{Mode: config.ModeLocal})

	rec := httptest.NewRecorder()
	s.handleVNCStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/vnc-status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Installed bool `json:"installed"`
		Running   bool `json:"running"`
		Port      int  `json:"port"`
	}
	decodeJSONBody(t, rec, &body)
	if body.Installed {
		t.Error("installed = true, want false (fixtures collector has no x11vnc)")
	}
	if body.Running {
		t.Error("running = true, want false — must not even check pgrep when not installed")
	}
	if body.Port != vncPort {
		t.Errorf("port = %d, want %d", body.Port, vncPort)
	}
}

// TestHandleVNCInstallWSGates mirrors TestHandleDbusInstallWSGates for the
// guard clauses that must return before ever spawning apt-get.
func TestHandleVNCInstallWSGates(t *testing.T) {
	t.Run("refused in fixtures mode", func(t *testing.T) {
		s := newVNCTestServer(t, &config.Config{Mode: config.ModeFixtures})

		rec := httptest.NewRecorder()
		s.handleVNCInstallWS(rec, httptest.NewRequest(http.MethodGet, "/api/system/vnc-install/ws", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused without apt-get", func(t *testing.T) {
		s := newVNCTestServer(t, &config.Config{Mode: config.ModeLocal})

		rec := httptest.NewRecorder()
		s.handleVNCInstallWS(rec, httptest.NewRequest(http.MethodGet, "/api/system/vnc-install/ws", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}

// TestHandleVNCInstallStatusIdle confirms the "vnc-install" session key
// reports cleanly with no job ever started — the same shape
// handleTmuxInstallStatus's own idle case has.
func TestHandleVNCInstallStatusIdle(t *testing.T) {
	s := newVNCTestServer(t, &config.Config{Mode: config.ModeLocal})

	rec := httptest.NewRecorder()
	s.handleVNCInstallStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/vnc-install/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Active   bool `json:"active"`
		Finished bool `json:"finished"`
	}
	decodeJSONBody(t, rec, &body)
	if body.Active || body.Finished {
		t.Errorf("body = %+v, want both false — no install has ever run", body)
	}
}

// TestHandleVNCStartGates covers every guard clause that must return
// before actually trying to launch x11vnc: disabled by config, fixtures
// mode, and not installed. Each of these fires strictly before
// handleVNCStart ever generates a password or touches the filesystem, so
// this is safe to run without a real x11vnc on the test machine.
func TestHandleVNCStartGates(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		s := newVNCTestServer(t, &config.Config{Mode: config.ModeLocal, TerminalEnabled: false})

		rec := httptest.NewRecorder()
		s.handleVNCStart(rec, httptest.NewRequest(http.MethodPost, "/api/system/vnc/start", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused in fixtures mode even if enabled", func(t *testing.T) {
		s := newVNCTestServer(t, &config.Config{Mode: config.ModeFixtures, TerminalEnabled: true})

		rec := httptest.NewRecorder()
		s.handleVNCStart(rec, httptest.NewRequest(http.MethodPost, "/api/system/vnc/start", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused when x11vnc is not installed", func(t *testing.T) {
		s := newVNCTestServer(t, &config.Config{Mode: config.ModeLocal, TerminalEnabled: true})

		rec := httptest.NewRecorder()
		s.handleVNCStart(rec, httptest.NewRequest(http.MethodPost, "/api/system/vnc/start", nil))

		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
		}
	})
}

// TestHandleVNCStartSurfacesRealReasonOnSilentFailure is the regression
// test for "не удалось запустить x11vnc: " with nothing after the colon —
// happened whenever x11vnc's own combined output was empty (the process
// exits immediately without printing anything, or the invocation never
// actually ran at all), since only cmd.CombinedOutput()'s out fed the
// error message, discarding err's own text entirely. Uses a real
// collect.Local (not the fixtures collector every other test in this file
// uses) with a fake x11vnc on PATH that exits 1 with zero output —
// collect.Which sees it as "installed" via the same real PATH lookup the
// actual invocation then fails through, reaching the exact code path the
// fixtures-backed gate tests above never touch.
func TestHandleVNCStartSurfacesRealReasonOnSilentFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/x11vnc", []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake x11vnc: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &config.Config{Mode: config.ModeLocal, TerminalEnabled: true}
	scanner := inventory.New(cfg, collect.NewLocal("", "", 5*time.Second), nil)
	s := &Server{cfg: cfg, scanner: scanner}

	rec := httptest.NewRecorder()
	s.handleVNCStart(rec, httptest.NewRequest(http.MethodPost, "/api/system/vnc/start", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	decodeJSONBody(t, rec, &body)
	if !strings.Contains(body.Error, "exit status") {
		t.Errorf("error = %q, want a real reason after the colon (e.g. \"exit status 1\"), not empty", body.Error)
	}
}

// TestHandleVNCStopNotRunning confirms stopping when nothing is running
// reports 404 rather than a bare unexplained pkill failure — this reaches
// a real pgrep/pkill -x x11vnc on the test machine (not through the
// fixtures collector, see handleVNCStop's own comment on why no PID
// verification step is needed here), which is safe: x11vnc is not
// expected to be running on the machine these tests execute on.
func TestHandleVNCStopNotRunning(t *testing.T) {
	s := newVNCTestServer(t, &config.Config{Mode: config.ModeLocal})

	rec := httptest.NewRecorder()
	s.handleVNCStop(rec, httptest.NewRequest(http.MethodPost, "/api/system/vnc/stop", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
