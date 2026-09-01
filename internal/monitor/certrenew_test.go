package monitor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

// copyFixturesRoot copies the repo's fixtures/host tree into a throwaway
// directory. RenewCertbot's recombine step writes through the Collector —
// pointing a test straight at the repo's own fixtures/host would let it
// mutate tracked files that the rest of the suite (and every other
// contributor's working tree) relies on.
func copyFixturesRoot(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("fixtures root: %v", err)
	}
	dst := t.TempDir()
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("копирование фикстур: %v", err)
	}
	return dst
}

func certRenewFixtures(t *testing.T, within time.Duration) *CertRenewer {
	t.Helper()
	root := copyFixturesRoot(t)
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
	services := control.NewServiceManager(cfg, c, db)
	return NewCertRenewer(cfg, scanner, control.NewCertManager(cfg, c, db, services, scanner))
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
