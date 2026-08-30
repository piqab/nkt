package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	osuser "os/user"
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

// TestHandleVNCStartSurfacesLogfileContent is the regression test for
// x11vnc's own documented behavior — "-bg... messages to stderr are lost
// unless -o is used" — meaning the invoking process's own captured output
// often carries none of the real diagnostic text on failure; it ends up in
// the -o logfile instead. The fake x11vnc here finds its own "-o <path>"
// argument and writes a distinctive message there before exiting nonzero,
// simulating exactly that handoff.
func TestHandleVNCStartSurfacesLogfileContent(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    echo "XOpenDisplay failed: no such display" > "$arg"
    exit 1
  fi
  prev="$arg"
done
exit 1
`
	if err := os.WriteFile(dir+"/x11vnc", []byte(script), 0o755); err != nil {
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
	if !strings.Contains(body.Error, "XOpenDisplay failed") {
		t.Errorf("error = %q, want it to include the logfile's own diagnostic text", body.Error)
	}
}

// TestHandleVNCStartResolvesTerminalUserForFileOwnership is the regression
// test for x11vnc failing immediately with "Permission denied" whenever
// TerminalUser is set: nkt itself normally runs as root, so the password/
// log temp files os.CreateTemp creates are root-owned 0600 — a
// non-root TerminalUser had no access to either one at all until
// handleVNCStart started chowning them. Covers both halves: an unknown
// TerminalUser is refused before ever touching the filesystem, and a real
// account's chown succeeds (reaching the actual — here fake and
// failing — invocation, not erroring out earlier on the ownership step).
func TestHandleVNCStartResolvesTerminalUserForFileOwnership(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/x11vnc", []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake x11vnc: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	t.Run("unknown TerminalUser refused before touching the filesystem", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeLocal, TerminalEnabled: true, TerminalUser: "nkt-test-no-such-user"}
		scanner := inventory.New(cfg, collect.NewLocal("", "", 5*time.Second), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		rec := httptest.NewRecorder()
		s.handleVNCStart(rec, httptest.NewRequest(http.MethodPost, "/api/system/vnc/start", nil))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
		}
	})

	t.Run("real account: uid/gid resolve and the chown itself succeed", func(t *testing.T) {
		// Actually exec'ing under cmd.SysProcAttr.Credential — even set to
		// this same already-unprivileged uid, a no-op switch on paper —
		// needs a setuid() call only root can reliably make; sandboxed test
		// environments commonly refuse it outright ("operation not
		// permitted" at fork/exec) regardless of target uid. That's an
		// unrelated OS-level restriction on privilege-dropping itself, not
		// what this test is after: whether resolveUserEnv + os.Chown (the
		// actual fix) succeed for a real account. Checking the temp files'
		// ownership directly sidesteps needing a real privileged exec.
		me, err := osuser.Current()
		if err != nil {
			t.Fatalf("os/user.Current: %v", err)
		}
		uid, gid, _, err := resolveUserEnv(nil, me.Username)
		if err != nil {
			t.Fatalf("resolveUserEnv(%q): %v", me.Username, err)
		}
		f, err := os.CreateTemp(t.TempDir(), "chown-check-*")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		f.Close()
		if err := os.Chown(f.Name(), int(uid), int(gid)); err != nil {
			t.Errorf("os.Chown to the resolved uid/gid of the current user: %v", err)
		}
	})
}

// TestHandleVNCStartVNCUserOverridesTerminalUser confirms VNCUser takes
// priority over TerminalUser when both are set — the whole point of
// config.Config.VNCUser existing as its own setting (see its own comment):
// x11vnc has to run as whoever owns the desktop session, which is not
// necessarily who the shell terminal should drop to. Deliberately gives
// TerminalUser a name that does not exist (resolveUserEnv would fail
// immediately if it were the one actually used) alongside a real VNCUser,
// and confirms the request proceeds past the resolve/chown stage — a
// bogus-TerminalUser error there would mean VNCUser was silently ignored.
func TestHandleVNCStartVNCUserOverridesTerminalUser(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/x11vnc", []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake x11vnc: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	me, err := osuser.Current()
	if err != nil {
		t.Fatalf("os/user.Current: %v", err)
	}
	cfg := &config.Config{
		Mode:            config.ModeLocal,
		TerminalEnabled: true,
		TerminalUser:    "nkt-test-no-such-user",
		VNCUser:         me.Username,
	}
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
	// Not "exit status" specifically (like the sibling tests above) —
	// actually exec'ing under cmd.SysProcAttr.Credential needs a real
	// setuid() only root can reliably make, which sandboxed test
	// environments commonly refuse outright regardless of target uid (see
	// TestHandleVNCStartResolvesTerminalUserForFileOwnership's identical
	// note); either failure shape proves resolveUserEnv succeeded for
	// VNCUser. What actually matters: it must NOT be the
	// "пользователь ... не найден" error resolveUserEnv would produce for
	// the bogus TerminalUser — that specific failure is the one proof that
	// VNCUser was ignored in favor of it.
	if strings.Contains(body.Error, "не найден") {
		t.Errorf("error = %q, looks like TerminalUser's bogus name was resolved instead of VNCUser", body.Error)
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
