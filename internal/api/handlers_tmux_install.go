package api

import (
	"net/http"
	"os/exec"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
)

// handleTmuxStatus reports whether tmux is on this host's PATH — the
// Terminal page uses this to decide whether "Открыть в tmux" can just open
// a session directly or first needs to offer the install dialog.
func (s *Server) handleTmuxStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"available": collect.Which(r.Context(), s.scanner.Collector(), "tmux"),
	})
}

// handleTmuxInstallWS runs `apt-get install -y tmux`, streamed live — same
// runUpdateSession mechanism as the dbus/ufw/firewalld install buttons.
// Stays on unrestrictedCommand (root), not the ssh_user privilege drop
// handleTerminalWS itself now uses: installing a package needs root to
// write into the host's package database regardless of who ends up running
// tmux afterwards.
func (s *Server) handleTmuxInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	if collect.Which(r.Context(), s.scanner.Collector(), "tmux") {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "pkgInstall.tmuxAlreadyInstalled"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get update && apt-get install -y tmux")
	}
	s.runUpdateSession(w, r, "tmux-install", buildCmd, "system.install_tmux", "apt-get install tmux", s.cfg.TerminalIdleTimeout)
}

// handleTmuxInstallStatus mirrors handleDbusInstallStatus for the
// "tmux-install" session key.
func (s *Server) handleTmuxInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("tmux-install")
	writeSessionStatus(w, active, finished, exitCode)
}
