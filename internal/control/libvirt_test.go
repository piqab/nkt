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

// libvirtSetup wires a LibvirtManager against the repo's fixtures tree
// directly — like podmanSetup/lxdSetup, virsh mutations never touch the
// filesystem in fixtures mode, only the mocked command index, so there is
// nothing here that needs isolation. The scanner is pre-scanned so
// UndefineVM can find web-vm's "running" state.
func libvirtSetup(t *testing.T) (*LibvirtManager, *store.DB) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("fixtures root: %v", err)
	}
	cfg := &config.Config{
		Mode:            config.ModeFixtures,
		FixturesRoot:    root,
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		ComposeFiles:    []string{"/srv/docker/docker-compose.yml"},
		LibvirtURI:      "qemu:///system",
		CommandTimeout:  5 * time.Second,
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
	return NewLibvirtManager(cfg, c, db, scanner), db
}

func TestLibvirtVMActionRejectsBadAction(t *testing.T) {
	m, _ := libvirtSetup(t)
	if err := m.VMAction(context.Background(), "test", "web-vm", "stop"); err == nil {
		t.Error("ожидалась ошибка: 'stop' не является допустимым действием (только shutdown/destroy)")
	}
}

func TestLibvirtVMActionRejectsBadName(t *testing.T) {
	m, _ := libvirtSetup(t)
	for _, name := range []string{"", "foo/../bar", "foo bar", "-x"} {
		if err := m.VMAction(context.Background(), "test", name, "start"); err == nil {
			t.Errorf("имя %q: ожидалась ошибка валидации", name)
		}
	}
}

func TestLibvirtVMActionSuccess(t *testing.T) {
	m, db := libvirtSetup(t)
	if err := m.VMAction(context.Background(), "test", "db-vm", "start"); err != nil {
		t.Fatalf("start: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "vm.start", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "db-vm" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

func TestLibvirtSetAutostart(t *testing.T) {
	m, db := libvirtSetup(t)
	if err := m.SetAutostart(context.Background(), "test", "db-vm", true); err != nil {
		t.Fatalf("autostart on: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "vm.autostart-on", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}

	if err := m.SetAutostart(context.Background(), "test", "db-vm", false); err != nil {
		t.Fatalf("autostart off: %v", err)
	}
	entries, err = db.ListAudit(context.Background(), store.AuditFilter{Action: "vm.autostart-off", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

// TestLibvirtUndefineRejectsRunningVM covers the guard that stops a running
// domain from being deleted out from under itself — web-vm is fixtures'
// "running" domain (see scan_test/fixtures .commands), the operator must
// shut it down first as a separate, visible step.
func TestLibvirtUndefineRejectsRunningVM(t *testing.T) {
	m, _ := libvirtSetup(t)
	if err := m.UndefineVM(context.Background(), "test", "web-vm", false); err == nil {
		t.Error("ожидалась ошибка: web-vm запущен")
	}
}

func TestLibvirtUndefineStoppedVM(t *testing.T) {
	m, db := libvirtSetup(t)
	if err := m.UndefineVM(context.Background(), "test", "db-vm", false); err != nil {
		t.Fatalf("undefine: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "vm.undefine", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "db-vm" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

func TestLibvirtUndefineRejectsBadName(t *testing.T) {
	m, _ := libvirtSetup(t)
	if err := m.UndefineVM(context.Background(), "test", "foo/bar", false); err == nil {
		t.Error("ожидалась ошибка валидации имени")
	}
}

func TestLibvirtCreateDiskSuccess(t *testing.T) {
	m, db := libvirtSetup(t)
	if err := m.CreateDisk(context.Background(), "test", "/var/lib/libvirt/images/new-vm.qcow2", 20); err != nil {
		t.Fatalf("create disk: %v", err)
	}
	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "vm.create_disk", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "ok" || entries[0].Target != "/var/lib/libvirt/images/new-vm.qcow2" {
		t.Errorf("неожиданная запись журнала: %+v", entries)
	}
}

// TestLibvirtCreateDiskRejectsPathOutsideImagesRoot covers the sandbox: an
// operator-supplied path must not be able to write a qcow2 header over an
// arbitrary file on the host.
func TestLibvirtCreateDiskRejectsPathOutsideImagesRoot(t *testing.T) {
	m, _ := libvirtSetup(t)
	for _, path := range []string{
		"/etc/passwd",
		"/var/lib/libvirt/images/../../../etc/passwd",
		"/var/lib/libvirt/imagesEVIL/x.qcow2",
		"",
	} {
		if err := m.CreateDisk(context.Background(), "test", path, 10); err == nil {
			t.Errorf("путь %q: ожидалась ошибка — вне /var/lib/libvirt/images", path)
		}
	}
}

func TestLibvirtCreateDiskRejectsBadSize(t *testing.T) {
	m, _ := libvirtSetup(t)
	for _, size := range []int{0, -1, 70000} {
		if err := m.CreateDisk(context.Background(), "test", "/var/lib/libvirt/images/x.qcow2", size); err == nil {
			t.Errorf("размер %d: ожидалась ошибка валидации", size)
		}
	}
}
