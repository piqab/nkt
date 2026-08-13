package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/althq/netknownsthat/internal/store"
)

// TestUpdateSessionSurvivesDisconnect is the regression test for the bug
// runUpdateSession exists to fix: closing the WebSocket (the operator
// closing the dialog, or just losing the connection) used to kill the
// underlying process via runPTYSession's cleanup — actively dangerous for
// an apt-get upgrade interrupted mid-transaction (dpkg can be left needing
// `dpkg --configure -a` to recover). This proves a first connection
// disconnecting mid-run does not stop the process, and a second
// connection made while it's still running attaches to that SAME run
// (replayed output from before it connected, then the live tail), rather
// than starting a second process racing the first.
func TestUpdateSessionSurvivesDisconnect(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := &Server{db: db}

	var runs int
	buildCmd := func() *exec.Cmd {
		runs++
		// middle at ~0.2s, end at ~1.7s — wide enough windows to dial the
		// second connection after "middle" has been buffered but well
		// before "end", so it exercises the still-running attach path,
		// not the already-finished one.
		return exec.Command("bash", "-c", "echo start; sleep 0.2; echo middle; sleep 1.5; echo end")
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.runUpdateSession(w, r, buildCmd, "test.session", "test", 0)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()
	conn1, _, err := websocket.Dial(ctx1, wsURL, nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	got1 := readUntilMarker(t, ctx1, conn1, "start")
	if !strings.Contains(got1, "start") {
		t.Fatalf("first connection never saw \"start\": %q", got1)
	}
	conn1.Close(websocket.StatusNormalClosure, "simulated dialog close")

	if runs != 1 {
		t.Fatalf("runs = %d, want 1 after the first connection", runs)
	}

	// Let the process keep going, unattended, well past "middle" but
	// before "end" — proves it wasn't killed when the first connection
	// closed.
	time.Sleep(600 * time.Millisecond)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	conn2, _, err := websocket.Dial(ctx2, wsURL, nil)
	if err != nil {
		t.Fatalf("second dial: %v", err)
	}
	defer conn2.CloseNow()
	got2 := readUntilMarker(t, ctx2, conn2, "end")
	for _, want := range []string{"start", "middle", "end"} {
		if !strings.Contains(got2, want) {
			t.Errorf("second connection missing %q in %q", want, got2)
		}
	}

	if runs != 1 {
		t.Errorf("runs = %d, want 1 — a second WebSocket connection must reattach, not start a second process", runs)
	}
}

// TestHandleUpdatesStatus locks in the three states the Overview page's
// button relies on to tell the operator whether "обновить" would reattach
// to a real, already-running apt-get or start a brand new one: no session
// yet, a session actively running, and a session that has finished.
func TestHandleUpdatesStatus(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := &Server{db: db}

	active := func(t *testing.T) bool {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/updates/status", nil)
		rec := httptest.NewRecorder()
		s.handleUpdatesStatus(rec, req)
		var body map[string]bool
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
		}
		return body["active"]
	}

	if active(t) {
		t.Error("active = true before any session was ever started")
	}

	sess, err := newUpdateSession(exec.Command("bash", "-c", "sleep 0.3"))
	if err != nil {
		t.Fatalf("newUpdateSession: %v", err)
	}
	s.updateSession = sess

	if !active(t) {
		t.Error("active = false while the session is still running")
	}

	for i := 0; i < 50 && !sess.isDone(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !sess.isDone() {
		t.Fatal("session never finished")
	}

	if active(t) {
		t.Error("active = true after the session finished")
	}
}

func readUntilMarker(t *testing.T, ctx context.Context, conn *websocket.Conn, marker string) string {
	t.Helper()
	var out strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read (collected so far: %q): %v", out.String(), err)
		}
		out.Write(data)
		if strings.Contains(out.String(), marker) {
			return out.String()
		}
	}
}
