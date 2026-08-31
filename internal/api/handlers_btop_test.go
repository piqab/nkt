package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/althq/netknownsthat/internal/config"
)

// TestHandleBtopWSGates mirrors TestHandleTerminalWSGates: btop rides the
// exact same NKT_TERMINAL_ENABLED/ModeFixtures gates as the plain shell and
// tmux, since it's the same class of action (a real process on the host
// over the same PTY channel), not a separate opt-in.
func TestHandleBtopWSGates(t *testing.T) {
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
			req := httptest.NewRequest(http.MethodGet, "/api/terminal/btop/ws", nil)
			rec := httptest.NewRecorder()

			s.handleBtopWS(rec, req)

			if rec.Code != c.wantCode {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, c.wantCode, rec.Body.String())
			}
		})
	}
}

func TestHandleBtopInstallWSRefusedInFixturesMode(t *testing.T) {
	s := newTestServer(t, &config.Config{Mode: config.ModeFixtures})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system/btop-install/ws", nil)
	s.handleBtopInstallWS(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}
