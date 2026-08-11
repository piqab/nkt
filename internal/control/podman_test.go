package control

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/store"
)

// podmanSetup wires a PodmanManager against the repo's fixtures tree
// directly — PodmanAPI mutations never touch the filesystem (they hit the
// mocked engine socket), so unlike renewSetup there is nothing here that
// needs an isolated copy.
func podmanSetup(t *testing.T) (*PodmanManager, *store.DB) {
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
	return NewPodmanManager(c, db), db
}

func TestPodmanContainerActionRejectsBadAction(t *testing.T) {
	m, _ := podmanSetup(t)
	if err := m.ContainerAction(context.Background(), "test", "monitoring-grafana", "delete"); err == nil {
		t.Error("ожидалась ошибка для недопустимого действия")
	}
}

func TestPodmanContainerActionRejectsBadName(t *testing.T) {
	m, _ := podmanSetup(t)
	for _, name := range []string{"", "foo/../bar", "foo?x=1", "foo&bar", "foo#bar"} {
		if err := m.ContainerAction(context.Background(), "test", name, "start"); err == nil {
			t.Errorf("имя %q: ожидалась ошибка валидации", name)
		}
	}
}

func TestPodmanContainerActionSuccess(t *testing.T) {
	m, db := podmanSetup(t)
	if err := m.ContainerAction(context.Background(), "test", "monitoring-grafana", "restart"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "podman.restart", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "monitoring-grafana" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

func TestPodmanCreateContainerStartsIt(t *testing.T) {
	m, db := podmanSetup(t)
	if err := m.CreateContainer(context.Background(), "test", "docker.io/library/nginx:1.27", "new-web"); err != nil {
		t.Fatalf("create: %v", err)
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	var sawCreate, sawStart bool
	for _, e := range entries {
		if e.Action == "podman.create" && e.Target == "new-web" && e.Result == "ok" {
			sawCreate = true
		}
		// CreateContainer starts by the engine-assigned ID, not the name —
		// the fixture always hands back the same canned ID.
		if e.Action == "podman.start" && e.Result == "ok" {
			sawStart = true
		}
	}
	if !sawCreate {
		t.Error("в журнале нет записи podman.create для new-web")
	}
	if !sawStart {
		t.Error("в журнале нет записи podman.start после создания")
	}
}

func TestPodmanCreateContainerRejectsMissingFields(t *testing.T) {
	m, _ := podmanSetup(t)
	if err := m.CreateContainer(context.Background(), "test", "", "new-web"); err == nil {
		t.Error("ожидалась ошибка при пустом образе")
	}
	if err := m.CreateContainer(context.Background(), "test", "nginx", ""); err == nil {
		t.Error("ожидалась ошибка при пустом имени")
	}
}

func TestPodmanDeleteContainer(t *testing.T) {
	m, db := podmanSetup(t)
	if err := m.DeleteContainer(context.Background(), "test", "adhoc-backup", false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "podman.delete", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "adhoc-backup" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

func TestPodmanDeleteContainerRejectsBadName(t *testing.T) {
	m, _ := podmanSetup(t)
	if err := m.DeleteContainer(context.Background(), "test", "foo/bar", false); err == nil {
		t.Error("ожидалась ошибка валидации имени")
	}
}
