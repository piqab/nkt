package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Версия 5: сайты (хост и конвейер — по именам, localhost — как есть),
// выбор проверок сухого прогона и история .env конвейера (перешифрована
// ключом хаба-получателя), адрес сайта справки.
func TestExportImportV5(t *testing.T) {
	m1, db1 := newTestManager(t)
	m2, db2 := newTestManager(t)
	m1.cfg.DataDir, m2.cfg.DataDir = t.TempDir(), t.TempDir()
	ctx := context.Background()

	web1, _ := m1.AddHost(ctx, "web1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	gone, _ := m1.AddHost(ctx, "gone", "10.0.0.2", 22, "root", store.HostAuthPassword, "pw", false)
	web1New, _ := m2.AddHost(ctx, "web1", "10.0.1.2", 22, "root", store.HostAuthPassword, "pw", false)

	enc := func(s string) []byte { b, _ := secretbox.Encrypt(m1.key, []byte(s)); return b }
	pid, err := db1.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: "kind: compose\n", HookID: "hook-shop-123456", HookSecret: enc("hs"), Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	_ = db1.SetPipelineDrySkip(ctx, pid, `["dns","outside"]`)
	_ = db1.SetPipelineEnv(ctx, pid, enc("A=2"))
	_, _ = db1.AddEnvVersion(ctx, store.EnvVersion{PipelineID: pid, Author: "admin", Note: "first", EnvEnc: enc("A=1")})
	_, _ = db1.AddEnvVersion(ctx, store.EnvVersion{PipelineID: pid, Author: "admin", Note: "second", EnvEnc: enc("A=2")})

	mustSite := func(s store.Site) {
		if _, err := db1.SaveSite(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	mustSite(store.Site{Domains: []string{"shop.example.com", "www.shop.example.com"}, HostID: web1, Proxy: "nginx", Stack: "shop", Service: "web", ContainerPort: 8080, OpenFirewall: true, PipelineID: pid})
	mustSite(store.Site{Domains: []string{"hub.example.com"}, HostID: localHostID, Proxy: "caddy", Upstream: "127.0.0.1:8443"})
	mustSite(store.Site{Domains: []string{"old.example.com"}, HostID: gone, Proxy: "haproxy", Upstream: "127.0.0.1:81"})
	_ = db1.KVSet(ctx, "ui.docs_url", "https://docs.example.com/")

	export, err := m1.ExportHub(ctx, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if export.Version != store.ExportFormatVersion || store.ExportFormatVersion != 5 {
		t.Fatalf("version %d", export.Version)
	}
	if len(export.Sites) != 3 || len(export.Pipelines) != 1 || len(export.Pipelines[0].EnvVersions) != 2 || export.Pipelines[0].DrySkip == "" {
		t.Fatalf("export: sites %d pipelines %+v", len(export.Sites), export.Pipelines)
	}
	raw, _ := json.Marshal(export)
	decoded, err := store.DecodeHubExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Хоста gone в хабе-получателе не будет.
	var keep []store.HostExport
	for _, h := range decoded.Hosts {
		if h.Name != "gone" {
			keep = append(keep, h)
		}
	}
	decoded.Hosts = keep

	plan, _ := m2.ImportPlan(ctx, decoded)
	found := false
	for _, ps := range plan {
		if ps.Section == store.SectionSites {
			found = len(ps.Items) == 3
		}
	}
	if !found {
		t.Fatalf("plan has no sites section: %+v", plan)
	}

	rep := m2.ImportHosts(ctx, decoded, nil)
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "old.example.com") {
		t.Fatalf("errors: %v", rep.Errors)
	}

	pipes, _ := db2.ListPipelines(ctx)
	if len(pipes) != 1 || pipes[0].DrySkip != `["dns","outside"]` {
		t.Fatalf("pipelines: %+v", pipes)
	}
	envs, _ := db2.EnvVersions(ctx, pipes[0].ID, 10)
	if len(envs) != 2 || envs[0].Note != "second" {
		t.Fatalf("env versions: %+v", envs)
	}
	if plain, err := secretbox.Decrypt(m2.key, envs[1].EnvEnc); err != nil || string(plain) != "A=1" {
		t.Fatalf("env version not re-encrypted: %q %v", plain, err)
	}

	sites, _ := db2.ListSites(ctx)
	if len(sites) != 2 {
		t.Fatalf("sites: %+v", sites)
	}
	for _, s := range sites {
		switch s.Domains[0] {
		case "shop.example.com":
			if s.HostID != web1New || s.PipelineID != pipes[0].ID || s.ContainerPort != 8080 || len(s.Domains) != 2 {
				t.Fatalf("shop site: %+v", s)
			}
		case "hub.example.com":
			if s.HostID != localHostID || s.Proxy != "caddy" {
				t.Fatalf("hub site: %+v", s)
			}
		default:
			t.Fatalf("unexpected site %+v", s)
		}
	}
	if v, ok, _ := db2.KVGet(ctx, "ui.docs_url"); !ok || v != "https://docs.example.com/" {
		t.Fatalf("docs url: %q", v)
	}

	// Повторный импорт: сайты уже есть — пропуск, с «заменить» — замена.
	rep = m2.ImportHosts(ctx, decoded, nil)
	if c := rep.Count(store.SectionSites); c.Skipped != 2 || c.Added != 0 {
		t.Fatalf("second import: %+v", c)
	}
}
