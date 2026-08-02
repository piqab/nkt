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
	c := collect.NewFixtures(root)
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
