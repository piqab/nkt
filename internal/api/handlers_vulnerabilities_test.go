package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/althq/netknownsthat/internal/model"
)

// TestHandleVulnerabilitiesIdleState confirms the shape of the response
// before any scan has ever run — scanning:false, no "scan" key at all
// (rather than a null one), matching the "never ran yet" case the frontend
// needs to tell apart from "ran and found nothing".
func TestHandleVulnerabilitiesIdleState(t *testing.T) {
	s := &Server{}
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
