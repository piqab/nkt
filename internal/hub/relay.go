package hub

import (
	"net"

	"github.com/hashicorp/yamux"
)

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
