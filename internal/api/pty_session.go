package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/althq/netknownsthat/internal/auth"
)

// ptyControl is the shape of a WebSocket TEXT frame — raw keystrokes and
// process output travel as BINARY frames instead, so a text/binary split
// (rather than an envelope around every byte) is enough to tell the one
// control message (resize) apart from actual terminal traffic.
type ptyControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// runPTYSession accepts a WebSocket upgrade, starts cmd under a PTY and
// bridges the two until either side closes — shared by handleTerminalWS (an
// arbitrary login shell) and handleUpdatesWS (a fixed apt-get command).
// Both need the same framing, idle timeout, audit-log shape and cleanup;
// only what gets exec'd and what it's logged as differ, which is exactly
// what the caller already decided before reaching here.
//
// auditTarget is what shows up in the audit log next to
// auditAction+".start"/".end" (e.g. the shell path, or "apt-get upgrade") —
// callers must have already applied whatever gating is specific to them
// (NKT_TERMINAL_ENABLED, ModeFixtures, apt-get present) before calling
// this; it does not repeat those checks.
func (s *Server) runPTYSession(w http.ResponseWriter, r *http.Request, cmd *exec.Cmd, auditAction, auditTarget string, idleTimeout time.Duration) {
	// Accept's default (no AcceptOptions) already rejects cross-origin
	// upgrade requests — exactly what a feature this powerful needs, with
	// nothing further to configure.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the response.
	}
	defer conn.CloseNow()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "не удалось запустить команду: "+err.Error())
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
	s.db.Audit(r.Context(), user, auditAction+".start", auditTarget, "ok", nil)
	defer func() {
		// context.Background(), not r.Context(): the request context is
		// already on its way out by the time this runs, and the audit
		// entry for a session ending must not be lost just because the
		// HTTP request that started it is too.
		s.db.Audit(context.Background(), user, auditAction+".end", auditTarget, "ok",
			map[string]any{"duration_s": int(time.Since(started).Seconds())})
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Idle timeout: the safety net for a tab left open and forgotten — the
	// process otherwise runs until someone closes it by hand. Any traffic
	// in either direction resets it.
	var resetIdle func()
	if idleTimeout > 0 {
		timer := time.AfterFunc(idleTimeout, cancel)
		resetIdle = func() { timer.Reset(idleTimeout) }
	} else {
		resetIdle = func() {}
	}

	// ptmx -> WebSocket: process output. Exits once ptmx is closed by the
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
			var ctrl ptyControl
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(ctrl.Cols), Rows: uint16(ctrl.Rows)})
			}
		}
	}
}
