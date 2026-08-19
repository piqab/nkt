package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
)

// TestHandleUFWInstallWSGates mirrors TestHandleUpdatesWSGates: ModeFixtures
// is refused outright, a host without apt-get is refused with an actionable
// message, and — the one guard specific to this handler — a host that
// already has ufw is refused too, so a stale "не установлен" banner on the
// frontend can never trigger a pointless (if harmless) second apt-get run.
func TestHandleUFWInstallWSGates(t *testing.T) {
	t.Run("refused in fixtures mode", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeFixtures}
		scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		req := httptest.NewRequest(http.MethodGet, "/api/firewall/ufw-install/ws", nil)
		rec := httptest.NewRecorder()
		s.handleUFWInstallWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused without apt-get", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		req := httptest.NewRequest(http.MethodGet, "/api/firewall/ufw-install/ws", nil)
		rec := httptest.NewRecorder()
		s.handleUFWInstallWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused when ufw is already installed", func(t *testing.T) {
		// The real fixtures tree stubs both "command -v apt-get" and
		// "command -v ufw" — exactly what a host that doesn't need this
		// action at all looks like.
		root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(root), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		req := httptest.NewRequest(http.MethodGet, "/api/firewall/ufw-install/ws", nil)
		rec := httptest.NewRecorder()
		s.handleUFWInstallWS(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
		}
	})
}

// TestHandleUFWInstallStatus mirrors TestHandleUpdatesStatus for the
// "ufw-install" session key, keyed independently of the "packages" one so
// the two can never be confused for each other.
func TestHandleUFWInstallStatus(t *testing.T) {
	s := &Server{sessions: map[string]*updateSession{}}

	req := httptest.NewRequest(http.MethodGet, "/api/firewall/ufw-install/status", nil)
	rec := httptest.NewRecorder()
	s.handleUFWInstallStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"active":false`) || !strings.Contains(rec.Body.String(), `"finished":false`) {
		t.Errorf("body = %s, want active/finished both false with no session", rec.Body.String())
	}
}

// TestHandleFirewalldInstallWSGates is TestHandleUFWInstallWSGates's twin —
// same three guards, "firewall-cmd" in place of "ufw".
func TestHandleFirewalldInstallWSGates(t *testing.T) {
	t.Run("refused in fixtures mode", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeFixtures}
		scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		req := httptest.NewRequest(http.MethodGet, "/api/firewall/firewalld-install/ws", nil)
		rec := httptest.NewRecorder()
		s.handleFirewalldInstallWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused without apt-get", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		req := httptest.NewRequest(http.MethodGet, "/api/firewall/firewalld-install/ws", nil)
		rec := httptest.NewRecorder()
		s.handleFirewalldInstallWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused when firewalld is already installed", func(t *testing.T) {
		root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(root), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		req := httptest.NewRequest(http.MethodGet, "/api/firewall/firewalld-install/ws", nil)
		rec := httptest.NewRecorder()
		s.handleFirewalldInstallWS(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
		}
	})
}

func TestHandleFirewalldInstallStatus(t *testing.T) {
	s := &Server{sessions: map[string]*updateSession{}}

	req := httptest.NewRequest(http.MethodGet, "/api/firewall/firewalld-install/status", nil)
	rec := httptest.NewRecorder()
	s.handleFirewalldInstallStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"active":false`) || !strings.Contains(rec.Body.String(), `"finished":false`) {
		t.Errorf("body = %s, want active/finished both false with no session", rec.Body.String())
	}
}
