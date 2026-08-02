package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/parse"
	"github.com/althq/netknownsthat/internal/store"
)

// configsSetup wires a ConfigManager against an isolated copy of the repo's
// fixtures tree (copyFixturesRoot, defined in certrenew_test.go) — block
// writes touch real files, so the tests must not run against the tracked
// fixtures/host directory itself.
func configsSetup(t *testing.T) *ConfigManager {
	t.Helper()
	root := copyFixturesRoot(t)
	return configsSetupWithCollector(t, root, collect.NewFixtures(root))
}

// configsSetupWithCollector is configsSetup with an injectable collector, so
// a test can wrap it (e.g. to force the validator to fail) while everything
// else about the setup stays identical.
func configsSetupWithCollector(t *testing.T, root string, c collect.Collector) *ConfigManager {
	t.Helper()
	cfg := &config.Config{
		Mode:            config.ModeFixtures,
		FixturesRoot:    root,
		DataDir:         t.TempDir(), // history blobs must not land next to the source
		NginxRoot:       "/etc/nginx",
		NginxMainConfig: "/etc/nginx/nginx.conf",
		HAProxyRoot:     "/etc/haproxy",
		HAProxyMainConf: "/etc/haproxy/haproxy.cfg",
		ComposeFiles:    []string{"/srv/docker/docker-compose.yml"},
		CommandTimeout:  5 * time.Second,
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	return NewConfigManager(cfg, c, db, scanner, NewServiceManager(cfg, c, db))
}

const nginxSiteFile = "/etc/nginx/sites-enabled/app.example.com.conf"

func TestListBlocksNginx(t *testing.T) {
	m := configsSetup(t)
	blocks, err := m.ListBlocks(nginxSiteFile)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	if len(blocks) != 2 || blocks[0].Kind != parse.BlockServer {
		t.Fatalf("blocks = %+v, ожидалось 2 server{}", blocks)
	}
}

func TestListBlocksRejectsPathOutsideAllowlist(t *testing.T) {
	m := configsSetup(t)
	if _, err := m.ListBlocks("/etc/passwd"); err == nil {
		t.Error("ожидалась ошибка для пути вне разрешённых каталогов")
	}
}

func TestWriteBlockUpdateAppliesAndValidates(t *testing.T) {
	m := configsSetup(t)
	blocks, err := m.ListBlocks(nginxSiteFile)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	loc := blocks[1].Children[1] // location /static/ { ... } in the https server
	if !strings.Contains(loc.Raw, "/static/") {
		t.Fatalf("ожидался location /static/, получено %q", loc.Raw)
	}

	res, err := m.WriteBlock(context.Background(), "test", nginxSiteFile, BlockWriteRequest{
		Op:      "update",
		Kind:    parse.BlockLocation,
		Start:   loc.StartLine,
		End:     loc.EndLine,
		Content: "    location /static/ {\n        proxy_pass http://static_backend;\n        expires 30d;\n    }",
	})
	if err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	if !res.Validated || res.RolledBack {
		t.Fatalf("WriteResult = %+v, ожидалась успешная валидация без отката", res)
	}

	updated, err := m.Read(nginxSiteFile)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(updated.Content, "expires 30d;") {
		t.Error("файл на диске не содержит новую директиву")
	}
}

func TestWriteBlockRejectsStaleExpectedSHA256(t *testing.T) {
	m := configsSetup(t)
	blocks, err := m.ListBlocks(nginxSiteFile)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	loc := blocks[0].Children[0]

	_, err = m.WriteBlock(context.Background(), "test", nginxSiteFile, BlockWriteRequest{
		Op:       "update",
		Kind:     parse.BlockLocation,
		Start:    loc.StartLine,
		End:      loc.EndLine,
		Content:  "    location /.well-known/acme-challenge/ {\n        root /var/www/certbot;\n        # touched\n    }",
		Expected: "not-the-real-sha256",
	})
	if err == nil {
		t.Error("ожидалась ошибка из-за несовпадения expected_sha256")
	}
}

func TestWriteBlockRejectsCreateDeleteOnSingletonHAProxySections(t *testing.T) {
	m := configsSetup(t)
	for _, op := range []string{"create", "delete"} {
		_, err := m.WriteBlock(context.Background(), "test", "/etc/haproxy/haproxy.cfg", BlockWriteRequest{
			Op: op, Kind: parse.BlockGlobal, Start: 1, End: 10, Content: "global\n    daemon",
		})
		if err == nil {
			t.Errorf("%s global: ожидалась ошибка (singleton-раздел)", op)
		}
	}
}

func TestWriteBlockCreateAppendsHAProxyBackend(t *testing.T) {
	m := configsSetup(t)
	res, err := m.WriteBlock(context.Background(), "test", "/etc/haproxy/haproxy.cfg", BlockWriteRequest{
		Op:      "create",
		Kind:    parse.BlockBackend,
		Content: "backend be_new\n    balance roundrobin\n    server n1 10.0.0.9:9090 check",
	})
	if err != nil {
		t.Fatalf("WriteBlock(create): %v", err)
	}
	if !res.Validated || res.RolledBack {
		t.Fatalf("WriteResult = %+v, ожидалась успешная валидация без отката", res)
	}

	blocks, err := m.ListBlocks("/etc/haproxy/haproxy.cfg")
	if err != nil {
		t.Fatalf("ListBlocks после создания: %v", err)
	}
	found := false
	for _, b := range blocks {
		if b.Kind == parse.BlockBackend && b.Name == "be_new" {
			found = true
		}
	}
	if !found {
		t.Error("новый backend be_new не найден после создания")
	}
}

func TestWriteBlockDeleteRemovesLocation(t *testing.T) {
	m := configsSetup(t)
	blocks, err := m.ListBlocks(nginxSiteFile)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	https := blocks[1]
	healthz := https.Children[2] // location = /healthz

	if _, err := m.WriteBlock(context.Background(), "test", nginxSiteFile, BlockWriteRequest{
		Op: "delete", Kind: parse.BlockLocation, Start: healthz.StartLine, End: healthz.EndLine,
	}); err != nil {
		t.Fatalf("WriteBlock(delete): %v", err)
	}

	after, err := m.ListBlocks(nginxSiteFile)
	if err != nil {
		t.Fatalf("ListBlocks после удаления: %v", err)
	}
	if len(after[1].Children) != 2 {
		t.Fatalf("после удаления ожидалось 2 location, получено %d", len(after[1].Children))
	}
	for _, l := range after[1].Children {
		if l.Name == "= /healthz" {
			t.Error("удалённый location всё ещё присутствует")
		}
	}
}

const composeFile = "/srv/docker/docker-compose.yml"

func TestListBlocksDocker(t *testing.T) {
	m := configsSetup(t)
	blocks, err := m.ListBlocks(composeFile)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	want := []string{"app", "api", "redis", "postgres", "grafana", "prometheus", "minio"}
	if len(blocks) != len(want) {
		t.Fatalf("сервисов = %d, ожидалось %d", len(blocks), len(want))
	}
	for i, b := range blocks {
		if b.Kind != parse.BlockService || b.Name != want[i] {
			t.Errorf("blocks[%d] = %s %q, ожидалось service %q", i, b.Kind, b.Name, want[i])
		}
	}
}

func TestWriteBlockUpdateDockerService(t *testing.T) {
	m := configsSetup(t)
	blocks, err := m.ListBlocks(composeFile)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	var redis parse.Block
	for _, b := range blocks {
		if b.Name == "redis" {
			redis = b
		}
	}
	if redis.Name == "" {
		t.Fatal("сервис redis не найден")
	}

	// docker-compose.yml has no canned validator response in the fixtures
	// (only nginx -t / haproxy -c do) — the write still must succeed and land
	// on disk, just unvalidated, exactly like the plain text editor already
	// behaves for a service without a fixture rule.
	res, err := m.WriteBlock(context.Background(), "test", composeFile, BlockWriteRequest{
		Op: "update", Kind: parse.BlockService, Start: redis.StartLine, End: redis.EndLine,
		Content: "  redis:\n    image: redis:7.2-alpine\n    restart: unless-stopped",
	})
	if err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	if res.RolledBack {
		t.Fatalf("WriteResult = %+v, откат не ожидался", res)
	}

	updated, err := m.Read(composeFile)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(updated.Content, "redis:7.2-alpine") {
		t.Error("файл на диске не содержит новый тег образа")
	}
}

func TestWriteBlockCreateDockerServiceLandsBeforeVolumes(t *testing.T) {
	m := configsSetup(t)
	res, err := m.WriteBlock(context.Background(), "test", composeFile, BlockWriteRequest{
		Op: "create", Kind: parse.BlockService,
		Content: "  worker:\n    image: ghcr.io/acme/worker:1.0.0\n    restart: unless-stopped",
	})
	if err != nil {
		t.Fatalf("WriteBlock(create): %v", err)
	}
	if res.RolledBack {
		t.Fatalf("WriteResult = %+v, откат не ожидался", res)
	}

	updated, err := m.Read(composeFile)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	workerIdx := strings.Index(updated.Content, "  worker:")
	volumesIdx := strings.Index(updated.Content, "\nvolumes:")
	if workerIdx < 0 || volumesIdx < 0 || workerIdx > volumesIdx {
		t.Fatalf("новый сервис должен быть перед volumes:, получено:\n%s", updated.Content)
	}

	blocks, err := m.ListBlocks(composeFile)
	if err != nil {
		t.Fatalf("ListBlocks после создания: %v", err)
	}
	if blocks[len(blocks)-1].Name != "worker" {
		t.Errorf("последний сервис = %q, ожидался worker", blocks[len(blocks)-1].Name)
	}
}

func TestWriteBlockDeleteDockerService(t *testing.T) {
	m := configsSetup(t)
	blocks, err := m.ListBlocks(composeFile)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	var minio parse.Block
	for _, b := range blocks {
		if b.Name == "minio" {
			minio = b
		}
	}
	if minio.Name == "" {
		t.Fatal("сервис minio не найден")
	}

	if _, err := m.WriteBlock(context.Background(), "test", composeFile, BlockWriteRequest{
		Op: "delete", Kind: parse.BlockService, Start: minio.StartLine, End: minio.EndLine,
	}); err != nil {
		t.Fatalf("WriteBlock(delete): %v", err)
	}

	after, err := m.ListBlocks(composeFile)
	if err != nil {
		t.Fatalf("ListBlocks после удаления: %v", err)
	}
	for _, b := range after {
		if b.Name == "minio" {
			t.Error("удалённый сервис minio всё ещё присутствует")
		}
	}
	if len(after) != len(blocks)-1 {
		t.Errorf("сервисов после удаления = %d, ожидалось %d", len(after), len(blocks)-1)
	}
}
