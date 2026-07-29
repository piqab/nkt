package monitor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/store"
)

func certRenewFixtures(t *testing.T, within time.Duration) *CertRenewer {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("fixtures root: %v", err)
	}
	cfg := &config.Config{
		Mode:                 config.ModeFixtures,
		FixturesRoot:         root,
		NginxMainConfig:      "/etc/nginx/nginx.conf",
		HAProxyMainConf:      "/etc/haproxy/haproxy.cfg",
		ComposeFiles:         []string{"/srv/docker/docker-compose.yml"},
		CommandTimeout:       5 * time.Second,
		AutoRenewCertsWithin: within,
	}
	c := collect.NewFixtures(root)
	dbPath := filepath.Join(t.TempDir(), "nkt.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	scanner := inventory.New(cfg, c, db)
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("скан: %v", err)
	}
	return NewCertRenewer(cfg, scanner, control.NewCertManager(cfg, c, db))
}

// app.example.com's fixture certificate is valid until 2035 — nowhere near
// due — so with the default-sized window the job must leave it alone.
func TestCertRenewerSkipsCertsNotDueYet(t *testing.T) {
	r := certRenewFixtures(t, 30*24*time.Hour)
	n, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("продлений: %d, ожидалось 0 — ни один сертификат ещё не должен быть due", n)
	}
}

// Widening the window past 2035 makes app.example.com "due"; the job must
// then call certbot for it (fixtures/.commands has a canned success for
// exactly this lineage) and skip api.example.com, which has no renewal.conf
// and so is not certbot-Managed at all.
func TestCertRenewerRenewsDueLineage(t *testing.T) {
	r := certRenewFixtures(t, 20*365*24*time.Hour)
	n, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("продлений: %d, ожидалась 1 (app.example.com)", n)
	}
}
