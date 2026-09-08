package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
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
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.disabled"))
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
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
			writeError(w, http.StatusInternalServerError, msgs.T(msgs.LangFromRequest(r), "terminal.tmuxStartFailed", err.Error()))
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

// handleTerminalConfig reports this host's own TerminalIdleTimeout so the
// frontend can show a countdown to the same disconnect runPTYSession/
// runUpdateSession actually enforce (see acceptWS's/resetIdle's own
// comments) — not gated on TerminalEnabled/ModeFixtures like
// handleTerminalWS, since PtyToolbar (and so this value) is shared by every
// WS session idleTimeout applies to, including the update/install ones
// that work regardless of whether the interactive shell itself is enabled.
// idle_timeout_s of 0 means disabled (see runPTYSession's own idleTimeout>0
// check) — the frontend hides the countdown rather than showing "0:00".
func (s *Server) handleTerminalConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"idle_timeout_s": int(s.cfg.TerminalIdleTimeout.Seconds()),
	})
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
	s.configureTmuxSession(ctx)
	return nil
}

// tmuxHistoryLimit is the scrollback kept per pane in the nkt session.
// tmux's own default is 2000 lines — enough to lose the start of a build
// log or an apt run, which is exactly what someone scrolls back for. Ten
// thousand lines of a terminal's width is single-digit megabytes per pane,
// paid only while the session is alive.
const tmuxHistoryLimit = "10000"

// configureTmuxSession applies nkt's own defaults to the session
// ensureTmuxSession has just created. Every call is best-effort: a tmux too
// old for one of these options must still yield a working terminal, just
// without that comfort.
//
// Only ever called right after creating the session, never on attach, so an
// operator's later change (the toolbar's mouse toggle, or anything typed
// inside the session) is not silently reverted on the next reconnect.
func (s *Server) configureTmuxSession(ctx context.Context) {
	// Without this tmux ignores the wheel entirely, and since it runs on the
	// alternate screen the terminal has no scrollback of its own to fall back
	// on — the wheel does nothing at all, which is exactly what it looked
	// like. Scoped with -t: the operator's own sessions keep their settings.
	_, _ = s.runTmux(ctx, "set-option", "-t", tmuxSessionName, "mouse", "on")

	// set-clipboard is a *server* option in tmux, so unlike the two beside it
	// this one does reach the operator's other sessions on the same tmux
	// server. It is set anyway because it is what makes copying inside tmux
	// reach the real clipboard: with "external" (tmux's default) tmux only
	// passes through OSC 52 written by programs running inside it and never
	// emits its own, so a copy-mode selection went to a tmux buffer and no
	// further. The terminal on the other end already turns OSC 52 into a
	// clipboard write (see usePty's own handler); this is the half that was
	// missing.
	_, _ = s.runTmux(ctx, "set-option", "-s", "set-clipboard", "on")

	// history-limit only applies to windows created *after* it is set — the
	// session's own first window was already created with tmux's 2000-line
	// default and no later set-option changes it (verified on tmux 3.5a).
	// Since nothing has run in that window yet, the fix is to open a second
	// one under the new limit and drop the original: identified by window id
	// rather than index 0, because a ~/.tmux.conf with base-index 1 would
	// make that index wrong.
	if _, err := s.runTmux(ctx, "set-option", "-t", tmuxSessionName, "history-limit", tmuxHistoryLimit); err != nil {
		return
	}
	firstWindow, err := s.runTmux(ctx, "list-windows", "-t", tmuxSessionName, "-F", "#{window_id}")
	if err != nil || strings.Contains(firstWindow, "\n") {
		// More than one window means this is not the fresh session this
		// function is documented to run on — leave it alone.
		return
	}
	if _, err := s.runTmux(ctx, "new-window", "-t", tmuxSessionName); err != nil {
		return
	}
	// Only now that the replacement exists is killing the original safe:
	// killing the last window would take the session with it.
	if _, err := s.runTmux(ctx, "kill-window", "-t", firstWindow); err != nil {
		return
	}
	// Renumber so the surviving window is 0 again rather than 1 — cosmetic,
	// but the window index is what Ctrl+B 0…9 selects.
	_, _ = s.runTmux(ctx, "move-window", "-r", "-t", tmuxSessionName)
}

// tmuxMouseState reports whether the nkt session currently has mouse mode
// on. Read from tmux itself rather than remembered here: the session
// outlives this process (that is the point of tmux mode), and the operator
// can change the option from inside the session at any time.
func (s *Server) tmuxMouseState(ctx context.Context) (bool, error) {
	out, err := s.runTmux(ctx, "show-options", "-t", tmuxSessionName, "mouse")
	if err != nil {
		return false, err
	}
	return parseTmuxMouseOption(out), nil
}

// parseTmuxMouseOption reads tmux's `show-options mouse` output: "mouse on"
// or "mouse off", and nothing at all when the option was never set. A plain
// HasSuffix(out, "on") would be wrong the moment tmux prints anything else
// after the value, so the value is taken as its own field.
func parseTmuxMouseOption(out string) bool {
	fields := strings.Fields(out)
	return len(fields) >= 2 && fields[1] == "on"
}

// handleTmuxMouse reads or flips mouse mode on the nkt tmux session — the
// toolbar's own toggle. With mouse on the wheel scrolls tmux's history; with
// it off the mouse belongs to the browser again, so text selection works
// without holding Shift. Both are legitimate preferences, hence a switch
// rather than a hardcoded choice.
func (s *Server) handleTmuxMouse(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		on, err := s.tmuxMouseState(r.Context())
		if err != nil {
			// No session yet is not an error worth failing on — the toggle
			// simply has nothing to act on until one is opened.
			writeJSON(w, http.StatusOK, map[string]any{"session": false, "mouse": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session": true, "mouse": on})
		return
	}

	var req struct {
		Mouse bool `json:"mouse"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	value := "off"
	if req.Mouse {
		value = "on"
	}
	if _, err := s.runTmux(r.Context(), "set-option", "-t", tmuxSessionName, "mouse", value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": true, "mouse": req.Mouse})
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
