package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
)

// TestHandleUpdatesWSGates locks in the two guards handleUpdatesWS checks
// before ever spawning apt-get: ModeFixtures is refused outright (a demo
// instance must never run a real package upgrade on whatever machine hosts
// it), and a host with no apt-get is refused with an actionable message
// rather than a confusing exec failure once the PTY is already running.
func TestHandleUpdatesWSGates(t *testing.T) {
	t.Run("refused in fixtures mode", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeFixtures}
		scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		req := httptest.NewRequest(http.MethodGet, "/api/updates/ws", nil)
		rec := httptest.NewRecorder()
		s.handleUpdatesWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused without apt-get", func(t *testing.T) {
		// ModeLocal so the fixtures-mode gate doesn't fire first — the
		// fixtures dir is empty either way (no "command -v apt-get" stub),
		// which is exactly what a non-Debian host looks like to Which.
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		req := httptest.NewRequest(http.MethodGet, "/api/updates/ws", nil)
		rec := httptest.NewRecorder()
		s.handleUpdatesWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("passes the apt-get gate against the real fixtures tree", func(t *testing.T) {
		// fixtures/host/.commands/index.json stubs "command -v apt-get" —
		// confirms the gate actually recognizes a host that does have it,
		// not just that an empty collector always fails closed.
		root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{Mode: config.ModeLocal}
		scanner := inventory.New(cfg, collect.NewFixtures(root), nil)
		s := &Server{cfg: cfg, scanner: scanner}

		if !collect.Which(t.Context(), s.scanner.Collector(), "apt-get") {
			t.Fatal("expected the fixtures tree's apt-get stub to be found")
		}
	})
}
