package api

import (
	"net/http"
	"os"
	"os/exec"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
)

// handleUpdatesWS runs `apt-get update && apt-get upgrade` on this host —
// deliberately without -y, streamed live over the same PTY/WebSocket
// bridge handleTerminalWS uses, so apt's own "Do you want to continue?
// [Y/n]" (and anything else it needs to ask, e.g. which services to
// restart after a libssl update) is answered by the operator watching in
// real time, not decided unattended. Unlike the general terminal this
// spawns one fixed command, not an arbitrary shell, so it does not require
// NKT_TERMINAL_ENABLED — it is still behind RequireAuth+RequireAdmin (admin
// role, AllowMutations on) like any other mutation, plus ModeFixtures is
// refused for the same reason the terminal refuses it: a demo instance
// must never run a real package upgrade on whatever machine hosts it.
func (s *Server) handleUpdatesWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, "обновление пакетов недоступно в режиме fixtures")
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, "apt-get не найден — обновление пакетов поддерживается только на Debian/Ubuntu")
		return
	}

	cmd := exec.Command("bash", "-c", "apt-get update && apt-get upgrade")
	// DEBIAN_FRONTEND intentionally left at its default (not "noninteractive"):
	// the whole point is a real prompt the operator answers themselves.
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	s.runPTYSession(w, r, cmd, "packages.upgrade", "apt-get upgrade", s.cfg.TerminalIdleTimeout)
}
