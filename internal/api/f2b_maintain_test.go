package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

// Устаревший фильтр nkt-manual (без <HOST>) хост заменяет сам и
// перезагружает fail2ban; совпадающий и чужие файлы не трогает.
func TestF2BRefreshOwnFiles(t *testing.T) {
	root := t.TempDir()
	f2b := filepath.Join(root, "fail2ban")
	_ = os.MkdirAll(filepath.Join(f2b, "filter.d"), 0o755)
	old := "[Definition]\nfailregex = ^nkt-manual-never-matches$\n"
	filter := filepath.Join(f2b, filepath.FromSlash(fail2ban.ManualFilterFile))
	_ = os.WriteFile(filter, []byte(old), 0o644)
	other := filepath.Join(f2b, "filter.d", "openvpn.conf")
	_ = os.WriteFile(other, []byte("[Definition]\nfailregex = ^x$\n"), 0o644)

	bin := t.TempDir()
	calls := filepath.Join(root, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\ncase \"$1\" in\n  status) printf 'Status\\n|- Number of jail:\\t1\\n`- Jail list:\\tsshd\\n' ;;\n  *) echo ok ;;\nesac\n"
	_ = os.WriteFile(filepath.Join(bin, "fail2ban-client"), []byte(script), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	c := collect.NewLocal("", "", 10*time.Second)
	cfg := &config.Config{Mode: config.ModeLocal, DataDir: t.TempDir(), Fail2banRoot: f2b, CommandTimeout: 10 * time.Second}
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	svc := control.NewServiceManager(cfg, c, db)
	s := &Server{cfg: cfg, db: db, scanner: scanner, services: svc, configs: control.NewConfigManager(cfg, c, db, scanner, svc)}

	ctx := context.Background()
	if err := s.f2bRefreshOwnFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filter); string(b) != fail2ban.ManualFilterContent {
		t.Fatalf("filter not refreshed: %q", b)
	}
	if b, _ := os.ReadFile(other); !strings.Contains(string(b), "^x$") {
		t.Fatal("foreign file touched")
	}
	log, _ := os.ReadFile(calls)
	if !strings.Contains(string(log), "reload") {
		t.Fatalf("no reload: %s", log)
	}
	// Второй раз — нечего делать: ни записи, ни перезагрузки.
	_ = os.Remove(calls)
	if err := s.f2bRefreshOwnFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if log, _ := os.ReadFile(calls); strings.Contains(string(log), "reload") {
		t.Fatalf("reloaded again: %s", log)
	}
}
