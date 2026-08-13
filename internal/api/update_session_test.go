package api

import (
	"context"
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
