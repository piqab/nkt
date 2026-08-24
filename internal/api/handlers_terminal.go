package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

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
	// the session first if it doesn't exist yet, through a quiet,
	// non-interactive tmux invocation — deliberately NOT `tmux new-session
	// -A` run directly as this interactive command: the very first time a
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

// tmuxControlTimeout bounds the quiet, non-interactive tmux calls
// ensureTmuxSession makes below — local IPC to the tmux server's own
// socket, not a network call, so this is a backstop against a wedged tmux
// server, not a realistic budget.
const tmuxControlTimeout = 5 * time.Second

// runTmux runs `tmux <args...>` non-interactively, respecting the same
// TerminalUser privilege drop the interactive session itself uses — tmux's
// default socket lives under a per-uid path, so reaching the session
// ensureTmuxSession is about to attach-session into means running as the
// exact same user, not just "some user with tmux on PATH".
func (s *Server) runTmux(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tmuxControlTimeout)
	defer cancel()

	argv := append([]string{"tmux"}, args...)
	var cmd *exec.Cmd
	if s.cfg.TerminalUser != "" {
		var err error
		cmd, err = unrestrictedQuietCommandAsUser(ctx, nil, s.cfg.TerminalUser, argv...)
		if err != nil {
			return "", err
		}
	} else {
		cmd = unrestrictedQuietCommand(ctx, nil, argv...)
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ensureTmuxSession makes sure tmuxSessionName's server+session already
// exist before handleTerminalWS opens an interactive PTY attached to it —
// see its own doc comment for why the session-creating fork must not
// happen inside that interactive, --pty-forwarded escape-hatch invocation.
// has-session first (rather than unconditionally creating and ignoring a
// "duplicate session" error) keeps the common case — reattaching to an
// already-running session — down to one quick, side-effect-free check.
func (s *Server) ensureTmuxSession(ctx context.Context) error {
	if _, err := s.runTmux(ctx, "has-session", "-t", tmuxSessionName); err == nil {
		return nil
	}
	if _, err := s.runTmux(ctx, "new-session", "-d", "-s", tmuxSessionName); err != nil {
		// Two tabs opening "Открыть в tmux" at nearly the same instant can
		// both reach here right after has-session fails for both — the
		// loser's `new-session` then fails with tmux's own "duplicate
		// session" error, which is success from this caller's point of
		// view too (the session exists, just not because of this call).
		if _, hasErr := s.runTmux(ctx, "has-session", "-t", tmuxSessionName); hasErr == nil {
			return nil
		}
		return err
	}
	return nil
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
