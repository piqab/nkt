package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/tunnel"
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
			m.log.Debug("fallback channel: connection to host not established or dropped", "host_id", hostID, "err", err)
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
	var fingerprint []byte
	dialer := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: tunnelDialTimeout},
		Config: &tls.Config{
			// The usual chain/hostname verification a non-self-signed cert
			// would get is skipped (there's no CA to chain to), but this is
			// not blind trust: VerifyPeerCertificate below pins the cert's
			// own fingerprint on first sight and enforces it on every
			// dial after — see verifyPinnedTunnelCert. The token written
			// right after this handshake completes is still what actually
			// authenticates the connection to the host; the pin closes the
			// gap that left, an on-path attacker swapping in their own
			// cert to read that token off the wire before this existed.
			InsecureSkipVerify: true, //nolint:gosec
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				fp, verr := verifyPinnedTunnelCert(rawCerts, host.TunnelCertSHA256)
				fingerprint = fp
				return verr
			},
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		// A pin mismatch is not an ordinary connection failure (host down,
		// wrong port, transient network blip) — it means the certificate
		// this dial just saw doesn't match what was pinned on a previous
		// successful connection, so it's logged at Warn instead of
		// runTunnelDialer's usual silent-retry Debug line. Deliberately not
		// escalated into the host's own Status/ErrorMsg: that field means
		// "the primary connection to this host is broken" and drives
		// pollOnce's decision to keep polling it — a stale pin on the
		// fallback channel alone must not stop overview polling over what
		// is very likely still a perfectly healthy SSH connection.
		var mismatch *tunnelCertMismatchError
		if errors.As(err, &mismatch) {
			m.log.Warn("fallback channel: host certificate did not match the pinned one — possible connection spoofing, or the host was reinstalled without clearing the pin",
				"host_id", hostID, "host", host.Name, "err", mismatch)
		}
		return false, err
	}
	if len(host.TunnelCertSHA256) == 0 && len(fingerprint) > 0 {
		if err := m.db.SetHostTunnelCertSHA256(ctx, hostID, fingerprint); err != nil {
			m.log.Warn("fallback channel: could not save host certificate pin", "host_id", hostID, "err", err)
		} else {
			m.log.Info("fallback channel: pinned host certificate on first connection", "host_id", hostID, "host", host.Name)
		}
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

	m.log.Info("fallback channel: connected to host", "host_id", hostID, "host", host.Name)
	select {
	case <-ctx.Done():
		return true, nil
	case <-session.CloseChan():
		m.log.Info("fallback channel: connection to host closed", "host_id", hostID, "host", host.Name)
		return true, nil
	}
}
