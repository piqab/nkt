package hub

import (
	"context"
	"net"
	"testing"

	"github.com/hashicorp/yamux"

	"github.com/althq/netknownsthat/internal/config"
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

// TestLooksRoutableFromHost locks in the filter that keeps an obviously
// wrong guess (the operator's own loopback hop into the hub — an SSH
// tunnel, say) from ever being baked into a host's tunnel config as a
// stand-in for NKT_HUB_PUBLIC_ADDR — while deliberately NOT rejecting a
// private/LAN address, since hub and managed host sharing a private
// network is a normal, common deployment, not an edge case (this used to
// reject IsPrivate() too, which silently broke exactly that setup — see
// the git history for the incident this test now guards against).
func TestLooksRoutableFromHost(t *testing.T) {
	cases := []struct {
		hostport string
		want     bool
	}{
		{"", false},
		{"localhost:8443", false},
		{"localhost", false},
		{"127.0.0.1:8443", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"[::1]:8443", false},
		{"169.254.1.2:8443", false},
		{"0.0.0.0:8443", false},
		// Private/LAN addresses ARE accepted — hub and host on the same
		// network is legitimate, not something to silently disable.
		{"10.0.0.5:8443", true},
		{"192.168.1.20:8443", true},
		{"172.20.0.5:8443", true},
		{"203.0.113.5:8443", true},
		{"hub.example.com:8443", true},
		{"hub.example.com", true},
	}
	for _, c := range cases {
		if got := looksRoutableFromHost(c.hostport); got != c.want {
			t.Errorf("looksRoutableFromHost(%q) = %v, want %v", c.hostport, got, c.want)
		}
	}
}

// TestPrepareTunnelEnvAddrResolution covers the three-way priority
// prepareTunnelEnv applies to pick a hub address: an explicitly configured
// NKT_HUB_PUBLIC_ADDR always wins; without one, a routable request Host is
// used automatically; a non-routable one (or none at all) leaves the
// feature off exactly like an unset NKT_HUB_PUBLIC_ADDR always has.
func TestPrepareTunnelEnvAddrResolution(t *testing.T) {
	ctx := context.Background()

	newHost := func(t *testing.T, db *store.DB) store.Host {
		t.Helper()
		id, err := db.CreateHost(ctx, "h", "203.0.113.9", 22, "root", store.HostAuthKey, []byte("enc"))
		if err != nil {
			t.Fatalf("CreateHost: %v", err)
		}
		host, err := db.HostByID(ctx, id)
		if err != nil {
			t.Fatalf("HostByID: %v", err)
		}
		host.TunnelEnabled = true
		return host
	}

	t.Run("tunnel disabled: never resolves an address", func(t *testing.T) {
		m, _ := newTestManager(t)
		host := store.Host{ID: 1, TunnelEnabled: false}
		tun, _, err := m.prepareTunnelEnv(ctx, host.ID, host, "public.example.com:8443")
		if err != nil {
			t.Fatalf("prepareTunnelEnv: %v", err)
		}
		if tun.Enabled {
			t.Errorf("prepareTunnelEnv() = %+v, want Enabled=false with TunnelEnabled off", tun)
		}
	})

	t.Run("explicit config always wins over the request Host", func(t *testing.T) {
		m, db := newTestManager(t)
		m.cfg = &config.Config{HubPublicAddr: "configured.example.com:8443"}
		host := newHost(t, db)

		tun, _, err := m.prepareTunnelEnv(ctx, host.ID, host, "public.example.com:8443")
		if err != nil {
			t.Fatalf("prepareTunnelEnv: %v", err)
		}
		if !tun.Enabled || tun.HubAddr != "configured.example.com:8443" {
			t.Errorf("prepareTunnelEnv() = %+v, want HubAddr from cfg.HubPublicAddr", tun)
		}
	})

	t.Run("no config: falls back to a routable request Host", func(t *testing.T) {
		m, db := newTestManager(t)
		host := newHost(t, db)

		tun, _, err := m.prepareTunnelEnv(ctx, host.ID, host, "public.example.com:8443")
		if err != nil {
			t.Fatalf("prepareTunnelEnv: %v", err)
		}
		if !tun.Enabled || tun.HubAddr != "public.example.com:8443" {
			t.Errorf("prepareTunnelEnv() = %+v, want HubAddr from the request Host", tun)
		}
	})

	t.Run("no config, non-routable request Host: stays off", func(t *testing.T) {
		m, db := newTestManager(t)
		host := newHost(t, db)

		tun, _, err := m.prepareTunnelEnv(ctx, host.ID, host, "127.0.0.1:8443")
		if err != nil {
			t.Fatalf("prepareTunnelEnv: %v", err)
		}
		if tun.Enabled {
			t.Errorf("prepareTunnelEnv() = %+v, want Enabled=false with only a loopback request Host to go on", tun)
		}
	})

	t.Run("private hub address, public host address: enabled but warns", func(t *testing.T) {
		m, db := newTestManager(t)
		m.cfg = &config.Config{HubPublicAddr: "192.168.1.50:8443"}
		host := newHost(t, db) // Addr: "203.0.113.9" — a public-looking IP

		tun, warn, err := m.prepareTunnelEnv(ctx, host.ID, host, "")
		if err != nil {
			t.Fatalf("prepareTunnelEnv: %v", err)
		}
		if !tun.Enabled || tun.HubAddr != "192.168.1.50:8443" {
			t.Errorf("prepareTunnelEnv() = %+v, want it still configured with the private address", tun)
		}
		if warn == "" {
			t.Error("prepareTunnelEnv() warn = \"\", want a warning about the private hub / public host mismatch")
		}
	})

	t.Run("private hub address, private host address: no warning", func(t *testing.T) {
		m, db := newTestManager(t)
		m.cfg = &config.Config{HubPublicAddr: "192.168.1.50:8443"}
		id, err := db.CreateHost(ctx, "h", "192.168.1.77", 22, "root", store.HostAuthKey, []byte("enc"))
		if err != nil {
			t.Fatalf("CreateHost: %v", err)
		}
		host, err := db.HostByID(ctx, id)
		if err != nil {
			t.Fatalf("HostByID: %v", err)
		}
		host.TunnelEnabled = true

		tun, warn, err := m.prepareTunnelEnv(ctx, host.ID, host, "")
		if err != nil {
			t.Fatalf("prepareTunnelEnv: %v", err)
		}
		if !tun.Enabled {
			t.Errorf("prepareTunnelEnv() = %+v, want Enabled=true", tun)
		}
		if warn != "" {
			t.Errorf("prepareTunnelEnv() warn = %q, want none — hub and host are both on the same private network", warn)
		}
	})
}
