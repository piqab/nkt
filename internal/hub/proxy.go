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
// cached one is fresh enough.
func (m *Manager) cookieFor(ctx context.Context, hostID int64, client *ssh.Client) (string, error) {
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

	cookie, err := bootstrapLogin(ctx, client, host.AdminUser, string(adminPassword))
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

// Proxy returns a handler that forwards every request it receives to
// hostID's own nkt API through the SSH tunnel, injecting that host's own
// session cookie — the browser only ever authenticates to the hub itself,
// never to each managed host individually. The caller is expected to have
// already rewritten the request path to what the remote's own API expects
// (see server.go's proxyHost).
func (m *Manager) Proxy(hostID int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		client, err := m.clientFor(ctx, hostID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		cookie, err := m.cookieFor(ctx, hostID, client)
		if err != nil {
			m.dropClient(hostID)
			m.dropSession(hostID)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = "http"
				req.URL.Host = remoteAPIAddr
				req.Host = remoteAPIAddr
				req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
			},
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return client.Dial("tcp", remoteAPIAddr)
				},
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				m.dropClient(hostID)
				m.dropSession(hostID)
				http.Error(w, "хост недоступен: "+err.Error(), http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// CloseHost drops any pooled connection/session for a host — called when a
// host is removed from the registry.
func (m *Manager) CloseHost(hostID int64) {
	m.dropClient(hostID)
	m.dropSession(hostID)
}
