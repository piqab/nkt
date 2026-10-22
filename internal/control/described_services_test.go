package control

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

// Сервис из описания (svcdesc) в снимке: раздел «Сервисы» с действиями,
// адреса и находки, файл в «Конфигурациях», systemctl через управление
// службами.
func TestDescribedServiceEndToEnd(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "svcdesc", "testdata", "host"))
	cfg := &config.Config{Mode: config.ModeFixtures, FixturesRoot: root, DataDir: t.TempDir(),
		ServicesDir: "/etc/netknownsthat/services.d", CommandTimeout: 5 * time.Second}
	c := collect.NewFixtures(root)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	snap, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var redisUnit string
	var actions []string
	for _, s := range snap.Services {
		if s.Name == "redis" {
			redisUnit, actions = s.Unit, s.Actions
			if !s.Installed || len(s.ConfigFiles) != 1 {
				t.Errorf("redis service: %+v", s)
			}
		}
	}
	if redisUnit != "redis-server" || len(actions) == 0 {
		t.Fatalf("redis in services: %q %v", redisUnit, actions)
	}
	found := false
	for _, f := range snap.Findings {
		found = found || f.Rule == "svc.redis.no-auth-public"
	}
	ep := false
	for _, e := range snap.Endpoints {
		ep = ep || (e.Service == "redis" && e.Port == 6379)
	}
	if !found || !ep {
		t.Errorf("redis finding %v, endpoint %v", found, ep)
	}
	configs := NewConfigManager(cfg, c, db, scanner, NewServiceManager(cfg, c, db))
	if svc, err := configs.ServiceForPath("/etc/redis/redis.conf"); err != nil || svc != "redis" {
		t.Errorf("configs: %q %v", svc, err)
	}
	if _, err := NewServiceManager(cfg, c, db).Action(context.Background(), "admin", "redis", "restart"); err != nil {
		t.Errorf("restart: %v", err)
	}
	if _, err := NewServiceManager(cfg, c, db).Action(context.Background(), "admin", "nope", "restart"); err == nil {
		t.Error("unknown service: want error")
	}
}
