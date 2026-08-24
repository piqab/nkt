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
	argv := []string{shell, "-l"}
	auditTarget := shell
	// ?tmux=1 (the "Открыть в tmux" button) runs a tmux session instead of
	// a bare login shell — -A attaches to tmuxSessionName if it already
	// exists (e.g. this same operator reconnecting after a dropped
	// connection, or a session left running on purpose) and creates it
	// otherwise, matching the "resilient session" behaviour package-update
	// sessions already have via runUpdateSession's own session tracking.
	// Left for tmux itself to fail with "command not found" if it isn't
	// installed — the frontend already checks /system/tmux-status and
	// offers to install it first, so reaching here with tmux missing would
	// mean that check was bypassed, not a case worth a nicer message for.
	if r.URL.Query().Get("tmux") == "1" {
		argv = []string{"tmux", "new-session", "-A", "-s", tmuxSessionName}
		auditTarget = "tmux"
	}
	env := map[string]string{"TERM": "xterm-256color"}

	// TerminalUser (NKT_TERMINAL_USER, written by the hub at install time —
	// see config.Config's own doc comment) is the ssh_user configured for
	// *this* host, which has nothing to do with the root this daemon
	// itself runs as. Empty means no such account is configured — a plain
	// standalone nkt with no hub, where the distinction does not apply —
	// and the shell keeps running as root exactly as it always has.
	if s.cfg.TerminalUser != "" {
		cmd, err := unrestrictedCommandAsUser(env, s.cfg.TerminalUser, argv...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.runPTYSession(w, r, cmd, "terminal", auditTarget, s.cfg.TerminalIdleTimeout)
		return
	}

	cmd := unrestrictedCommand(env, argv...)
	s.runPTYSession(w, r, cmd, "terminal", auditTarget, s.cfg.TerminalIdleTimeout)
}

// tmuxSessionName is the fixed tmux session name handleTerminalWS attaches
// to/creates in tmux mode — one shared session per OS account (root, or
// TerminalUser's ssh_user) is what makes reattaching after a dropped
// connection actually useful, rather than every "Открыть в tmux" click
// starting a brand new, unnamed session nobody could get back to.
const tmuxSessionName = "nkt"

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
