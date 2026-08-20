package hub

import (
	"net"
	"testing"

	"github.com/hashicorp/yamux"

	"github.com/althq/netknownsthat/internal/store"
)

// newTestRelaySession wires a real yamux client/server pair over an
// in-memory net.Pipe — enough to register a live "reverse-tunnel session"
// for a host without any of TestProxyFallsBackToRelayWhenSSHUnreachable's
// real WebSocket/SSH machinery, since tunnelReinstallFallback/TunnelConnected
// only ever check whether *a* session is registered, never dial through it
// themselves in these tests.
func newTestRelaySession(t *testing.T) *yamux.Session {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		srv, err := yamux.Server(serverConn, nil)
		if err != nil {
			return
		}
		<-srv.CloseChan()
	}()
	t.Cleanup(func() { <-serverDone })

	client, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatalf("yamux.Client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestTunnelReinstallFallback locks in every condition that has to hold
// before install() will even try updating a host over the reverse-tunnel
// channel instead of failing outright when SSH is down: the feature must
// be on for that host, its architecture must already be known (a first
// install never gets here — see the function's own doc comment), and a
// session must actually be live right now.
func TestTunnelReinstallFallback(t *testing.T) {
	m, _ := newTestManager(t)

	t.Run("tunnel not enabled", func(t *testing.T) {
		host := store.Host{ID: 1, TunnelEnabled: false, Arch: "linux/amd64"}
		if m.tunnelReinstallFallback(host) {
			t.Error("tunnelReinstallFallback() = true with TunnelEnabled false")
		}
	})

	t.Run("architecture not yet known", func(t *testing.T) {
		host := store.Host{ID: 2, TunnelEnabled: true, Arch: ""}
		if m.tunnelReinstallFallback(host) {
			t.Error("tunnelReinstallFallback() = true with Arch empty")
		}
	})

	t.Run("enabled and known arch, but no live session", func(t *testing.T) {
		host := store.Host{ID: 3, TunnelEnabled: true, Arch: "linux/amd64"}
		if m.tunnelReinstallFallback(host) {
			t.Error("tunnelReinstallFallback() = true with no registered relay session")
		}
	})

	t.Run("enabled, known arch, live session: falls back", func(t *testing.T) {
		host := store.Host{ID: 4, TunnelEnabled: true, Arch: "linux/amd64"}
		session := newTestRelaySession(t)
		m.registerRelay(host.ID, session)
		t.Cleanup(func() { m.dropRelayAll(host.ID) })

		if !m.tunnelReinstallFallback(host) {
			t.Fatal("tunnelReinstallFallback() = false, want true with a live relay session")
		}
	})
}

// TestTunnelConnectedAndRecordChannel exercises the two pieces of state the
// "Канал" badge in the UI reads (see handlers.go's hostWithOverview):
// TunnelConnected reflects whether a relay session is registered regardless
// of use, while recordChannel/Overview track which path was most recently
// actually dialed.
func TestTunnelConnectedAndRecordChannel(t *testing.T) {
	m, _ := newTestManager(t)
	const hostID = int64(7)

	if m.TunnelConnected(hostID) {
		t.Error("TunnelConnected() = true before any session was registered")
	}

	session := newTestRelaySession(t)
	m.registerRelay(hostID, session)
	if !m.TunnelConnected(hostID) {
		t.Error("TunnelConnected() = false with a session registered")
	}

	m.recordChannel(hostID, channelTunnel)
	ov, ok := m.Overview(hostID)
	if !ok || ov.Channel != channelTunnel {
		t.Errorf("Overview() = (%+v, %v), want Channel = %q", ov, ok, channelTunnel)
	}

	m.dropRelayAll(hostID)
	if m.TunnelConnected(hostID) {
		t.Error("TunnelConnected() = true after dropRelayAll")
	}
}

// TestDynamicRelayDialSurvivesSessionSwap is the regression test for the
// exact failure mode installOverTunnel exists to avoid: a dial bound to
// *today's* session would start erroring the moment that session closes —
// which self-update's own restart guarantees happens mid-flow. dynamicRelayDial
// must re-resolve on every call, so it keeps working once the restarted host
// reconnects with a brand new session, without installOverTunnel needing to
// notice the swap itself.
func TestDynamicRelayDialSurvivesSessionSwap(t *testing.T) {
	m, _ := newTestManager(t)
	const hostID = int64(9)

	dial := m.dynamicRelayDial(hostID)
	if _, err := dial("tcp", "127.0.0.1:0"); err == nil {
		t.Error("dynamicRelayDial()'s dial succeeded with no session registered at all")
	}

	first := newTestRelaySession(t)
	m.registerRelay(hostID, first)
	if _, err := dial("tcp", "127.0.0.1:0"); err != nil {
		t.Errorf("dial() with a live session: %v", err)
	}

	// Simulate the host restarting mid-update: its old session drops, a
	// new one (a fresh yamux handshake, not the same object) replaces it.
	_ = first.Close()
	second := newTestRelaySession(t)
	m.registerRelay(hostID, second)

	if _, err := dial("tcp", "127.0.0.1:0"); err != nil {
		t.Errorf("dial() after the session was swapped out from under it: %v", err)
	}
}
