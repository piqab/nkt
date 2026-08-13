package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/store"
)

// TestHandleListHostsMergesOverview confirms handleListHosts folds in
// whatever Manager.pollOverviews last cached (see overview_poll.go) — a
// host never polled must come back with no "findings" key at all (the
// frontend's signal for "неизвестно", not "zero problems"), and a polled
// one must carry its cached counts/reachability.
func TestHandleListHostsMergesOverview(t *testing.T) {
	m, db := newTestManager(t)
	ctx := t.Context()

	polledID, err := m.AddHost(ctx, "polled", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if err := db.SetHostStatus(ctx, polledID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}
	downID, err := m.AddHost(ctx, "down", "10.0.0.2", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if err := db.SetHostStatus(ctx, downID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}
	unpolledID, err := m.AddHost(ctx, "unpolled", "10.0.0.3", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if err := db.SetHostStatus(ctx, unpolledID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}

	// Whitebox: populate the cache the same way pollHost/recordUnreachable
	// would, without needing a real SSH host for this test.
	now := time.Now()
	m.overviewMu.Lock()
	m.overview[polledID] = hostOverview{
		reachable:    true,
		findings:     map[string]int{"critical": 2, "high": 1},
		lastPolledAt: now,
	}
	// A host that has been polled at least once but is currently
	// unreachable — reachable=false is a real, meaningful value here, not
	// an unset one, and must round-trip through JSON as such (this is
	// exactly the case a plain `bool` with `omitempty` would silently
	// collapse into "never polled").
	m.overview[downID] = hostOverview{
		reachable: false,
		errMsg:    "connection refused",
	}
	m.overviewMu.Unlock()

	srv := New(Deps{DB: db, Hub: m})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hub/hosts", nil)
	srv.handleListHosts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleListHosts: status %d, body %s", rec.Code, rec.Body.String())
	}

	var out []hostWithOverview
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rec.Body.String())
	}
	if len(out) != 3 {
		t.Fatalf("got %d hosts, want 3", len(out))
	}

	var polled, down, unpolled *hostWithOverview
	for i := range out {
		switch out[i].ID {
		case polledID:
			polled = &out[i]
		case downID:
			down = &out[i]
		case unpolledID:
			unpolled = &out[i]
		}
	}
	if polled == nil || down == nil || unpolled == nil {
		t.Fatalf("response missing one of the three hosts: %+v", out)
	}

	if polled.Reachable == nil || !*polled.Reachable {
		t.Errorf("polled host: Reachable = %v, want a present true", polled.Reachable)
	}
	if polled.Findings["critical"] != 2 || polled.Findings["high"] != 1 {
		t.Errorf("polled host: Findings = %+v, want {critical:2 high:1}", polled.Findings)
	}
	if polled.LastPolledAt == "" {
		t.Errorf("polled host: LastPolledAt is empty")
	}

	if down.Reachable == nil || *down.Reachable {
		t.Errorf("down host: Reachable = %v, want a present false — this is the bug omitempty-on-bool would reintroduce", down.Reachable)
	}

	if unpolled.Findings != nil {
		t.Errorf("unpolled host: Findings = %+v, want nil (never polled)", unpolled.Findings)
	}
	if unpolled.Reachable != nil {
		t.Errorf("unpolled host: Reachable = %v, want nil (never polled, not just false)", unpolled.Reachable)
	}
}
