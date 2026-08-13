package api

import (
	"net/http"
	"os"
	"os/exec"

	"github.com/althq/netknownsthat/internal/config"
)

// handleTerminalWS opens a real login shell on this host and streams it
// over a WebSocket — direct interactive shell access, not a bounded
// action. Reached through the same RequireAuth+RequireAdmin chain as every
// other mutating endpoint (admin role, AllowMutations on), plus two more
// gates specific to how much more this one thing can do: NKT_TERMINAL_ENABLED
// must be explicitly turned on, and it is refused outright in ModeFixtures
// no matter how that flag is set — a demo/fixtures instance must never be
// able to spawn a real shell on whatever machine happens to be running it.
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TerminalEnabled {
		writeError(w, http.StatusForbidden, "веб-терминал выключен: задайте NKT_TERMINAL_ENABLED=true")
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, "веб-терминал недоступен в режиме fixtures")
		return
	}

	shell := loginShell()
	cmd := unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, shell, "-l")
	s.runPTYSession(w, r, cmd, "terminal", shell, s.cfg.TerminalIdleTimeout)
}

// loginShell picks the shell handleTerminalWS runs: $SHELL if the
// environment sets one (the norm under systemd's own default environment
// and most login setups), else bash if it exists, else the POSIX baseline.
func loginShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	return "/bin/sh"
}
