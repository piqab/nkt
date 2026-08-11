package control

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/store"
)

// lxdSetup wires an LXDManager against the repo's fixtures tree directly —
// like podmanSetup, lxc mutations never touch the filesystem, only the
// mocked command index, so there is nothing here that needs isolation.
func lxdSetup(t *testing.T) (*LXDManager, *store.DB) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("fixtures root: %v", err)
	}
	c := collect.NewFixtures(root)
	dbPath := filepath.Join(t.TempDir(), "nkt.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewLXDManager(c, db), db
}

func TestLXDInstanceActionRejectsBadAction(t *testing.T) {
	m, _ := lxdSetup(t)
	if err := m.InstanceAction(context.Background(), "test", "build-runner", "delete"); err == nil {
		t.Error("ожидалась ошибка для недопустимого действия")
	}
}

func TestLXDInstanceActionRejectsBadName(t *testing.T) {
	m, _ := lxdSetup(t)
	for _, name := range []string{"", "foo/../bar", "foo?x=1", "foo&bar", "foo#bar", "foo bar"} {
		if err := m.InstanceAction(context.Background(), "test", name, "start"); err == nil {
			t.Errorf("имя %q: ожидалась ошибка валидации", name)
		}
	}
}

func TestLXDInstanceActionSuccess(t *testing.T) {
	m, db := lxdSetup(t)
	if err := m.InstanceAction(context.Background(), "test", "build-runner", "restart"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "lxd.restart", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "build-runner" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

func TestLXDCreateInstance(t *testing.T) {
	m, db := lxdSetup(t)
	if err := m.CreateInstance(context.Background(), "test", "ubuntu:24.04", "new-runner"); err != nil {
		t.Fatalf("create: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "lxd.create", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "new-runner" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

func TestLXDCreateInstanceRejectsMissingFields(t *testing.T) {
	m, _ := lxdSetup(t)
	if err := m.CreateInstance(context.Background(), "test", "", "new-runner"); err == nil {
		t.Error("ожидалась ошибка при пустом образе")
	}
	if err := m.CreateInstance(context.Background(), "test", "ubuntu:24.04", ""); err == nil {
		t.Error("ожидалась ошибка при пустом имени")
	}
}

func TestLXDDeleteInstance(t *testing.T) {
	m, db := lxdSetup(t)
	if err := m.DeleteInstance(context.Background(), "test", "win-testbed", false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "lxd.delete", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "win-testbed" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

func TestLXDDeleteInstanceRejectsBadName(t *testing.T) {
	m, _ := lxdSetup(t)
	if err := m.DeleteInstance(context.Background(), "test", "foo/bar", false); err == nil {
		t.Error("ожидалась ошибка валидации имени")
	}
}
