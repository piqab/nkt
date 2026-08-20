package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/store"
)

// pollHostConcurrency bounds how many hosts pollOverviews contacts at once —
// a fleet of many hosts must not open dozens of simultaneous SSH sessions
// just because one poll tick fired.
const pollHostConcurrency = 8

// pollHostTimeout bounds a single host's poll — one hung host must not stall
// the rest of the fleet's tick.
const pollHostTimeout = 10 * time.Second

// hostOverview is what pollOverviews last learned about one host: its
// findings severity counts (same map[string]int shape /api/overview's own
// "findings" field uses) and whether the last attempt to reach it succeeded.
// Kept in memory only (see Manager.overview) — like conns/sessions, this is
// "fresh while the hub is running" data with no reason to survive a restart,
// unlike a durable observation such as sudo_status.
type hostOverview struct {
	reachable bool
	findings  map[string]int
	// lastPolledAt is the last *successful* poll — findings is only ever
	// updated on success, so this is also findings' own freshness.
	lastPolledAt time.Time
	// lastCheckedAt is the last attempt, success or failure.
	lastCheckedAt time.Time
	errMsg        string
	// version is what the host's own binary reports serving the request,
	// which is not the same thing as the version the hub recorded having
	// installed there (store.Host.NktVersion): an update that silently
	// failed to take effect shows up as exactly that difference.
	version string
}

// pollOverviews periodically refreshes every online host's findings/
// reachability, following evictIdleConns' exact ticker+select shape (see
// proxy.go). Meant to run as a background goroutine for the hub's lifetime,
// started from Run.
//
// A side effect worth calling out: continuously polling every online host
// means clientFor's cache-hit path keeps marking their pooled connections as
// recently used, so evictIdleConns' idle TTL effectively stops applying to
// them — their SSH connections and login sessions now stay open for as long
// as the hub runs. That is the correct behavior for "poll all online hosts
// in the background," not a bug; connIdleTTL still matters for hosts that go
// offline or get removed.
func (m *Manager) pollOverviews(ctx context.Context) {
	interval := m.cfg.HubFindingsPollInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pollOnce(ctx)
		}
	}
}

// pollOnce polls every online host once, bounding concurrency with a
// semaphore so one tick never opens more than pollHostConcurrency SSH
// sessions at a time.
func (m *Manager) pollOnce(ctx context.Context) {
	hosts, err := m.db.ListHosts(ctx)
	if err != nil {
		return
	}

	live := map[int64]bool{}
	sem := make(chan struct{}, pollHostConcurrency)
	var wg sync.WaitGroup
	for _, h := range hosts {
		if h.Status != store.HostStatusOnline {
			continue
		}
		live[h.ID] = true
		wg.Add(1)
		sem <- struct{}{}
		go func(hostID int64) {
			defer wg.Done()
			defer func() { <-sem }()
			m.pollHost(ctx, hostID)
		}(h.ID)
	}
	wg.Wait()

	m.pruneOverview(live)
}

// pollHost refreshes one host's cached overview — over SSH, or the
// reverse-tunnel fallback when SSH is unreachable (see dialerFor), same as
// Proxy. A connection-level failure (dialing, logging in, or the HTTP
// round-trip itself) evicts whichever pooled client/session dialerFor used
// — mirroring Proxy's ErrorHandler — so the next tick reconnects from
// scratch instead of retrying a connection already known bad. A non-2xx
// response or undecodable body leaves the connection alone: nothing about
// the tunnel or session was actually broken, there is just nothing to
// report this cycle. Either way, previously cached findings are never
// cleared on failure — a stale-but-labeled count is more useful in the UI
// than one that silently drops to zero on a single blip.
func (m *Manager) pollHost(ctx context.Context, hostID int64) {
	ctx, cancel := context.WithTimeout(ctx, pollHostTimeout)
	defer cancel()

	dial, onFail, err := m.dialerFor(ctx, hostID)
	if err != nil {
		m.recordUnreachable(hostID, err)
		return
	}
	cookie, err := m.cookieFor(ctx, hostID, dial)
	if err != nil {
		onFail()
		m.recordUnreachable(hostID, err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+remoteAPIAddr+"/api/overview", nil)
	if err != nil {
		m.recordUnreachable(hostID, err)
		return
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})

	resp, err := tunnelHTTPClient(dial).Do(req)
	if err != nil {
		onFail()
		m.recordUnreachable(hostID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		m.recordUnreachable(hostID, fmt.Errorf("код %d", resp.StatusCode))
		return
	}

	var body struct {
		Findings map[string]int `json:"findings"`
		Version  string         `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		m.recordUnreachable(hostID, err)
		return
	}

	now := time.Now()
	m.overviewMu.Lock()
	m.overview[hostID] = hostOverview{
		reachable:     true,
		findings:      body.Findings,
		version:       body.Version,
		lastPolledAt:  now,
		lastCheckedAt: now,
	}
	m.overviewMu.Unlock()
}

// recordUnreachable marks a host unreachable without touching whatever
// findings counts were last successfully polled — see pollHost's doc
// comment for why.
func (m *Manager) recordUnreachable(hostID int64, err error) {
	m.overviewMu.Lock()
	defer m.overviewMu.Unlock()
	cur := m.overview[hostID]
	cur.reachable = false
	cur.lastCheckedAt = time.Now()
	cur.errMsg = err.Error()
	m.overview[hostID] = cur
}

// pruneOverview drops cached entries for host ids pollOnce didn't just see
// as online — a host that was deleted, or moved out of "online" status,
// must not keep reporting stale findings forever.
func (m *Manager) pruneOverview(live map[int64]bool) {
	m.overviewMu.Lock()
	defer m.overviewMu.Unlock()
	for id := range m.overview {
		if !live[id] {
			delete(m.overview, id)
		}
	}
}

// dropOverview forgets a host's cached overview outright — called alongside
// dropClient/dropSession when a host is removed from the registry.
func (m *Manager) dropOverview(hostID int64) {
	m.overviewMu.Lock()
	defer m.overviewMu.Unlock()
	delete(m.overview, hostID)
}

// Overview returns hostID's last-polled findings/reachability, and whether
// pollOverviews has ever reported anything for it at all (false for a host
// not yet polled, or never online). The returned map is a defensive copy —
// callers must not be handed the cache's own backing map under lock.
func (m *Manager) Overview(hostID int64) (HostOverview, bool) {
	m.overviewMu.Lock()
	defer m.overviewMu.Unlock()
	cur, ok := m.overview[hostID]
	if !ok {
		return HostOverview{}, false
	}
	cp := make(map[string]int, len(cur.findings))
	for k, v := range cur.findings {
		cp[k] = v
	}
	return HostOverview{
		Findings:     cp,
		Reachable:    cur.reachable,
		Version:      cur.version,
		LastPolledAt: cur.lastPolledAt,
	}, true
}

// HostOverview is what the hub last learned about one host, as handed to
// callers outside this package — a struct rather than a growing list of
// return values, since every field here is optional context about the
// same single observation.
type HostOverview struct {
	Findings  map[string]int
	Reachable bool
	// Version is what the host's own binary reports actually running,
	// which is what makes a silently-failed update visible.
	Version      string
	LastPolledAt time.Time
}
