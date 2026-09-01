package api

import (
	"net/http"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
)

// handleBtopWS opens a live, fully interactive btop session over the same
// PTY/WebSocket bridge handleTerminalWS uses for the plain shell and tmux —
// gated identically (NKT_TERMINAL_ENABLED, refused outright in
// ModeFixtures, admin+mutations via the route's own RequireAuth+
// RequireAdmin group): running btop is the same class of action as opening
// a shell, a real process on the host under the operator's control, even
// though btop itself only ever reads /proc rather than mutating anything.
func (s *Server) handleBtopWS(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TerminalEnabled {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.disabled"))
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return
	}

	env := map[string]string{"TERM": "xterm-256color"}
	argv := []string{"btop"}

	// Same TerminalUser privilege drop handleTerminalWS applies — see its
	// own doc comment. btop reading another account's /proc entries isn't
	// itself dangerous, but running as whatever account the operator's
	// shell would run as is the least surprising choice, and keeps this
	// PTY channel's privilege model uniform regardless of which program is
	// attached to it.
	if s.cfg.TerminalUser != "" {
		cmd, err := unrestrictedCommandAsUser(env, s.cfg.TerminalUser, argv...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.runPTYSession(w, r, cmd, "btop", "btop", s.cfg.TerminalIdleTimeout)
		return
	}

	cmd := unrestrictedCommand(env, argv...)
	s.runPTYSession(w, r, cmd, "btop", "btop", s.cfg.TerminalIdleTimeout)
}
