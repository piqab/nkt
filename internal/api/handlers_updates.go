package api

import (
	"net/http"
	"os/exec"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
)

// handleUpdatesWS runs `apt-get update && apt-get upgrade` on this host —
// deliberately without -y, streamed live over a PTY/WebSocket bridge, so
// apt's own "Do you want to continue? [Y/n]" (and anything else it needs
// to ask, e.g. which services to restart after a libssl update) is
// answered by the operator watching in real time, not decided unattended.
// Unlike the general terminal this spawns one fixed command, not an
// arbitrary shell, so it does not require NKT_TERMINAL_ENABLED — it is
// still behind RequireAuth+RequireAdmin (admin role, AllowMutations on)
// like any other mutation, plus ModeFixtures is refused for the same
// reason the terminal refuses it: a demo instance must never run a real
// package upgrade on whatever machine hosts it.
//
// Unlike handleTerminalWS, this goes through runUpdateSession, not
// runPTYSession: the underlying apt-get process must survive the operator
// closing the dialog (or losing the connection) instead of being killed
// with it — an apt-get upgrade interrupted mid-transaction can leave dpkg
// needing `dpkg --configure -a` to recover, so "обновить" clicked again
// reattaches to whatever is still running instead of starting a second
// process that would just deadlock on apt's own lock file.
func (s *Server) handleUpdatesWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, "обновление пакетов недоступно в режиме fixtures")
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, "apt-get не найден — обновление пакетов поддерживается только на Debian/Ubuntu")
		return
	}

	buildCmd := func() *exec.Cmd {
		// DEBIAN_FRONTEND intentionally left at its default (not
		// "noninteractive"): the whole point is a real prompt the operator
		// answers themselves.
		return unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, "bash", "-c", "apt-get update && apt-get upgrade")
	}
	s.runUpdateSession(w, r, buildCmd, "packages.upgrade", "apt-get upgrade", s.cfg.TerminalIdleTimeout)
}
