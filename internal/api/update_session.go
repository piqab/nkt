package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/althq/netknownsthat/internal/auth"
)

// maxUpdateSessionBuf bounds how much of a session's output is kept for
// replay to a client that (re)attaches after missing some of it —
// generous for even a large apt-get upgrade's output, small enough to
// never matter for memory.
const maxUpdateSessionBuf = 4 << 20 // 4 MiB

// updateSession is a single apt-get update/upgrade run that outlives any
// one WebSocket connection to it. Closing the browser tab used to kill the
// underlying process outright (runPTYSession kills cmd.Process the moment
// its WebSocket closes) — fine for an interactive shell, actively harmful
// for a package manager transaction: an apt-get upgrade interrupted
// mid-transaction can leave dpkg needing `dpkg --configure -a` to recover.
// A closed dialog (by accident, or on purpose to go check something else)
// must reattach to the same running process on the next "обновить" click,
// not kill it and/or start a second one racing it for apt's own lock file.
type updateSession struct {
	ptmx *os.File
	cmd  *exec.Cmd

	mu   sync.Mutex
	buf  []byte
	done bool
	// exitCode is meaningful only once done — -1 stands for "the process
	// state could not be determined", which must not be mistaken for
	// success by anything deciding what to do after the run.
	exitCode int
	subs     map[chan []byte]struct{}
}

func newUpdateSession(cmd *exec.Cmd) (*updateSession, error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	s := &updateSession{ptmx: ptmx, cmd: cmd, exitCode: -1, subs: map[chan []byte]struct{}{}}
	go s.pump()
	return s, nil
}

// pump is the session's one and only reader of ptmx — a pty master can't
// be read from more than one place at once, so every attached WebSocket
// gets its data relayed from here rather than reading ptmx itself.
func (s *updateSession) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			s.broadcast(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			break
		}
	}
	_ = s.ptmx.Close()
	exitCode := -1
	if s.cmd.Process != nil {
		if state, err := s.cmd.Process.Wait(); err == nil && state != nil {
			exitCode = state.ExitCode()
		}
	}

	s.mu.Lock()
	s.done = true
	s.exitCode = exitCode
	for ch := range s.subs {
		close(ch)
	}
	s.subs = map[chan []byte]struct{}{}
	s.mu.Unlock()
}

func (s *updateSession) broadcast(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, chunk...)
	if len(s.buf) > maxUpdateSessionBuf {
		s.buf = s.buf[len(s.buf)-maxUpdateSessionBuf:]
	}
	for ch := range s.subs {
		select {
		case ch <- chunk:
		default:
			// A slow subscriber must not block the pump (and every other
			// subscriber with it) — it already has everything up to its
			// attach point via the replay buffer, and can always reattach
			// to catch back up to wherever things ended.
		}
	}
}

// attach registers a new subscriber and returns the output so far for
// immediate replay, plus a channel of everything from here on. live is
// false if the session had already finished by the time this was called —
// buf is then the complete output there will ever be, and ch is nil.
func (s *updateSession) attach() (buf []byte, ch chan []byte, live bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf = append([]byte(nil), s.buf...)
	if s.done {
		return buf, nil, false
	}
	ch = make(chan []byte, 64)
	s.subs[ch] = struct{}{}
	return buf, ch, true
}

func (s *updateSession) detach(ch chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, ch)
}

func (s *updateSession) write(p []byte) { _, _ = s.ptmx.Write(p) }

func (s *updateSession) resize(cols, rows uint16) {
	_ = pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

func (s *updateSession) isDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// outcome reports how the run ended: done is false while it is still
// going, and exitCode is only meaningful once it is true (-1 meaning the
// process state could not be read at all).
func (s *updateSession) outcome() (done bool, exitCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done, s.exitCode
}

// runUpdateSession bridges a WebSocket to a process-level session that
// survives the connection itself: buildCmd is only actually called (and a
// new process started) when there is no session yet, or the previous one
// has already finished — otherwise this attaches to whatever is already
// running and replays its output so far, so a client that reconnects
// mid-upgrade sees the same session, not a fresh one racing it.
func (s *Server) runUpdateSession(w http.ResponseWriter, r *http.Request, buildCmd func() *exec.Cmd, auditAction, auditTarget string, idleTimeout time.Duration) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the response.
	}
	defer conn.CloseNow()

	s.updateSessionMu.Lock()
	sess := s.updateSession
	if sess == nil || sess.isDone() {
		newSess, err := newUpdateSession(buildCmd())
		if err != nil {
			s.updateSessionMu.Unlock()
			conn.Close(websocket.StatusInternalError, "не удалось запустить команду: "+err.Error())
			return
		}
		s.updateSession = newSess
		sess = newSess
	}
	s.updateSessionMu.Unlock()

	buf, ch, live := sess.attach()

	user := auth.Username(r.Context())
	s.db.Audit(r.Context(), user, auditAction+".attach", auditTarget, "ok", nil)
	defer func() {
		s.db.Audit(context.Background(), user, auditAction+".detach", auditTarget, "ok", nil)
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if live {
		defer sess.detach(ch)
	}

	if len(buf) > 0 {
		if err := conn.Write(ctx, websocket.MessageBinary, buf); err != nil {
			return
		}
	}
	if !live {
		// The session had already finished before this connection ever
		// attached — the replay above is the whole story, nothing more
		// will ever arrive.
		conn.Close(websocket.StatusNormalClosure, "сессия завершена")
		return
	}

	var resetIdle func()
	if idleTimeout > 0 {
		timer := time.AfterFunc(idleTimeout, cancel)
		resetIdle = func() { timer.Reset(idleTimeout) }
	} else {
		resetIdle = func() {}
	}

	// session -> WebSocket: relayed output. Exits once ch is closed (the
	// session finished) or the connection errors out.
	go func() {
		for {
			select {
			case chunk, ok := <-ch:
				if !ok {
					cancel()
					return
				}
				resetIdle()
				if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// WebSocket -> session: keystrokes (binary frames) and control
	// messages (text frames, currently just resize). Closing this
	// connection only detaches (see the defer above) — it does not touch
	// the process, which is exactly the point.
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		resetIdle()
		switch msgType {
		case websocket.MessageBinary:
			sess.write(data)
		case websocket.MessageText:
			var ctrl ptyControl
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
				sess.resize(uint16(ctrl.Cols), uint16(ctrl.Rows))
			}
		}
	}
}
