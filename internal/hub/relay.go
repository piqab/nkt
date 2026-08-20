package hub

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/althq/netknownsthat/internal/tunnel"
)

// handleTunnel accepts a managed host's outbound reverse-tunnel connection
// — see internal/tunnel for the host-side client and the wire protocol
// this speaks (WebSocket, multiplexed with yamux). Registered outside both
// RequireAuth and the request Timeout (see server.go): the connecting
// party is a machine presenting a per-host token, not a browser with a
// session cookie, and the connection is meant to live far longer than any
// ordinary request.
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	hostID, err := strconv.ParseInt(r.Header.Get(tunnel.HeaderHostID), 10, 64)
	token := r.Header.Get(tunnel.HeaderToken)
	if err != nil || token == "" {
		http.Error(w, "некорректные заголовки аутентификации резервного канала", http.StatusBadRequest)
		return
	}

	host, err := s.db.HostByID(r.Context(), hostID)
	if err != nil {
		http.Error(w, "неизвестный хост", http.StatusUnauthorized)
		return
	}
	// TunnelTokenHash is only ever set once TunnelEnabled was on for some
	// install (see Manager.prepareTunnelEnv) — checking it directly rather
	// than the (possibly since-toggled-off) TunnelEnabled flag means a
	// connection using an old but still-valid token from before the
	// operator disabled the feature is still refused, since the token
	// itself is exactly what proves this is genuinely that host.
	if len(host.TunnelTokenHash) == 0 {
		http.Error(w, "резервный канал не настроен для этого хоста", http.StatusUnauthorized)
		return
	}

	// Rate-limited by host id, not by the raw (still-unvalidated-until-
	// here) header value: only a token guess against a real, tunnel-
	// configured host is what this is meant to slow down, and host id
	// already passed a DB lookup above — an attacker can't cheaply grow
	// s.hub.tunnelAttempts' map by spraying arbitrary garbage ids that
	// never reach this point at all (see AttemptLimiter's own doc comment
	// on why that distinction matters).
	attemptKey := strconv.FormatInt(hostID, 10)
	if !s.hub.tunnelAttempts.Allow(attemptKey) {
		http.Error(w, "слишком много неудачных попыток подключения, попробуйте позже", http.StatusTooManyRequests)
		return
	}
	if subtle.ConstantTimeCompare(tunnel.TokenHash(token), host.TunnelTokenHash) != 1 {
		s.hub.tunnelAttempts.Fail(attemptKey)
		http.Error(w, "неверный токен резервного канала", http.StatusUnauthorized)
		return
	}
	s.hub.tunnelAttempts.Clear(attemptKey)

	wsConn, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept already wrote its own error response.
		return
	}
	defer wsConn.CloseNow()

	conn := websocket.NetConn(r.Context(), wsConn, websocket.MessageBinary)
	session, err := yamux.Server(conn, tunnel.Config())
	if err != nil {
		s.log.Warn("резервный канал: настройка мультиплексирования", "host_id", hostID, "err", err)
		return
	}
	defer session.Close()

	s.hub.registerRelay(hostID, session)
	defer s.hub.dropRelay(hostID, session)

	s.log.Info("резервный канал: хост подключился", "host_id", hostID, "host", host.Name)
	<-session.CloseChan()
	s.log.Info("резервный канал: хост отключился", "host_id", hostID, "host", host.Name)
}

// registerRelay records hostID's live reverse-tunnel session, replacing
// (and closing) any previous one — a host that reconnects after a network
// blip always wins over whatever stale session was registered before it,
// rather than the two racing to serve the same virtual streams.
func (m *Manager) registerRelay(hostID int64, session *yamux.Session) {
	m.relayMu.Lock()
	old := m.relaySessions[hostID]
	m.relaySessions[hostID] = session
	m.relayMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// dropRelay removes hostID's session from the registry, but only if it's
// still the exact session passed in — so a handler's own deferred cleanup,
// running after a newer connection has already replaced it via
// registerRelay, doesn't evict that newer one out from under it.
func (m *Manager) dropRelay(hostID int64, session *yamux.Session) {
	m.relayMu.Lock()
	if m.relaySessions[hostID] == session {
		delete(m.relaySessions, hostID)
	}
	m.relayMu.Unlock()
}

// dropRelayAll unconditionally drops and closes whatever session is
// currently registered for hostID — used by CloseHost (host removed from
// the registry entirely), unlike dropRelay's own narrower check.
func (m *Manager) dropRelayAll(hostID int64) {
	m.relayMu.Lock()
	session := m.relaySessions[hostID]
	delete(m.relaySessions, hostID)
	m.relayMu.Unlock()
	if session != nil {
		_ = session.Close()
	}
}

// TunnelConnected reports whether hostID currently has a live reverse-tunnel
// session registered — independent of whether anything has actually dialed
// through it yet (see Manager.recordChannel for that). Surfaced to the UI
// as a standing "резервный канал подключён" badge, separate from the
// "сейчас используется" one that only lights up once SSH has actually
// failed and traffic is really flowing over it.
func (m *Manager) TunnelConnected(hostID int64) bool {
	m.relayMu.Lock()
	defer m.relayMu.Unlock()
	return m.relaySessions[hostID] != nil
}

// relayDial returns a dialFunc that opens a new virtual stream over
// hostID's live reverse-tunnel session, and whether one is currently
// registered at all — consulted by dialerFor only after an SSH dial has
// already failed.
func (m *Manager) relayDial(hostID int64) (dialFunc, bool) {
	m.relayMu.Lock()
	session := m.relaySessions[hostID]
	m.relayMu.Unlock()
	if session == nil {
		return nil, false
	}
	return func(_, _ string) (net.Conn, error) { return session.Open() }, true
}
