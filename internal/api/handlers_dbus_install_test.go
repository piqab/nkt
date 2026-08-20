package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
)

func decodeJSONBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode response body: %v (body: %s)", err, rec.Body.String())
	}
}

func touchFile(path string) error {
	return os.WriteFile(path, nil, 0o600)
}

// TestHandleDbusStatus covers the three states the "install dbus" button
// needs to distinguish: nothing to show (no systemd unit context at all),
// a working button (unit context + nsenter usable), and "поставьте вручную"
// (unit context but no way to escape the sandbox to run the installer
// either).
func TestHandleDbusStatus(t *testing.T) {
	t.Run("not sandboxed: nothing needed", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "")
		s := &Server{cfg: &config.Config{}}

		rec := httptest.NewRecorder()
		s.handleDbusStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/dbus-status", nil))

		var body struct {
			Needed     bool `json:"needed"`
			CanInstall bool `json:"can_install"`
		}
		decodeJSONBody(t, rec, &body)
		if body.Needed || body.CanInstall {
			t.Errorf("body = %+v, want both false without INVOCATION_ID", body)
		}
	})

	t.Run("sandboxed, no control socket, nsenter available: can install", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "deadbeef")
		t.Setenv("PATH", withFakeBinaryOnPath(t, "nsenter"))
		withSystemdControlSocketPaths(t, t.TempDir()+"/private", t.TempDir()+"/system_bus_socket")
		s := &Server{cfg: &config.Config{}}

		rec := httptest.NewRecorder()
		s.handleDbusStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/dbus-status", nil))

		var body struct {
			Needed     bool `json:"needed"`
			CanInstall bool `json:"can_install"`
		}
		decodeJSONBody(t, rec, &body)
		if !body.Needed || !body.CanInstall {
			t.Errorf("body = %+v, want both true", body)
		}
	})

	t.Run("sandboxed, no control socket, no nsenter either: needed but can't install", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "deadbeef")
		t.Setenv("PATH", t.TempDir()) // neither systemd-run nor nsenter resolvable
		withSystemdControlSocketPaths(t, t.TempDir()+"/private", t.TempDir()+"/system_bus_socket")
		s := &Server{cfg: &config.Config{}}

		rec := httptest.NewRecorder()
		s.handleDbusStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/dbus-status", nil))

		var body struct {
			Needed     bool `json:"needed"`
			CanInstall bool `json:"can_install"`
		}
		decodeJSONBody(t, rec, &body)
		if !body.Needed || body.CanInstall {
			t.Errorf("body = %+v, want needed=true, can_install=false", body)
		}
	})

	t.Run("sandboxed, control socket present: nothing needed", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "deadbeef")
		dir := t.TempDir()
		socket := dir + "/private"
		if err := touchFile(socket); err != nil {
			t.Fatalf("create fake socket file: %v", err)
		}
		withSystemdControlSocketPaths(t, socket, dir+"/system_bus_socket")
		s := &Server{cfg: &config.Config{}}

		rec := httptest.NewRecorder()
		s.handleDbusStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/dbus-status", nil))

		var body struct {
			Needed bool `json:"needed"`
		}
		decodeJSONBody(t, rec, &body)
		if body.Needed {
			t.Errorf("body.Needed = true with a control socket present, want false")
		}
	})
}

// TestHandleDbusInstallWSGates covers the guard clauses that return before
// ever spawning apt-get — mirrors TestHandleUpdatesWSGates.
func TestHandleDbusInstallWSGates(t *testing.T) {
	t.Run("refused in fixtures mode", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeFixtures}
		scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		rec := httptest.NewRecorder()
		s.handleDbusInstallWS(rec, httptest.NewRequest(http.MethodGet, "/api/system/dbus-install/ws", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused without apt-get", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		rec := httptest.NewRecorder()
		s.handleDbusInstallWS(rec, httptest.NewRequest(http.MethodGet, "/api/system/dbus-install/ws", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused when D-Bus is already reachable", func(t *testing.T) {
		root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(root), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		dir := t.TempDir()
		socket := dir + "/private"
		if err := touchFile(socket); err != nil {
			t.Fatalf("create fake socket file: %v", err)
		}
		withSystemdControlSocketPaths(t, socket, dir+"/system_bus_socket")

		rec := httptest.NewRecorder()
		s.handleDbusInstallWS(rec, httptest.NewRequest(http.MethodGet, "/api/system/dbus-install/ws", nil))

		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
		}
	})

	t.Run("refused when nsenter fallback isn't usable either", func(t *testing.T) {
		root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(root), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		withSystemdControlSocketPaths(t, t.TempDir()+"/private", t.TempDir()+"/system_bus_socket")
		t.Setenv("INVOCATION_ID", "") // no unit context: needsNsenterFallback() is false

		rec := httptest.NewRecorder()
		s.handleDbusInstallWS(rec, httptest.NewRequest(http.MethodGet, "/api/system/dbus-install/ws", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}
