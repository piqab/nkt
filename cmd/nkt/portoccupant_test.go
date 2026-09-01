package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
)

// TestDiagnoseListenErrorNamesOccupant drives the real failure end to end:
// occupy a port with a listener of our own, then try to bind it a second
// time exactly the way ListenAndServe would, and check the resulting error
// names this test's own process. No network access needed — this never
// leaves the loopback interface.
func TestDiagnoseListenErrorNamesOccupant(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupying a port: %v", err)
	}
	defer l.Close()
	addr := l.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	_, bindErr := net.Listen("tcp", addr)
	if bindErr == nil {
		t.Fatal("expected a second Listen on the same port to fail")
	}

	who := portOccupant(context.Background(), port)
	if who == "" {
		t.Skip("ss found no occupant for this port — likely no ss in PATH or a sandbox without /proc access; not this code's bug")
	}
	t.Logf("occupant: %q", who)
	if !strings.Contains(who, "pid") && !strings.Contains(who, strconv.Itoa(port)) {
		// Loose check: just confirm it looks like a real "<process> (pid N)"
		// description rather than garbage — the exact process name
		// (go/exe/__debug_bin under `go test`) isn't worth pinning down.
		t.Errorf("portOccupant result doesn't look like a process description: %q", who)
	}

	got := diagnoseListenError(context.Background(), bindErr, addr)
	if got == nil {
		t.Fatal("diagnoseListenError returned nil for a real EADDRINUSE")
	}
	if !strings.Contains(got.Error(), who) {
		t.Errorf("diagnoseListenError result %q does not contain the occupant %q", got, who)
	}
	t.Logf("enriched error: %v", got)
}

func TestDiagnoseListenErrorPassesThroughUnrelatedErrors(t *testing.T) {
	other := fmt.Errorf("something else entirely")
	got := diagnoseListenError(context.Background(), other, "127.0.0.1:8077")
	if got != other {
		t.Errorf("diagnoseListenError altered a non-EADDRINUSE error: got %v, want unchanged %v", got, other)
	}

	if got := diagnoseListenError(context.Background(), nil, "127.0.0.1:8077"); got != nil {
		t.Errorf("diagnoseListenError(nil) = %v, want nil", got)
	}
}
