package hub

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
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

// withShortTunnelReinstallFallbackWait shrinks the wait/poll intervals
// awaitTunnelReinstallFallback uses so tests exercise real timer behavior
// without actually waiting the production 5-second budget.
func withShortTunnelReinstallFallbackWait(t *testing.T) {
	t.Helper()
	origWait, origPoll := tunnelReinstallFallbackWait, tunnelReinstallFallbackPoll
	tunnelReinstallFallbackWait = 200 * time.Millisecond
	tunnelReinstallFallbackPoll = 20 * time.Millisecond
	t.Cleanup(func() {
		tunnelReinstallFallbackWait = origWait
		tunnelReinstallFallbackPoll = origPoll
	})
}

// TestAwaitTunnelReinstallFallback covers the bounded-wait wrapper install()
// actually calls: it must not wait at all when tunnelReinstallFallback's own
// fast-fail conditions (TunnelEnabled, known Arch) already rule out the
// fallback for good, but it must ride out a session that appears moments
// after SSH failed — the exact "works on the second click" symptom this
// exists to fix, reproduced here as "appears within the wait window" rather
// than as a second manual call.
func TestAwaitTunnelReinstallFallback(t *testing.T) {
	t.Run("already live: returns true immediately, no wait needed", func(t *testing.T) {
		m, _ := newTestManager(t)
		host := store.Host{ID: 10, TunnelEnabled: true, Arch: "linux/amd64"}
		session := newTestRelaySession(t)
		m.registerRelay(host.ID, session)
		t.Cleanup(func() { m.dropRelayAll(host.ID) })

		start := time.Now()
		if !m.awaitTunnelReinstallFallback(context.Background(), host) {
			t.Fatal("awaitTunnelReinstallFallback() = false, want true with an already-live session")
		}
		if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
			t.Errorf("took %v to return for an already-live session, want near-instant", elapsed)
		}
	})

	t.Run("tunnel not enabled: returns false without waiting", func(t *testing.T) {
		m, _ := newTestManager(t)
		host := store.Host{ID: 11, TunnelEnabled: false, Arch: "linux/amd64"}

		start := time.Now()
		if m.awaitTunnelReinstallFallback(context.Background(), host) {
			t.Fatal("awaitTunnelReinstallFallback() = true with TunnelEnabled false")
		}
		if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
			t.Errorf("took %v to return false for TunnelEnabled=false, want near-instant (no point waiting)", elapsed)
		}
	})

	t.Run("session registers partway through the wait: returns true", func(t *testing.T) {
		withShortTunnelReinstallFallbackWait(t)
		m, _ := newTestManager(t)
		host := store.Host{ID: 12, TunnelEnabled: true, Arch: "linux/amd64"}
		// Created up front, on the main test goroutine (t.Fatalf inside it
		// would be unsafe from the background goroutine below) — only the
		// registration itself is delayed.
		session := newTestRelaySession(t)
		t.Cleanup(func() { m.dropRelayAll(host.ID) })

		go func() {
			time.Sleep(60 * time.Millisecond)
			m.registerRelay(host.ID, session)
		}()

		if !m.awaitTunnelReinstallFallback(context.Background(), host) {
			t.Fatal("awaitTunnelReinstallFallback() = false, want true once the session registered mid-wait")
		}
	})

	t.Run("session never appears: returns false once the wait elapses", func(t *testing.T) {
		withShortTunnelReinstallFallbackWait(t)
		m, _ := newTestManager(t)
		host := store.Host{ID: 13, TunnelEnabled: true, Arch: "linux/amd64"}

		if m.awaitTunnelReinstallFallback(context.Background(), host) {
			t.Fatal("awaitTunnelReinstallFallback() = true with no session ever registered")
		}
	})

	t.Run("ctx cancelled mid-wait: returns false promptly", func(t *testing.T) {
		withShortTunnelReinstallFallbackWait(t)
		m, _ := newTestManager(t)
		host := store.Host{ID: 14, TunnelEnabled: true, Arch: "linux/amd64"}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		if m.awaitTunnelReinstallFallback(ctx, host) {
			t.Fatal("awaitTunnelReinstallFallback() = true with a cancelled ctx and no session")
		}
		if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
			t.Errorf("took %v to return after ctx cancellation, want it to stop promptly", elapsed)
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

// TestPrepareTunnelEnv covers Manager.prepareTunnelEnv now that it no
// longer has any hub-address guessing to do — the hub is the side that
// dials out (see tunneldial.go), using the host's own already-known Addr,
// so this only has TunnelEnabled to branch on. Disabled means no token is
// generated or stored at all; enabled means a token is generated, stored
// secretbox-encrypted (recoverable — the hub has to present it again on
// every future reconnect, unlike the first iteration's plain hash), and
// ListenAddr reflects cfg.HubTunnelPort.
func TestPrepareTunnelEnv(t *testing.T) {
	ctx := context.Background()

	t.Run("disabled: no token generated or stored", func(t *testing.T) {
		m, db := newTestManager(t)
		id, err := db.CreateHost(ctx, "h", "203.0.113.9", 22, "root", store.HostAuthKey, []byte("enc"))
		if err != nil {
			t.Fatalf("CreateHost: %v", err)
		}
		host, err := db.HostByID(ctx, id)
		if err != nil {
			t.Fatalf("HostByID: %v", err)
		}

		tun, err := m.prepareTunnelEnv(ctx, host.ID, host)
		if err != nil {
			t.Fatalf("prepareTunnelEnv: %v", err)
		}
		if tun.Enabled {
			t.Errorf("prepareTunnelEnv() = %+v, want Enabled=false with TunnelEnabled off", tun)
		}
		got, err := db.HostByID(ctx, id)
		if err != nil {
			t.Fatalf("HostByID: %v", err)
		}
		if got.TunnelTokenEnc != nil {
			t.Errorf("TunnelTokenEnc = %x, want nil — nothing should be stored when the feature is off", got.TunnelTokenEnc)
		}
	})

	t.Run("enabled: generates, stores encrypted, and derives ListenAddr from HubTunnelPort", func(t *testing.T) {
		m, db := newTestManager(t)
		m.cfg = &config.Config{HubTunnelPort: 9999}
		id, err := db.CreateHost(ctx, "h", "203.0.113.9", 22, "root", store.HostAuthKey, []byte("enc"))
		if err != nil {
			t.Fatalf("CreateHost: %v", err)
		}
		host, err := db.HostByID(ctx, id)
		if err != nil {
			t.Fatalf("HostByID: %v", err)
		}
		host.TunnelEnabled = true

		tun, err := m.prepareTunnelEnv(ctx, host.ID, host)
		if err != nil {
			t.Fatalf("prepareTunnelEnv: %v", err)
		}
		if !tun.Enabled || tun.Token == "" {
			t.Fatalf("prepareTunnelEnv() = %+v, want Enabled=true with a generated token", tun)
		}
		if tun.ListenAddr != "0.0.0.0:9999" {
			t.Errorf("ListenAddr = %q, want %q (from cfg.HubTunnelPort)", tun.ListenAddr, "0.0.0.0:9999")
		}

		got, err := db.HostByID(ctx, id)
		if err != nil {
			t.Fatalf("HostByID: %v", err)
		}
		if len(got.TunnelTokenEnc) == 0 {
			t.Fatal("TunnelTokenEnc not stored")
		}
		decrypted, err := secretbox.Decrypt(m.key, got.TunnelTokenEnc)
		if err != nil {
			t.Fatalf("decrypt stored token: %v", err)
		}
		if string(decrypted) != tun.Token {
			t.Errorf("stored token decrypts to %q, want the same token renderEnv got: %q", decrypted, tun.Token)
		}
	})
}
