package inventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/model"
)

// Встроенные разборщики через описания: выключить, сменить путь,
// забрать файл приоритетом.
func TestEngineDescriptions(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "fixtures", "host"))); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "etc", "netknownsthat", "services.d")
	_ = os.MkdirAll(dir, 0o755)
	put := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scan := func() *model.Snapshot {
		t.Helper()
		cfg := &config.Config{
			Mode: config.ModeFixtures, FixturesRoot: root, DataDir: t.TempDir(),
			NginxMainConfig: "/etc/nginx/nginx.conf", HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
			ComposeFiles: []string{"/srv/docker/docker-compose.yml"}, LibvirtURI: "qemu:///system",
			ServicesDir: "/etc/netknownsthat/services.d", CommandTimeout: 5 * time.Second,
		}
		snap, err := New(cfg, collect.NewFixtures(root), nil).Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	count := func(snap *model.Snapshot, service string) int {
		n := 0
		for _, e := range snap.Endpoints {
			if e.Service == service {
				n++
			}
		}
		return n
	}
	base := scan()
	if count(base, "nginx") == 0 || len(base.Container) == 0 {
		t.Fatalf("base: nginx %d, containers %d", count(base, "nginx"), len(base.Container))
	}

	// Выключить docker и сменить главный файл nginx на несуществующий.
	put("docker.yaml", "name: docker\ndisabled: true\n")
	put("nginx.yaml", "name: nginx\nengine: nginx\noptions:\n  main_config: /etc/nginx-other/nginx.conf\n")
	snap := scan()
	if len(snap.Container) != 0 || count(snap, "docker") != 0 {
		t.Errorf("docker off: %d containers", len(snap.Container))
	}
	if count(snap, "nginx") != 0 {
		t.Errorf("nginx with another main_config: %d endpoints", count(snap, "nginx"))
	}
	var off bool
	for _, s := range snap.Sources {
		if s.Name == "docker" && !s.Available && len(s.Warnings) == 1 && strings.Contains(s.Warnings[0], "docker.yaml") {
			off = true
		}
	}
	if !off {
		t.Errorf("docker source should say who turned it off: %+v", snap.Sources)
	}

	// Описание с priority выше встроенного забирает его файл.
	_ = os.Remove(filepath.Join(dir, "docker.yaml"))
	_ = os.Remove(filepath.Join(dir, "nginx.yaml"))
	put("mynginx.yaml", "name: mynginx\npriority: 150\nsources:\n  - id: main\n    file: /etc/nginx/nginx.conf\n    format: text\n")
	snap = scan()
	owner := ""
	for _, f := range snap.Files {
		if f.Path == "/etc/nginx/nginx.conf" {
			owner = f.Service
		}
	}
	if owner != "mynginx" {
		t.Errorf("nginx.conf owner: %q", owner)
	}
	if count(snap, "nginx") == 0 {
		t.Error("nginx parser still runs and gives endpoints")
	}
}
