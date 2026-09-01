package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

// TestHandleVulnerabilitiesIdleState confirms the shape of the response
// before any scan has ever run — scanning:false, no "scan" key at all
// (rather than a null one), matching the "never ran yet" case the frontend
// needs to tell apart from "ran and found nothing".
func TestHandleVulnerabilitiesIdleState(t *testing.T) {
	s := newTestServerWithDB(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vulnerabilities", nil)
	rec := httptest.NewRecorder()

	s.handleVulnerabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["scanning"] != false {
		t.Errorf("scanning = %v, want false", got["scanning"])
	}
	if _, ok := got["scan"]; ok {
		t.Errorf("body has a \"scan\" key before any scan ran: %v", got)
	}
}

// TestHandleVulnerabilitiesReportsLastResult confirms a completed scan's
// result is what handleVulnerabilities hands back — the actual scan/trivy
// machinery is exercised directly and thoroughly in internal/vuln's own
// tests (including a real end-to-end run behind NKT_TEST_LIVE_VULN=1); this
// only checks the handler wires vuln.result through correctly.
func TestHandleVulnerabilitiesReportsLastResult(t *testing.T) {
	s := &Server{}
	s.vuln.result = &model.VulnScan{
		Available: true,
		Findings:  []model.VulnFinding{{ID: "CVE-2024-1", Package: "openssl", Severity: "HIGH"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/vulnerabilities", nil)
	rec := httptest.NewRecorder()
	s.handleVulnerabilities(rec, req)

	var got struct {
		Scan model.VulnScan `json:"scan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Scan.Available || len(got.Scan.Findings) != 1 || got.Scan.Findings[0].ID != "CVE-2024-1" {
		t.Errorf("scan = %+v, unexpected", got.Scan)
	}
}

// TestHandleVulnScanStartRejectsConcurrentScan confirms a second start
// request while one is already running gets a 409, not a duplicate
// goroutine racing the first — StartVulnScan itself is a fire-and-forget
// background operation, so this gate is the only thing preventing two
// scans (and two trivy DB downloads) from running at once.
func TestHandleVulnScanStartRejectsConcurrentScan(t *testing.T) {
	s := &Server{}
	s.vuln.scanning = true

	req := httptest.NewRequest(http.MethodPost, "/api/vulnerabilities/scan", nil)
	rec := httptest.NewRecorder()
	s.handleVulnScanStart(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
}

func newTestServerWithDB(t *testing.T) *Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Server{db: db}
}

// TestApplyVulnDiffFirstScan confirms the very first scan on a host — no
// previous scan persisted at all — reports Compared=false and marks
// nothing New, rather than either erroring (no previous row is not a
// failure) or (worse) treating every finding as "new" just because there
// was nothing to diff against yet.
func TestApplyVulnDiffFirstScan(t *testing.T) {
	s := newTestServerWithDB(t)
	findings := []model.VulnFinding{
		{ID: "CVE-2024-1", Package: "openssl"},
		{ID: "CVE-2024-2", Package: "bash"},
	}

	compared, newCount, fixedCount, err := s.applyVulnDiff(context.Background(), findings)
	if err != nil {
		t.Fatalf("applyVulnDiff: %v", err)
	}
	if compared {
		t.Error("compared = true on the very first scan, want false")
	}
	if newCount != 0 || fixedCount != 0 {
		t.Errorf("newCount=%d fixedCount=%d, want 0/0 on the first scan", newCount, fixedCount)
	}
	for _, f := range findings {
		if f.New {
			t.Errorf("finding %+v marked New on the first scan", f)
		}
	}
}

// persistVulnScan is the test-side equivalent of what runVulnScan itself
// does after a real scan (see handlers_vulnerabilities.go) — applyVulnDiff
// no longer persists anything on its own, only reads whatever the previous
// scan already left behind.
func persistVulnScan(t *testing.T, s *Server, findings []model.VulnFinding) {
	t.Helper()
	encoded, err := json.Marshal(&model.VulnScan{Available: true, Findings: findings})
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}
	if err := s.db.KVSet(context.Background(), vulnScanKVKey, string(encoded)); err != nil {
		t.Fatalf("KVSet: %v", err)
	}
}

// TestApplyVulnDiffSecondScan covers the actual point of this feature: a
// CVE that newly appeared gets New=true, one that's gone since the last
// scan is counted as fixed, and one present in both scans is neither.
func TestApplyVulnDiffSecondScan(t *testing.T) {
	s := newTestServerWithDB(t)
	ctx := context.Background()

	persistVulnScan(t, s, []model.VulnFinding{
		{ID: "CVE-2024-1", Package: "openssl"}, // survives into the second scan unchanged
		{ID: "CVE-2024-2", Package: "bash"},    // fixed by the second scan
	})

	second := []model.VulnFinding{
		{ID: "CVE-2024-1", Package: "openssl"}, // unchanged
		{ID: "CVE-2024-3", Package: "curl"},    // newly appeared
	}
	compared, newCount, fixedCount, err := s.applyVulnDiff(ctx, second)
	if err != nil {
		t.Fatalf("applyVulnDiff (second): %v", err)
	}
	if !compared {
		t.Fatal("compared = false on the second scan, want true")
	}
	if newCount != 1 {
		t.Errorf("newCount = %d, want 1", newCount)
	}
	if fixedCount != 1 {
		t.Errorf("fixedCount = %d, want 1", fixedCount)
	}

	byKey := map[string]bool{}
	for _, f := range second {
		byKey[findingKey(f)] = f.New
	}
	if byKey[findingKey(model.VulnFinding{ID: "CVE-2024-1", Package: "openssl"})] {
		t.Error("CVE-2024-1/openssl marked New, but it was already present in the first scan")
	}
	if !byKey[findingKey(model.VulnFinding{ID: "CVE-2024-3", Package: "curl"})] {
		t.Error("CVE-2024-3/curl not marked New, but it was absent from the first scan")
	}
}

// TestHandleVulnerabilitiesFallsBackToPersistedScan is the actual point of
// vulnScanKVKey's existence: Server.vuln.result is nil (as it always is on
// a fresh process, whether or not this host has scanned before) but a scan
// was persisted by an earlier process — handleVulnerabilities must recover
// it rather than reporting "never scanned" and prompting a needless
// re-scan just because nkt's own process restarted (or, in practice, just
// as easily: the user navigated away from Уязвимости and back, in a setup
// where something in between recycled the process).
func TestHandleVulnerabilitiesFallsBackToPersistedScan(t *testing.T) {
	s := newTestServerWithDB(t)
	persistVulnScan(t, s, []model.VulnFinding{{ID: "CVE-2024-1", Package: "openssl", Severity: "HIGH"}})

	req := httptest.NewRequest(http.MethodGet, "/api/vulnerabilities", nil)
	rec := httptest.NewRecorder()
	s.handleVulnerabilities(rec, req)

	var got struct {
		Scan model.VulnScan `json:"scan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Scan.Available || len(got.Scan.Findings) != 1 || got.Scan.Findings[0].ID != "CVE-2024-1" {
		t.Errorf("scan = %+v, want the persisted scan recovered", got.Scan)
	}

	// Also cached back into memory, so a second GET doesn't need another
	// kv round trip.
	s.vuln.mu.Lock()
	cached := s.vuln.result
	s.vuln.mu.Unlock()
	if cached == nil {
		t.Error("handleVulnerabilities did not cache the recovered scan into Server.vuln.result")
	}
}

// TestHandleVulnerabilitiesNoFallbackWhileScanning confirms the KV
// fallback only kicks in for the genuine "nothing in memory" case — a scan
// actively running (Server.vuln.scanning true, result still nil) must not
// have some stale persisted scan from before injected mid-flight.
func TestHandleVulnerabilitiesNoFallbackWhileScanning(t *testing.T) {
	s := newTestServerWithDB(t)
	persistVulnScan(t, s, []model.VulnFinding{{ID: "CVE-2024-1", Package: "openssl"}})
	s.vuln.scanning = true

	req := httptest.NewRequest(http.MethodGet, "/api/vulnerabilities", nil)
	rec := httptest.NewRecorder()
	s.handleVulnerabilities(rec, req)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["scan"]; ok {
		t.Errorf("body has a \"scan\" key while a scan is actively running: %v", got)
	}
}

// TestFindingKeyDistinguishesPackages confirms the same CVE ID against two
// different packages is treated as two distinct findings, not conflated —
// e.g. a libc CVE that shows up against both libc6 and libc6-dev.
func TestFindingKeyDistinguishesPackages(t *testing.T) {
	a := findingKey(model.VulnFinding{ID: "CVE-2024-1", Package: "libc6"})
	b := findingKey(model.VulnFinding{ID: "CVE-2024-1", Package: "libc6-dev"})
	if a == b {
		t.Errorf("findingKey collided for the same CVE against two different packages: %q", a)
	}
}
