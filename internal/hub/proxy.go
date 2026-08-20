package hub

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/secretbox"
	"github.com/althq/netknownsthat/internal/store"
)

// connIdleTTL bounds how long an unused SSH connection to a host stays open
// — a hub managing many hosts must not keep every one of them connected
// forever just because it was proxied to once.
const connIdleTTL = 10 * time.Minute

// sessionTTL is how long a captured remote session cookie is reused before
// cookieFor logs in again — comfortably inside the remote's own
// NKT_SESSION_TTL (12h default), so a proxied request is never the one that
// discovers a stale cookie.
const sessionTTL = 2 * time.Hour

type hostConn struct {
	client   *ssh.Client
	lastUsed time.Time
}

type sessionCache struct {
	cookie  string
	expires time.Time
}

// clientFor returns a live SSH connection to hostID, reusing a pooled one
// when available and dialing fresh otherwise.
func (m *Manager) clientFor(ctx context.Context, hostID int64) (*ssh.Client, error) {
	m.connsMu.Lock()
	if hc, ok := m.conns[hostID]; ok {
		hc.lastUsed = time.Now()
		m.connsMu.Unlock()
		return hc.client, nil
	}
	m.connsMu.Unlock()

	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return nil, fmt.Errorf("хост не найден: %w", err)
	}
	if host.Status != store.HostStatusOnline {
		return nil, fmt.Errorf("хост %q ещё не готов (статус: %s)", host.Name, host.Status)
	}
	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return nil, fmt.Errorf("расшифровка SSH-секрета: %w", err)
	}
	client, err := dialSSH(ctx, host.Addr, host.SSHPort, host.SSHUser, host.SSHAuthKind, secret)
	if err != nil {
		return nil, err
	}

	m.connsMu.Lock()
	m.conns[hostID] = &hostConn{client: client, lastUsed: time.Now()}
	m.connsMu.Unlock()

	_ = m.db.TouchHostSeen(ctx, hostID)
	return client, nil
}

// dropClient closes and forgets a pooled connection — called whenever a
// proxied request fails, so the next one reconnects instead of repeatedly
// handing out a dead client.
func (m *Manager) dropClient(hostID int64) {
	m.connsMu.Lock()
	hc, ok := m.conns[hostID]
	delete(m.conns, hostID)
	m.connsMu.Unlock()
	if ok {
		_ = hc.client.Close()
	}
}

// evictIdleConns closes SSH connections that have sat unused past
// connIdleTTL. Meant to run as a background goroutine for the hub's
// lifetime; returns when ctx is done.
func (m *Manager) evictIdleConns(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.connsMu.Lock()
			for id, hc := range m.conns {
				if time.Since(hc.lastUsed) > connIdleTTL {
					delete(m.conns, id)
					_ = hc.client.Close()
				}
			}
			m.connsMu.Unlock()
		}
	}
}

// cookieFor returns a session cookie for hostID's own nkt, logging in as its
// bootstrap admin (with the credentials StartInstall saved) whenever no
// cached one is fresh enough. dial reaches the host over whichever channel
// dialerFor picked — SSH or the reverse-tunnel fallback.
func (m *Manager) cookieFor(ctx context.Context, hostID int64, dial dialFunc) (string, error) {
	m.sessionMu.Lock()
	if sc, ok := m.sessions[hostID]; ok && time.Now().Before(sc.expires) {
		m.sessionMu.Unlock()
		return sc.cookie, nil
	}
	m.sessionMu.Unlock()

	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return "", fmt.Errorf("хост не найден: %w", err)
	}
	if host.AdminUser == "" || len(host.AdminPasswordEnc) == 0 {
		return "", fmt.Errorf("для хоста %q ещё не сохранена учётная запись администратора", host.Name)
	}
	adminPassword, err := secretbox.Decrypt(m.key, host.AdminPasswordEnc)
	if err != nil {
		return "", fmt.Errorf("расшифровка пароля администратора: %w", err)
	}

	cookie, err := bootstrapLogin(ctx, dial, host.AdminUser, string(adminPassword))
	if err != nil {
		return "", fmt.Errorf("вход на хост %q: %w", host.Name, err)
	}

	m.sessionMu.Lock()
	m.sessions[hostID] = sessionCache{cookie: cookie, expires: time.Now().Add(sessionTTL)}
	m.sessionMu.Unlock()
	return cookie, nil
}

// dropSession forgets a cached cookie — called alongside dropClient so a
// reconnect also gets a fresh login instead of replaying a cookie tied to a
// connection that's gone.
func (m *Manager) dropSession(hostID int64) {
	m.sessionMu.Lock()
	delete(m.sessions, hostID)
	m.sessionMu.Unlock()
}

// channelSSH and channelTunnel identify which path dialerFor picked, for
// callers that surface it to the operator (see recordChannel) — a plain
// string rather than a typed enum since its only consumers are a log field
// and a JSON API response.
const (
	channelSSH    = "ssh"
	channelTunnel = "tunnel"
)

// dialerFor returns a way to reach hostID's own nkt API: SSH first — the
// primary, fully-capable path, unchanged — falling back to the
// reverse-tunnel channel (internal/tunnel, see relay.go) only when the SSH
// dial itself fails and a live tunnel session happens to be registered for
// this host. Install/update/SFTP/sudo commands never go through this —
// they need real SSH regardless and call clientFor/dialSSH directly (except
// installOverTunnel's own deliberate fallback, which never calls this
// either — it already knows which channel it's using).
//
// The returned onFail must be called if something reached via dial later
// fails too (a stale pooled SSH conn dying mid-use, say) — always safe to
// call even when there's nothing to drop.
func (m *Manager) dialerFor(ctx context.Context, hostID int64) (dial dialFunc, channel string, onFail func(), err error) {
	client, sshErr := m.clientFor(ctx, hostID)
	if sshErr == nil {
		return client.Dial, channelSSH, func() { m.dropClient(hostID); m.dropSession(hostID) }, nil
	}
	if relay, ok := m.relayDial(hostID); ok {
		return relay, channelTunnel, func() {}, nil
	}
	return nil, "", nil, sshErr
}

// Proxy returns a handler that forwards every request it receives to
// hostID's own nkt API — over SSH, or the reverse-tunnel fallback when SSH
// is unreachable (see dialerFor) — injecting that host's own session
// cookie either way: the browser only ever authenticates to the hub
// itself, never to each managed host individually. The caller is expected
// to have already rewritten the request path to what the remote's own API
// expects (see server.go's proxyHost).
func (m *Manager) Proxy(hostID int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		dial, channel, onFail, err := m.dialerFor(ctx, hostID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		m.recordChannel(hostID, channel)
		cookie, err := m.cookieFor(ctx, hostID, dial)
		if err != nil {
			onFail()
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = "http"
				req.URL.Host = remoteAPIAddr
				req.Host = remoteAPIAddr
				// The incoming request still carries the browser's own hub
				// session cookie (proxyHost clones it as-is) — same name
				// (auth.SessionCookie) as the one injected below, but
				// meaningless to this host. Left in place, the two would
				// travel together and the host would resolve whichever one
				// net/http's Cookie() returns first — the hub's, in
				// practice — instead of the one actually meant for it,
				// failing auth on every single request. It must be gone
				// before AddCookie puts the right one in its place.
				req.Header.Del("Cookie")
				req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
			},
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return dial("tcp", remoteAPIAddr)
				},
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				onFail()
				http.Error(w, "хост недоступен: "+err.Error(), http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// CloseHost drops any pooled connection/session/cached overview/tunnel
// session for a host — called when a host is removed from the registry
// entirely. Deliberately not used by UpdateHost/UpdateHostGenerated (see
// dropSSHPool): editing a host's SSH details has nothing to do with
// whether its reverse-tunnel session is still good, and dropping it there
// too would force every "изменить" to wait out the host's own reconnect
// backoff (up to 30s) before install()'s SSH-down fallback could find a
// live session again — exactly the gap that let a save-then-reinstall
// (e.g. flipping the terminal checkbox) fail over to raw SSH errors
// instead of the tunnel that was working a moment before.
func (m *Manager) CloseHost(hostID int64) {
	m.dropSSHPool(hostID)
	m.dropOverview(hostID)
	m.dropRelayAll(hostID)
}

// dropSSHPool forgets hostID's pooled SSH connection and cached session
// cookie — everything UpdateHost/UpdateHostGenerated need to drop after
// changing connection details (address, port, user, credential) so the
// next request reconnects with the new ones, without touching the
// unrelated reverse-tunnel session or overview cache CloseHost also clears.
func (m *Manager) dropSSHPool(hostID int64) {
	m.dropClient(hostID)
	m.dropSession(hostID)
}
