package control

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/store"
)

func firewalldSetup(t *testing.T, c collect.Collector) *FirewalldManager {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewFirewalldManager(&config.Config{}, c, db)
}

// TestFirewalldAddRuleBothStores locks in the two-call shape a "apply now
// and persist" request takes: firewall-cmd has no single flag meaning
// both, so Runtime+Permanent both true must produce exactly two calls, one
// per store, runtime first.
func TestFirewalldAddRuleBothStores(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	f := firewalldSetup(t, rec)

	_, err = f.AddRule(t.Context(), "test", FirewalldPortSpec{
		Zone: "public", Port: 8080, Protocol: "tcp", Runtime: true, Permanent: true,
	})
	if err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("got %d firewall-cmd calls, want 2: %v", len(rec.calls), rec.calls)
	}
	want1 := []string{"firewall-cmd", "--zone=public", "--add-port=8080/tcp"}
	want2 := []string{"firewall-cmd", "--permanent", "--zone=public", "--add-port=8080/tcp"}
	if strings.Join(rec.calls[0], " ") != strings.Join(want1, " ") {
		t.Errorf("calls[0] = %v, want %v", rec.calls[0], want1)
	}
	if strings.Join(rec.calls[1], " ") != strings.Join(want2, " ") {
		t.Errorf("calls[1] = %v, want %v", rec.calls[1], want2)
	}
}

// TestFirewalldAddRuleRuntimeOnly confirms a Permanent:false request never
// touches the permanent store at all — a deliberately temporary rule must
// not survive a reload.
func TestFirewalldAddRuleRuntimeOnly(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	f := firewalldSetup(t, rec)

	_, err = f.AddRule(t.Context(), "test", FirewalldPortSpec{
		Zone: "public", Service: "ssh", Runtime: true,
	})
	if err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("got %d firewall-cmd calls, want 1: %v", len(rec.calls), rec.calls)
	}
	want := []string{"firewall-cmd", "--zone=public", "--add-service=ssh"}
	if strings.Join(rec.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("calls[0] = %v, want %v", rec.calls[0], want)
	}
}

// TestFirewalldDeleteRuleUsesRemoveFlags mirrors AddRule's argv shape for
// the opposite verb.
func TestFirewalldDeleteRuleUsesRemoveFlags(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	f := firewalldSetup(t, rec)

	_, err = f.DeleteRule(t.Context(), "test", FirewalldPortSpec{
		Zone: "public", Port: 8443, Protocol: "tcp", Permanent: true,
	})
	if err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("got %d firewall-cmd calls, want 1: %v", len(rec.calls), rec.calls)
	}
	want := []string{"firewall-cmd", "--permanent", "--zone=public", "--remove-port=8443/tcp"}
	if strings.Join(rec.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("calls[0] = %v, want %v", rec.calls[0], want)
	}
}

// TestFirewalldPortSpecValidateRejectsBadInput covers the validation gate
// AddRule/DeleteRule both go through before ever touching firewall-cmd.
func TestFirewalldPortSpecValidateRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		spec FirewalldPortSpec
	}{
		{"empty zone", FirewalldPortSpec{Port: 80, Protocol: "tcp", Runtime: true}},
		{"bad zone chars", FirewalldPortSpec{Zone: "pub lic", Port: 80, Protocol: "tcp", Runtime: true}},
		{"port and service both set", FirewalldPortSpec{Zone: "public", Port: 80, Protocol: "tcp", Service: "ssh", Runtime: true}},
		{"port out of range", FirewalldPortSpec{Zone: "public", Port: 99999, Protocol: "tcp", Runtime: true}},
		{"bad protocol", FirewalldPortSpec{Zone: "public", Port: 80, Protocol: "sctp", Runtime: true}},
		{"neither store selected", FirewalldPortSpec{Zone: "public", Port: 80, Protocol: "tcp"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.spec.Validate(); err == nil {
				t.Errorf("Validate() accepted %+v", c.spec)
			}
		})
	}
}

// TestFirewalldReload locks in the exact command Reload runs.
func TestFirewalldReload(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	f := firewalldSetup(t, rec)

	if _, err := f.Reload(t.Context(), "test"); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(rec.calls) != 1 || strings.Join(rec.calls[0], " ") != "firewall-cmd --reload" {
		t.Errorf("calls = %v, want exactly [firewall-cmd --reload]", rec.calls)
	}
}
