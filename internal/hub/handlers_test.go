package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/store"
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

// TestExportImportHostsHandlersRoundTrip exercises the HTTP layer end to
// end: export one host from a real server, POST that exact response body
// to a second server's import handler, and confirm it landed — this is
// what actually proves handleExportHosts and handleImportHosts agree on
// the wire format, not just that store.ExportHosts/ImportHosts do
// (already covered directly in internal/store).
func TestExportImportHostsHandlersRoundTrip(t *testing.T) {
	m, db := newTestManager(t)
	ctx := t.Context()

	id, err := m.AddHost(ctx, "h1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", true)
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if err := db.SetHostStatus(ctx, id, store.HostStatusOnline, ""); err != nil {
		t.Fatalf("SetHostStatus: %v", err)
	}

	srv := New(Deps{DB: db, Hub: m})

	exportRec := httptest.NewRecorder()
	srv.handleExportHosts(exportRec, httptest.NewRequest(http.MethodGet, "/api/hub/export", nil))
	if exportRec.Code != http.StatusOK {
		t.Fatalf("handleExportHosts: status %d, body %s", exportRec.Code, exportRec.Body.String())
	}
	if cd := exportRec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}

	m2, db2 := newTestManager(t)
	srv2 := New(Deps{DB: db2, Hub: m2})

	importRec := httptest.NewRecorder()
	importReq := httptest.NewRequest(http.MethodPost, "/api/hub/import", exportRec.Body)
	srv2.handleImportHosts(importRec, importReq)
	if importRec.Code != http.StatusOK {
		t.Fatalf("handleImportHosts: status %d, body %s", importRec.Code, importRec.Body.String())
	}

	var result struct {
		Imported int      `json:"imported"`
		Errors   []string `json:"errors"`
	}
	if err := json.Unmarshal(importRec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if result.Imported != 1 || len(result.Errors) != 0 {
		t.Fatalf("import result = %+v, want 1 imported and no errors", result)
	}

	hosts, err := db2.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts on the importing hub: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Name != "h1" || !hosts[0].TerminalEnabled {
		t.Errorf("imported host = %+v, want h1 with TerminalEnabled=true", hosts)
	}
}
