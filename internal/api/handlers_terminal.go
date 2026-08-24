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
	env := map[string]string{"TERM": "xterm-256color"}

	// TerminalUser (NKT_TERMINAL_USER, written by the hub at install time —
	// see config.Config's own doc comment) is the ssh_user configured for
	// *this* host, which has nothing to do with the root this daemon
	// itself runs as. Empty means no such account is configured — a plain
	// standalone nkt with no hub, where the distinction does not apply —
	// and the shell keeps running as root exactly as it always has.
	if s.cfg.TerminalUser != "" {
		cmd, err := unrestrictedCommandAsUser(env, s.cfg.TerminalUser, shell, "-l")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.runPTYSession(w, r, cmd, "terminal", shell, s.cfg.TerminalIdleTimeout)
		return
	}

	cmd := unrestrictedCommand(env, shell, "-l")
	s.runPTYSession(w, r, cmd, "terminal", shell, s.cfg.TerminalIdleTimeout)
}

// loginShell picks the shell handleTerminalWS runs: bash if it exists —
// the predictable, consistent choice regardless of what $SHELL happens to
// be set to in nkt's own process environment (often unset entirely under
// systemd, but not guaranteed, and not necessarily what the operator
// opening this terminal would expect anyway) — else $SHELL if the
// environment sets one, else the POSIX baseline.
func loginShell() string {
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}
