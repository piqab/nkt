package api

import (
	"bufio"
	"context"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
)

// handleLogSources lists what can be watched: the journal of each service
// this host is known to run, plus the plain files under control.LogRoot.
func (s *Server) handleLogSources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": s.logs.ListSources(),
		"root":    control.LogRoot,
	})
}

// logSourceFromQuery reads the source out of ?unit= or ?path=.
func logSourceFromQuery(r *http.Request) (control.LogSource, error) {
	unit := strings.TrimSpace(r.URL.Query().Get("unit"))
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	switch {
	case unit != "" && path != "":
		return control.LogSource{}, msgs.Errorf("api.specifyEitherUnitPathBoth")
	case unit != "":
		return control.LogSource{Kind: control.LogKindUnit, Name: unit}, nil
	case path != "":
		return control.LogSource{Kind: control.LogKindFile, Name: path}, nil
	default:
		return control.LogSource{}, msgs.Errorf("api.neitherUnitPathSpecified")
	}
}

// handleLogSnapshot returns the last N lines without following — for
// clients that cannot hold a socket open, and as the thing to fall back on
// when the stream drops.
func (s *Server) handleLogSnapshot(w http.ResponseWriter, r *http.Request) {
	source, err := logSourceFromQuery(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	out, err := s.logs.Snapshot(r.Context(), source, intParam(r, "lines", 500))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": out})
}

// logBatchInterval is how long lines accumulate before being sent.
//
// One frame per line is wasteful when a log dumps thousands at once (which
// is exactly what the first screenful of `tail -n 500` is), and batching
// without a deadline would leave a quiet log showing nothing. A tenth of a
// second is below what reads as lag while still collapsing a burst into a
// handful of frames.
const logBatchInterval = 100 * time.Millisecond

// handleLogStream follows a log over a WebSocket.
//
// Text frames of whole lines, not the binary byte stream the terminal uses
// (see pty_session.go): a log is line-oriented, the client filters and
// highlights per line, and handing it half a line to reassemble would make
// every client reimplement the same buffering. No PTY either — a pseudo
// terminal would wrap long lines at some arbitrary width and colour them
// for a screen that does not exist here.
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	source, err := logSourceFromQuery(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	argv, err := s.logs.StreamArgv(source, intParam(r, "lines", 500))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the response.
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	startWSKeepalive(ctx, cancel, conn, wsKeepalivePingInterval, wsKeepalivePingTimeout, wsKeepaliveMaxMissed)

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	// journalctl reports "no entries" and permission problems on stderr,
	// and those are the messages an operator most needs to see — forwarded
	// rather than discarded.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		conn.Close(websocket.StatusInternalError, msgs.Tc(r.Context(), "api.startFailed", err))
		return
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			_ = conn.Write(ctx, websocket.MessageText, scanner.Bytes())
		}
	}()

	user := auth.Username(r.Context())
	s.db.Audit(r.Context(), user, "logs.follow", source.Name, "ok",
		map[string]any{"kind": source.Kind})

	// Reading is stopped by killing the process (a blocking Read is not
	// context-aware), so a client going away ends the command too.
	go func() {
		<-ctx.Done()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	lines := make(chan []byte, 1024)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		// A single log line can be very long (a stack trace on one line, a
		// dumped request body); the default 64KiB limit would silently
		// truncate the stream at the first one.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			chunk := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()

	ticker := time.NewTicker(logBatchInterval)
	defer ticker.Stop()
	var batch [][]byte

	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		payload := strings.Join(bytesToStrings(batch), "\n")
		batch = batch[:0]
		if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			return false
		}
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				flush()
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			batch = append(batch, line)
			// Do not sit on a large burst waiting for the tick.
			if len(batch) >= 500 && !flush() {
				return
			}
		case <-ticker.C:
			if !flush() {
				return
			}
		}
	}
}

func bytesToStrings(in [][]byte) []string {
	out := make([]string, len(in))
	for i, b := range in {
		out[i] = string(b)
	}
	return out
}
