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

	polledID, err := m.AddHost(ctx, "polled", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if err := db.SetHostStatus(ctx, polledID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}
	unpolledID, err := m.AddHost(ctx, "unpolled", "10.0.0.2", 22, "root", store.HostAuthPassword, "pw")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if err := db.SetHostStatus(ctx, unpolledID, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}

	// Whitebox: populate the cache the same way pollHost would, without
	// needing a real SSH host for this test.
	now := time.Now()
	m.overviewMu.Lock()
	m.overview[polledID] = hostOverview{
		reachable:    true,
		findings:     map[string]int{"critical": 2, "high": 1},
		lastPolledAt: now,
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
	if len(out) != 2 {
		t.Fatalf("got %d hosts, want 2", len(out))
	}

	var polled, unpolled *hostWithOverview
	for i := range out {
		switch out[i].ID {
		case polledID:
			polled = &out[i]
		case unpolledID:
			unpolled = &out[i]
		}
	}
	if polled == nil || unpolled == nil {
		t.Fatalf("response missing one of the two hosts: %+v", out)
	}

	if !polled.Reachable {
		t.Errorf("polled host: Reachable = false, want true")
	}
	if polled.Findings["critical"] != 2 || polled.Findings["high"] != 1 {
		t.Errorf("polled host: Findings = %+v, want {critical:2 high:1}", polled.Findings)
	}
	if polled.LastPolledAt == "" {
		t.Errorf("polled host: LastPolledAt is empty")
	}

	if unpolled.Findings != nil {
		t.Errorf("unpolled host: Findings = %+v, want nil (never polled)", unpolled.Findings)
	}
	if unpolled.Reachable {
		t.Errorf("unpolled host: Reachable = true, want false (never polled)")
	}
}
