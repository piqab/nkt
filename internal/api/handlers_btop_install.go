package api

import (
	"net/http"
	"os/exec"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/msgs"
)

// handleBtopStatus reports whether btop is on this host's PATH — the Usage
// page uses this to decide whether "Открыть btop" can just open a session
// directly or first needs to offer the install dialog.
func (s *Server) handleBtopStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"available": collect.Which(r.Context(), s.scanner.Collector(), "btop"),
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
