package api

import (
	"net/http"
	"os/exec"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
)

// handleUFWInstallWS runs `apt-get install -y ufw` on this host, streamed
// live over a PTY/WebSocket bridge — same runUpdateSession mechanism as
// package updates (handleUpdatesWS), under its own "ufw-install" key so it
// can never collide with an in-progress apt upgrade. -y is safe here
// (unlike the upgrade flow): installing one already-known package from the
// distro's own repos does not carry the "which services get restarted, is
// this really what I want on this system" judgment call a full upgrade
// does, so there is nothing worth pausing for a prompt over.
func (s *Server) handleUFWInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, "установка пакетов недоступна в режиме fixtures")
		return
	}
	c := s.scanner.Collector()
	if !collect.Which(r.Context(), c, "apt-get") {
		writeError(w, http.StatusForbidden, "apt-get не найден — установка пакетов поддерживается только на Debian/Ubuntu")
		return
	}
	if collect.Which(r.Context(), c, "ufw") {
		writeError(w, http.StatusConflict, "ufw уже установлен")
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "apt-get", "install", "-y", "ufw")
	}
	s.runUpdateSession(w, r, "ufw-install", buildCmd, "firewall.install_ufw", "apt-get install ufw", s.cfg.TerminalIdleTimeout)
}

// handleUFWInstallStatus mirrors handleUpdatesStatus for the "ufw-install"
// session key — the Firewall page polls this independently of whether the
// install dialog is open, so a freshly loaded page (or one reopened after a
// disconnect) can show "установка выполняется — открыть" instead of racing
// a second apt-get install against one already running.
func (s *Server) handleUFWInstallStatus(w http.ResponseWriter, r *http.Request) {
	s.sessionsMu.Lock()
	sess := s.sessions["ufw-install"]
	s.sessionsMu.Unlock()

	if sess == nil {
		writeJSON(w, http.StatusOK, map[string]any{"active": false, "finished": false})
		return
	}
	done, exitCode := sess.outcome()
	writeJSON(w, http.StatusOK, map[string]any{
		"active":    !done,
		"finished":  done,
		"exit_code": exitCode,
		"succeeded": done && exitCode == 0,
	})
}
