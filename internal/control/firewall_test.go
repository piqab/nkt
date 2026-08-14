package control

import (
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/store"
)

func firewallSetup(t *testing.T, c collect.Collector) *FirewallManager {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewFirewallManager(&config.Config{}, c, db)
}

// TestAddedRulesParsesBothForms covers `ufw show added`'s two output
// shapes: a plain "allow 80/tcp" and the longer "allow from X to any port
// Y proto Z" a source-restricted rule takes. This is the one thing that
// still reports a rule's existence while ufw itself is inactive —
// `ufw status numbered` (NumberedRules) prints nothing at all then, which
// is exactly the gap that made the web UI's "разрешить" button look like
// it did nothing on a host where ufw was never turned on: the rule really
// was added, there was just no way left to see it.
func TestAddedRulesParsesBothForms(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	f := firewallSetup(t, collect.NewFixtures(root))

	rules, err := f.AddedRules(t.Context())
	if err != nil {
		t.Fatalf("AddedRules: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3: %+v", len(rules), rules)
	}

	if rules[0].Action != "allow" || rules[0].Port != 22 || rules[0].Protocol != "tcp" {
		t.Errorf("rules[0] = %+v, want allow 22/tcp", rules[0])
	}
	if rules[1].Action != "allow" || rules[1].Port != 80 || rules[1].Protocol != "tcp" {
		t.Errorf("rules[1] = %+v, want allow 80/tcp", rules[1])
	}
	// The "from X to any port Y proto Z" form — a source-restricted rule.
	if rules[2].Action != "allow" || rules[2].Port != 8080 || rules[2].Protocol != "tcp" {
		t.Errorf("rules[2] = %+v, want allow .../8080 proto tcp", rules[2])
	}
	if rules[2].Spec == "" {
		t.Error("rules[2].Spec is empty — the raw form must survive even when structured fields are extracted")
	}
}

// TestDeleteRuleBySpecUsesForceDelete locks in the exact argv ufw needs:
// "--force" ahead of "delete" (a bare "ufw delete <spec>" prompts
// interactively for confirmation, which would hang a non-interactive
// caller), followed by the same spec AddRule would have used to create
// the rule in the first place.
func TestDeleteRuleBySpecUsesForceDelete(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	f := firewallSetup(t, rec)

	_, err = f.DeleteRuleBySpec(t.Context(), "test", RuleSpec{Action: "allow", Port: 80, Protocol: "tcp"})
	if err != nil {
		t.Fatalf("DeleteRuleBySpec: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("got %d ufw calls, want 1: %v", len(rec.calls), rec.calls)
	}
	got := rec.calls[0]
	want := []string{"ufw", "--force", "delete", "allow", "80/tcp"}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestDeleteRuleBySpecRejectsInvalidSpec confirms it goes through the same
// Validate() AddRule does — a spec good enough to delete must have been
// good enough to add in the first place.
func TestDeleteRuleBySpecRejectsInvalidSpec(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	f := firewallSetup(t, collect.NewFixtures(root))

	if _, err := f.DeleteRuleBySpec(t.Context(), "test", RuleSpec{Action: "allow", Port: 99999, Protocol: "tcp"}); err == nil {
		t.Error("DeleteRuleBySpec accepted an out-of-range port")
	}
}

// TestNumberedDeleteUsesForceDelete is a regression test for a bug that
// predates this change and had nothing exercising it: DeleteRule already
// called `ufw --force delete <number>`, but the fixtures stub matched
// on ["ufw", "delete"] — one token short, since matchesPrefix checks
// position by position and argv[1] is "--force", not "delete". The stub
// never actually matched, silently falling through to a 127 "command not
// found" that nothing was checking for.
func TestNumberedDeleteUsesForceDelete(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	f := firewallSetup(t, collect.NewFixtures(root))

	res, err := f.DeleteRule(t.Context(), "test", 1, "22/tcp                     ALLOW IN    Anywhere")
	if err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	if !res.OK() {
		t.Errorf("DeleteRule result not OK: exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
}
