package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/store"
)

// ufw изнутри песочницы не пишет /etc/ufw — правило добавляется в обход.
func TestUFWEscapeOnReadOnly(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".commands"), 0o755)
	idx := map[string]any{"commands": []map[string]any{{
		"match":     []string{"ufw", "allow"},
		"stderr":    "ERROR: '/etc/ufw/user.rules' is not writable",
		"exit_code": 1,
	}}}
	raw, _ := json.Marshal(idx)
	_ = os.WriteFile(filepath.Join(root, ".commands", "index.json"), raw, 0o644)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := NewFirewallManager(&config.Config{}, collect.NewFixtures(root), db)
	if _, err := f.AddRule(context.Background(), "admin", RuleSpec{Action: "allow", Port: 80, Protocol: "tcp"}); err == nil {
		t.Fatal("read-only ufw without escape accepted")
	}
	var escaped []string
	f.SetEscape(func(ctx context.Context, argv ...string) (collect.CommandResult, error) {
		escaped = argv
		return collect.CommandResult{Argv: argv}, nil
	})
	if _, err := f.AddRule(context.Background(), "admin", RuleSpec{Action: "allow", Port: 80, Protocol: "tcp"}); err != nil {
		t.Fatal(err)
	}
	if len(escaped) < 2 || filepath.Base(escaped[0]) != "ufw" || escaped[1] != "allow" {
		t.Fatalf("escape argv: %v", escaped)
	}
	if !f.Writable() {
		t.Fatal("writable with escape")
	}
}
