package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestStartWSKeepalivePingsUntilCtxDone is the regression test for
// "терминал иногда обрывает сессию" — an idle terminal/apt session with no
// application traffic used to send nothing at all over the WebSocket for
// arbitrarily long stretches, which is exactly what an intermediate reverse
// proxy's idle-connection timeout (nginx's default proxy_read_timeout: 60s)
// or a NAT/firewall device reclaims. Confirms startWSKeepalive actually
// pings on the given interval, and stops once ctx is cancelled rather than
// leaking a ticker goroutine for the life of the process.
func TestStartWSKeepalivePingsUntilCtxDone(t *testing.T) {
	const serverCtxLife = 500 * time.Millisecond
	const pingInterval = 50 * time.Millisecond

	var pings atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), serverCtxLife)
		defer cancel()
		startWSKeepalive(ctx, cancel, conn, pingInterval, time.Second, wsKeepaliveMaxMissed)
		// Ping needs a concurrent Read to ever observe the matching pong
		// (see Conn.Ping's own doc comment) — block here reading until ctx
		// ends, exactly like runPTYSession/runUpdateSession's own main
		// loops already do for the real thing.
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	start := time.Now()
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		OnPingReceived: func(_ context.Context, _ []byte) bool {
			pings.Add(1)
			return true // let the library reply with the matching pong
		},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	// The client, like the server above, only actually processes control
	// frames (including the pings this test is counting) while something
	// is calling Read concurrently. Kept alive well past serverCtxLife so
	// the "pings actually stop" check below can observe the quiet period.
	readCtx, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readCancel()
	go func() {
		for {
			if _, _, err := conn.Read(readCtx); err != nil {
				return
			}
		}
	}()

	// serverCtxLife / pingInterval = 10 ticks expected before the server
	// stops — 3 is a conservative floor that tolerates real scheduling
	// jitter without the assertion being trivially satisfied by a single
	// stray ping.
	deadline := start.Add(serverCtxLife)
	for pings.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(pingInterval / 2)
	}
	if got := pings.Load(); got < 3 {
		t.Fatalf("received %d pings in %v at a %v interval, want at least 3", got, serverCtxLife, pingInterval)
	}

	// Let the server's ctx actually expire (with margin), then confirm
	// pings stop instead of the ticker goroutine running forever
	// regardless of ctx — sampled twice, half a second apart, well after
	// serverCtxLife has elapsed.
	time.Sleep(time.Until(start.Add(serverCtxLife + 300*time.Millisecond)))
	afterStop := pings.Load()
	time.Sleep(500 * time.Millisecond)
	if got := pings.Load(); got != afterStop {
		t.Errorf("pings kept arriving after the server ctx should have ended (%d -> %d)", afterStop, got)
	}
}

// TestStartWSKeepaliveCancelsAfterConsecutiveMissedPongs is the regression
// test for the bug behind "терминал зависает, помогает только полное
// закрытие": a connection black-holed by a NAT device or proxy (packets
// simply stop being delivered, with neither a FIN nor a RST) used to leave
// the session looking alive indefinitely, because conn.Ping(ctx) had no
// deadline of its own and only gave up once the same long-lived session
// ctx that resetIdle governs eventually ended. Simulates a permanent black
// hole by having the client suppress every pong reply, then asserts cancel
// fires (ctx.Done() closes) within a handful of ping intervals, not after
// ctx's full lifetime.
func TestStartWSKeepaliveCancelsAfterConsecutiveMissedPongs(t *testing.T) {
	const pingInterval = 50 * time.Millisecond
	const pingTimeout = 100 * time.Millisecond
	const maxMissed = 3
	const serverCtxLife = 10 * time.Second // must never be reached by this test

	cancelled := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), serverCtxLife)
		defer cancel()
		startWSKeepalive(ctx, cancel, conn, pingInterval, pingTimeout, maxMissed)
		go func() {
			<-ctx.Done()
			close(cancelled)
		}()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	start := time.Now()
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		OnPingReceived: func(_ context.Context, _ []byte) bool {
			return false // swallow it — never reply, as a black-holed connection would
		},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()
	go func() {
		for {
			if _, _, err := conn.Read(readCtx); err != nil {
				return
			}
		}
	}()

	select {
	case <-cancelled:
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("cancel took %v — should fire within roughly maxMissed*(pingInterval+pingTimeout), not sit until serverCtxLife", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("session ctx was never cancelled after pings went unanswered")
	}
}

// TestStartWSKeepaliveToleratesTransientMissedPongs is the regression test
// for "если окно терминала не активно какое-то время, терминал зависает" —
// the very first version of the missed-pong fix above cancelled the
// session on a SINGLE missed pong, which false-positived on a browser tab
// that had merely lost focus: OS/browser power-saving deprioritises a
// backgrounded tab's networking for a while, delaying its pong well past
// one ping's timeout even though the connection is perfectly fine.
// Simulates that transient stall (a few consecutive missed pongs, fewer
// than maxMissed) followed by normal replies resuming, and asserts the
// session survives — cancel must never fire.
func TestStartWSKeepaliveToleratesTransientMissedPongs(t *testing.T) {
	const pingInterval = 100 * time.Millisecond
	const pingTimeout = 300 * time.Millisecond
	const maxMissed = 3
	// Deliberately no deadline on the server session ctx (context.WithCancel,
	// not WithTimeout) — production code's own session ctx only ends via an
	// explicit cancel (idle timeout or this keepalive), never its own
	// built-in deadline; giving this test ctx a deadline would make
	// ctx.Done() fire from the deadline itself, independent of whatever the
	// keepalive logic under test actually does, and falsely look like a
	// cancellation caused by missed pongs.
	const observeFor = 2 * time.Second

	var pings atomic.Int32
	cancelled := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		startWSKeepalive(ctx, cancel, conn, pingInterval, pingTimeout, maxMissed)
		go func() {
			<-ctx.Done()
			close(cancelled)
		}()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		OnPingReceived: func(_ context.Context, _ []byte) bool {
			n := pings.Add(1)
			return n > maxMissed-1 // swallow the first maxMissed-1 pongs, then resume replying
		},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	readCtx, readCancel := context.WithTimeout(context.Background(), observeFor+time.Second)
	defer readCancel()
	go func() {
		for {
			if _, _, err := conn.Read(readCtx); err != nil {
				return
			}
		}
	}()

	select {
	case <-cancelled:
		t.Fatal("session was cancelled after only a transient run of missed pongs, below maxMissed")
	case <-time.After(observeFor):
		// Outlasted the transient stall window without cancel firing — the
		// dip below maxMissed did not trip the liveness check.
	}
}
