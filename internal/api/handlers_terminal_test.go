package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
