package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

// categoriesSetup строит менеджер поверх временного дерева с файлами всех
// категорий — включая тот самый случай, ради которого всё затевалось: файл
// в sites-available, до которого конфигурация nginx не дотягивается.
func categoriesSetup(t *testing.T) *ConfigManager {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("подготовить каталог для %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("подготовить %s: %v", rel, err)
		}
	}
	write("etc/nginx/nginx.conf", "http {\n  include /etc/nginx/sites-enabled/*.conf;\n}\n")
	write("etc/nginx/sites-enabled/live.conf", "server { listen 80; server_name live.example; }\n")
	write("etc/nginx/sites-available/dormant.conf", "server { listen 81; server_name dormant.example; }\n")
	write("etc/ssh/sshd_config", "Port 22\n")
	write("etc/systemd/system/demo.service", "[Service]\nExecStart=/bin/true\n")
	write("etc/netplan/01-net.yaml", "network:\n  version: 2\n")
	write("etc/sysctl.d/99-forward.conf", "net.ipv4.ip_forward=1\n")
	write("etc/cron.d/demo", "*/5 * * * * root /bin/true\n")

	cfg := &config.Config{
		Mode: config.ModeFixtures, FixturesRoot: root, DataDir: t.TempDir(),
		NginxRoot: "/etc/nginx", NginxMainConfig: "/etc/nginx/nginx.conf",
		SSHRoot: "/etc/ssh", SystemdUnitRoot: "/etc/systemd/system",
		NetplanRoot: "/etc/netplan", SysctlRoot: "/etc/sysctl.d", CronRoot: "/etc/cron.d",
		CommandTimeout: 5 * time.Second,
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c := collect.NewFixtures(root)
	scanner := inventory.New(cfg, c, db)
	return NewConfigManager(cfg, c, db, scanner, NewServiceManager(cfg, c, db))
}

func TestServiceForPathCategories(t *testing.T) {
	m := categoriesSetup(t)

	allowed := map[string]string{
		"/etc/ssh/sshd_config":                 "ssh",
		"/etc/ssh/sshd_config.d/10-local.conf": "ssh",
		"/etc/systemd/system/demo.service":     "systemd",
		"/etc/netplan/01-net.yaml":             "network",
		"/etc/hosts":                           "network",
		"/etc/resolv.conf":                     "network",
		"/etc/sysctl.d/99-forward.conf":        "sysctl",
		"/etc/sysctl.conf":                     "sysctl",
		"/etc/cron.d/demo":                     "cron",
		"/etc/crontab":                         "cron",
	}
	for path, want := range allowed {
		got, err := m.ServiceForPath(path)
		if err != nil {
			t.Errorf("ServiceForPath(%q) = ошибка %v", path, err)
			continue
		}
		if got != want {
			t.Errorf("ServiceForPath(%q) = %q, want %q", path, got, want)
		}
	}

	// Расширение категорий не должно превратиться в право править /etc
	// целиком — это единственное, что отделяет редактор от shadow.
	for _, path := range []string{"/etc/shadow", "/etc/passwd", "/etc/sudoers", "/root/.ssh/authorized_keys"} {
		if _, err := m.ServiceForPath(path); err == nil {
			t.Errorf("ServiceForPath(%q) не отклонён", path)
		}
	}
}

// Файл, до которого конфигурация службы не дотягивается, обязан быть в
// списке: именно он раньше исчезал сразу после создания.
func TestListIncludesFilesOutsideTheParsedConfig(t *testing.T) {
	m := categoriesSetup(t)

	files, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byPath := map[string]bool{}
	inUse := map[string]bool{}
	for _, f := range files {
		byPath[f.Path] = true
		inUse[f.Path] = f.InUse
	}

	for _, want := range []string{
		"/etc/nginx/sites-available/dormant.conf",
		"/etc/ssh/sshd_config",
		"/etc/systemd/system/demo.service",
		"/etc/netplan/01-net.yaml",
		"/etc/sysctl.d/99-forward.conf",
		"/etc/cron.d/demo",
	} {
		if !byPath[want] {
			t.Errorf("%s нет в списке", want)
		}
	}

	// «Не подключён» — про разбираемые службы: sites-available без ссылки
	// в sites-enabled действительно не участвует в работе nginx, а вот
	// sshd_config работает сам по себе, его никто ниоткуда не включает.
	if inUse["/etc/nginx/sites-available/dormant.conf"] {
		t.Error("файл в sites-available помечен как участвующий в конфигурации")
	}
	if !inUse["/etc/ssh/sshd_config"] {
		t.Error("sshd_config помечен как не подключённый")
	}
}
