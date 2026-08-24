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
	// ?tmux=1 (the "Открыть в tmux" button) attaches to tmuxSessionName
	// instead of opening a bare login shell — matching the "resilient
	// session" behaviour package-update sessions already have via
	// runUpdateSession's own session tracking. ensureTmuxSession creates
	// the session first if it doesn't exist yet, through the same quiet,
	// non-interactive path the tmux control panel uses (see
	// handlers_tmux_control.go) — deliberately NOT `tmux new-session -A`
	// run directly as this interactive command: the very first time a
	// session doesn't exist yet, tmux has to fork and daemonize a whole
	// new server process, and doing that inside a --pty-forwarded,
	// interactively-attached escape-hatch invocation (systemd-run/nsenter)
	// is exactly the kind of fork/detach systemd's own process tracking
	// for that transient unit is not guaranteed to survive cleanly — a
	// plain login shell never forks anything like that, which is why only
	// tmux mode is at risk here. Once the session definitely already
	// exists, all this command has to do is attach-session — a plain
	// connect-to-an-existing-socket operation with none of that risk.
	if r.URL.Query().Get("tmux") == "1" {
		if err := s.ensureTmuxSession(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "не удалось запустить tmux: "+err.Error())
			return
		}
		argv = []string{"tmux", "attach-session", "-t", tmuxSessionName}
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
