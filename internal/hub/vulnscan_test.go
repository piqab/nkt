package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/secretbox"
)

func TestApplyHostVulnDiffMarksNewAndFixed(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()
	const hostID = int64(1)

	previous := &model.VulnScan{
		Available: true,
		Findings: []model.VulnFinding{
			{ID: "CVE-1", Package: "openssl", Target: ""},
			{ID: "CVE-2", Package: "curl", Target: ""},
		},
	}
	encoded, err := json.Marshal(previous)
	if err != nil {
		t.Fatalf("marshal previous scan: %v", err)
	}
	if err := db.KVSet(ctx, vulnScanKVKeyFor(hostID), string(encoded)); err != nil {
		t.Fatalf("KVSet: %v", err)
	}

	current := []model.VulnFinding{
		{ID: "CVE-1", Package: "openssl", Target: ""}, // still present
		{ID: "CVE-3", Package: "bash", Target: ""},    // new
		// CVE-2/curl no longer present — fixed
	}

	compared, newCount, fixedCount, err := m.applyHostVulnDiff(ctx, hostID, current)
	if err != nil {
		t.Fatalf("applyHostVulnDiff: %v", err)
	}
	if !compared {
		t.Error("compared = false, want true (a previous scan exists)")
	}
	if newCount != 1 {
		t.Errorf("newCount = %d, want 1", newCount)
	}
	if fixedCount != 1 {
		t.Errorf("fixedCount = %d, want 1", fixedCount)
	}
	if !current[1].New {
		t.Error("the CVE-3 finding was not marked New")
	}
	if current[0].New {
		t.Error("the still-present CVE-1 finding was incorrectly marked New")
	}
}

func TestApplyHostVulnDiffNoPreviousScan(t *testing.T) {
	m, _ := newTestManager(t)
	compared, newCount, fixedCount, err := m.applyHostVulnDiff(context.Background(), 999, []model.VulnFinding{
		{ID: "CVE-1", Package: "openssl"},
	})
	if err != nil {
		t.Fatalf("applyHostVulnDiff: %v", err)
	}
	if compared {
		t.Error("compared = true for a host with no previous scan, want false")
	}
	if newCount != 0 || fixedCount != 0 {
		t.Errorf("newCount/fixedCount = %d/%d, want 0/0 when nothing to compare against", newCount, fixedCount)
	}
}

// TestHostVulnStatusFallsBackToPersisted mirrors
// TestVersionStatusReflectsRecordedCheck's own reasoning: HostVulnStatus
// must survive a hub restart (Manager.vulnScans is plain in-memory state)
// by falling back to whatever was last persisted under vulnScanKVKeyFor,
// the same way a standalone nkt's own handleVulnerabilities does.
func TestHostVulnStatusFallsBackToPersisted(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()
	const hostID = int64(42)

	scanning, _, result, _ := m.HostVulnStatus(ctx, hostID)
	if scanning {
		t.Error("scanning = true for a host that was never scanned")
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil before anything was ever persisted", result)
	}

	persisted := &model.VulnScan{Available: true, Findings: []model.VulnFinding{{ID: "CVE-9", Package: "zlib"}}}
	encoded, err := json.Marshal(persisted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := db.KVSet(ctx, vulnScanKVKeyFor(hostID), string(encoded)); err != nil {
		t.Fatalf("KVSet: %v", err)
	}

	// A *fresh* Manager sharing the same db (simulating a hub restart —
	// nothing cached in memory, but the KV row survives) must pick up the
	// persisted scan instead of reporting "never scanned". The key doesn't
	// need to match m's — this path never touches secretbox.
	key2, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	m2 := NewManager(&config.Config{}, db, key2, "test", slog.New(slog.DiscardHandler))
	_, _, result, _ = m2.HostVulnStatus(ctx, hostID)
	if result == nil {
		t.Fatal("result is nil after a restart, want the persisted scan")
	}
	if len(result.Findings) != 1 || result.Findings[0].ID != "CVE-9" {
		t.Errorf("result.Findings = %+v, want the persisted CVE-9 finding", result.Findings)
	}
}
