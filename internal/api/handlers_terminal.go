package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/config"
)

// terminalControl is the shape of a WebSocket TEXT frame — raw keystrokes
// and shell output travel as BINARY frames instead, so a text/binary split
// (rather than an envelope around every byte) is enough to tell the one
// control message (resize) apart from actual terminal traffic.
type terminalControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

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

	// Accept's default (no AcceptOptions) already rejects cross-origin
	// upgrade requests — exactly what a feature this powerful needs, with
	// nothing further to configure.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the response.
	}
	defer conn.CloseNow()

	shell := loginShell()
	cmd := exec.Command(shell, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "не удалось запустить шелл: "+err.Error())
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	user := auth.Username(r.Context())
	started := time.Now()
	s.db.Audit(r.Context(), user, "terminal.start", shell, "ok", nil)
	defer func() {
		// context.Background(), not r.Context(): the request context is
		// already on its way out by the time this runs, and the audit
		// entry for a session ending must not be lost just because the
		// HTTP request that started it is too.
		s.db.Audit(context.Background(), user, "terminal.end", shell, "ok",
			map[string]any{"duration_s": int(time.Since(started).Seconds())})
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Idle timeout: the safety net for a tab left open and forgotten — the
	// shell process otherwise runs until someone closes it by hand. Any
	// traffic in either direction resets it.
	var resetIdle func()
	if idle := s.cfg.TerminalIdleTimeout; idle > 0 {
		timer := time.AfterFunc(idle, cancel)
		resetIdle = func() { timer.Reset(idle) }
	} else {
		resetIdle = func() {}
	}

	// ptmx -> WebSocket: shell output. Exits once ptmx is closed by the
	// cleanup above (a blocking os-level Read is not context-aware, so
	// closing the file descriptor — not cancelling ctx — is what actually
	// unblocks it when the session ends from the other side).
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				resetIdle()
				if wErr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); wErr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				cancel()
				return
			}
		}
	}()

	// WebSocket -> ptmx: keystrokes (binary frames) and control messages
	// (text frames, currently just resize).
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		resetIdle()
		switch msgType {
		case websocket.MessageBinary:
			_, _ = ptmx.Write(data)
		case websocket.MessageText:
			var ctrl terminalControl
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(ctrl.Cols), Rows: uint16(ctrl.Rows)})
			}
		}
	}
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
