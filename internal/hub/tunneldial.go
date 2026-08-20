package hub

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/tunnel"
)

// tunnelDialerScanInterval is how often maintainTunnelDialers re-reads the
// host list to notice a host that just got TunnelEnabled turned on/off (or
// deleted) — the running dialer goroutines themselves handle their own
// reconnects on a much faster cadence (see tunnelDialerMaxBackoff); this
// only governs how quickly a *configuration* change is picked up.
const tunnelDialerScanInterval = 15 * time.Second

// maintainTunnelDialers keeps exactly one runTunnelDialer goroutine alive
// per host with TunnelEnabled on and a token to present, stopping it the
// moment that stops being true (the feature was turned off, or the host was
// deleted) — the hub-side counterpart to what internal/tunnel.Run used to
// do on the host, now that the hub is the one dialing out. Meant to run for
// the hub's whole lifetime, started from Manager.Run alongside
// pollOverviews/evictIdleConns.
func (m *Manager) maintainTunnelDialers(ctx context.Context) {
	running := map[int64]context.CancelFunc{}
	defer func() {
		for _, cancel := range running {
			cancel()
		}
	}()

	ticker := time.NewTicker(tunnelDialerScanInterval)
	defer ticker.Stop()
	for {
		hosts, err := m.db.ListHosts(ctx)
		if err == nil {
			want := map[int64]bool{}
			for _, h := range hosts {
				if h.TunnelEnabled && len(h.TunnelTokenEnc) > 0 {
					want[h.ID] = true
					if _, ok := running[h.ID]; !ok {
						hctx, cancel := context.WithCancel(ctx)
						running[h.ID] = cancel
						go m.runTunnelDialer(hctx, h.ID)
					}
				}
			}
			for id, cancel := range running {
				if !want[id] {
					cancel()
					delete(running, id)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tunnelDialerMaxBackoff caps the reconnect delay runTunnelDialer backs off
// to after repeated failures — the same 30s ceiling internal/tunnel's old
// host-side client used.
const tunnelDialerMaxBackoff = 30 * time.Second

// tunnelDialTimeout bounds a single connection attempt — a host whose
// tunnel port is firewalled (packets silently dropped, not refused) must
// not hang this goroutine indefinitely.
const tunnelDialTimeout = 10 * time.Second

// runTunnelDialer maintains a persistent reverse-tunnel connection to one
// host, reconnecting with exponential backoff until ctx is cancelled —
// directly mirroring internal/tunnel's old Run/connectOnce shape, just
// running on the hub now that it is the side initiating the connection.
func (m *Manager) runTunnelDialer(ctx context.Context, hostID int64) {
	backoff := time.Second
	for ctx.Err() == nil {
		connected, err := m.tunnelDialOnce(ctx, hostID)
		if err != nil && ctx.Err() == nil {
			m.log.Debug("резервный канал: соединение с хостом не установлено или прервано", "host_id", hostID, "err", err)
		}
		if connected {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > tunnelDialerMaxBackoff {
			backoff = tunnelDialerMaxBackoff
		}
	}
}

// tunnelDialOnce dials hostID's reverse-tunnel listener once, registers the
// session for relayDial/Proxy to use, and blocks until it closes — reports
// whether it ever got far enough to be useful (so runTunnelDialer can
// decide whether to reset its backoff), same shape as the old host-side
// connectOnce.
func (m *Manager) tunnelDialOnce(ctx context.Context, hostID int64) (connected bool, err error) {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return false, err
	}
	token, err := secretbox.Decrypt(m.key, host.TunnelTokenEnc)
	if err != nil {
		return false, err
	}

	addr := net.JoinHostPort(host.Addr, strconv.Itoa(m.cfg.HubTunnelPort))
	dialer := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: tunnelDialTimeout},
		Config: &tls.Config{
			// Not blind trust: the token written right after this handshake
			// completes is what actually authenticates this connection to
			// the host (see internal/tunnel's package doc comment) — the
			// same trade-off dialSSH already makes for SSH host keys in
			// this codebase.
			InsecureSkipVerify: true, //nolint:gosec
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, err
	}
	if err := tunnel.WriteToken(conn, string(token)); err != nil {
		_ = conn.Close()
		return false, err
	}

	session, err := yamux.Client(conn, tunnel.Config())
	if err != nil {
		_ = conn.Close()
		return false, err
	}
	defer session.Close()

	m.registerRelay(hostID, session)
	defer m.dropRelay(hostID, session)

	m.log.Info("резервный канал: подключился к хосту", "host_id", hostID, "host", host.Name)
	select {
	case <-ctx.Done():
		return true, nil
	case <-session.CloseChan():
		m.log.Info("резервный канал: соединение с хостом разорвано", "host_id", hostID, "host", host.Name)
		return true, nil
	}
}
