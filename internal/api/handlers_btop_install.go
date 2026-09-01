package api

import (
	"net/http"
	"os/exec"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
)

// handleBtopStatus reports whether btop is on this host's PATH, plus every
// other precondition handleBtopWS/handleBtopInstallWS themselves gate on
// (terminal_enabled, fixtures_mode, apt_get_available) — a browser's
// WebSocket API has no way to read the response body of a rejected
// upgrade, so a 403 from either of those handlers is invisible to the
// frontend beyond "it failed somehow". Checking these here, over a plain
// REST call, before ever attempting the WS, is what actually lets the
// Usage page explain *why* instead of just showing a generic connection
// error.
func (s *Server) handleBtopStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"available":         collect.Which(r.Context(), s.scanner.Collector(), "btop"),
		"terminal_enabled":  s.cfg.TerminalEnabled,
		"fixtures_mode":     s.cfg.Mode == config.ModeFixtures,
		"apt_get_available": collect.Which(r.Context(), s.scanner.Collector(), "apt-get"),
	})
}

// handleBtopInstallWS runs `apt-get install -y btop`, streamed live — same
// runUpdateSession mechanism as the tmux/dbus/ufw/firewalld install
// buttons. Stays on unrestrictedCommand (root), not the ssh_user privilege
// drop handleBtopWS itself uses: installing a package needs root regardless
// of who ends up running btop afterwards.
func (s *Server) handleBtopInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	if collect.Which(r.Context(), s.scanner.Collector(), "btop") {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "pkgInstall.btopAlreadyInstalled"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get update && apt-get install -y btop")
	}
	s.runUpdateSession(w, r, "btop-install", buildCmd, "system.install_btop", "apt-get install btop", s.cfg.TerminalIdleTimeout)
}

// handleBtopInstallStatus mirrors handleTmuxInstallStatus for the
// "btop-install" session key.
func (s *Server) handleBtopInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("btop-install")
	writeSessionStatus(w, active, finished, exitCode)
}
