package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/api"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Версия 4 целиком: конвейер с историей и секретами, nkt-edge, учётные
// записи (по выбору), шаблон fail2ban с историей, инструкция ИИ ip — с
// одного хаба на другой с другим мастер-ключом; секреты читаются новым
// ключом, адрес вебхука сохранён.
func TestExportImportV4(t *testing.T) {
	m1, db1 := newTestManager(t)
	m2, db2 := newTestManager(t)
	m1.cfg.DataDir, m2.cfg.DataDir = t.TempDir(), t.TempDir()
	ctx := context.Background()

	hid, err := m1.AddHost(ctx, "edge-host", "10.0.0.9", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := secretbox.Encrypt(m1.key, []byte("hook-secret"))
	pid, err := db1.CreatePipeline(ctx, store.Pipeline{Name: "app", Content: "v1", HookID: "abcdef0123456789", HookSecret: secret, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db1.UpdatePipelineContent(ctx, pid, "v2", "admin", "правка"); err != nil {
		t.Fatal(err)
	}
	git, _ := secretbox.Encrypt(m1.key, []byte("git-token"))
	if err := db1.SetPipelineSecrets(ctx, pid, nil, git, nil); err != nil {
		t.Fatal(err)
	}
	token, _ := secretbox.Encrypt(m1.key, []byte("edge-token"))
	edge, _ := json.Marshal(EdgeSettings{Enabled: true, Address: "vps:8444", Domain: "hooks.example.com", TokenEnc: token, HostID: hid})
	_ = db1.KVSet(ctx, edgeSettingsKey, string(edge))
	if _, err := db1.CreateUser(ctx, "ops", "$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$aGFzaA", store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	_ = db1.KVSet(ctx, "ai.prompt.ip/ru", "Своя инструкция проверки адреса.")
	tpl := fail2ban.Template{Name: "myapp", Description: "d", Jail: "[myapp]\nenabled = true\n"}
	list, _ := json.Marshal([]fail2ban.Template{tpl})
	_ = db1.KVSet(ctx, api.F2BTemplatesKey, string(list))
	for _, doc := range []string{"## description: old\n## jail\n[myapp]\n## filter\n", api.F2BTemplateDoc(tpl)} {
		blob, sum, err := control.HistoryWrite(m1.cfg.HistoryDir(), api.F2BTemplatePrefix+"myapp", []byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db1.AddVersion(ctx, store.ConfigVersion{Path: api.F2BTemplatePrefix + "myapp", Service: "fail2ban", Action: store.ActionEdit, SHA256: sum, BlobName: blob, Size: int64(len(doc))}); err != nil {
			t.Fatal(err)
		}
	}

	export, err := m1.ExportHub(ctx, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if export.Version != store.ExportFormatVersion || len(export.Pipelines) != 1 || len(export.Pipelines[0].Versions) != 2 ||
		export.Edge == nil || export.Edge.Host != "edge-host" || len(export.Users) != 2 && len(export.Users) != 1 ||
		len(export.F2BTemplates) != 1 || len(export.F2BTemplates[0].Versions) != 2 {
		t.Fatalf("export: %+v", export)
	}
	if _, err := m1.ExportHub(ctx, false, false); err != nil {
		t.Fatal(err)
	}
	if noUsers, _ := m1.ExportHub(ctx, false, false); len(noUsers.Users) != 0 {
		t.Fatal("users exported without the choice")
	}

	// Через JSON — как в файле.
	raw, _ := json.Marshal(export)
	decoded, err := store.DecodeHubExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	rep := m2.ImportHosts(ctx, decoded, nil)
	if len(rep.Errors) != 0 {
		t.Fatalf("import errors: %v", rep.Errors)
	}
	p, err := db2.PipelineByHook(ctx, "abcdef0123456789")
	if err != nil || p.Content != "v2" {
		t.Fatalf("pipeline: %+v %v", p, err)
	}
	if got, err := secretbox.Decrypt(m2.key, p.HookSecret); err != nil || string(got) != "hook-secret" {
		t.Fatalf("hook secret: %q %v", got, err)
	}
	if got, err := secretbox.Decrypt(m2.key, p.GitCred); err != nil || string(got) != "git-token" {
		t.Fatalf("git cred: %q %v", got, err)
	}
	if vs, _ := db2.PipelineVersions(ctx, p.ID); len(vs) != 2 {
		t.Fatalf("pipeline history: %+v", vs)
	}
	// Прежний одиночный edge (ключ hub.edge) переехал в список и прошёл
	// через файл с ролью «вебхуки».
	edges := loadEdges(ctx, db2)
	if len(edges) != 1 || !edges[0].Has(EdgeRoleHooks) || edges[0].Has(EdgeRoleAPI) {
		t.Fatalf("edges: %+v", edges)
	}
	st := edges[0]
	hosts, _ := db2.ListHosts(ctx)
	if st.Address != "vps:8444" || len(hosts) != 1 || st.HostID != hosts[0].ID {
		t.Fatalf("edge: %+v hosts %+v", st, hosts)
	}
	if got, err := secretbox.Decrypt(m2.key, st.TokenEnc); err != nil || string(got) != "edge-token" {
		t.Fatalf("edge token: %q %v", got, err)
	}
	if u, err := db2.UserByName(ctx, "ops"); err != nil || u.Role != store.RoleViewer {
		t.Fatalf("user: %+v %v", u, err)
	}
	if v, _, _ := db2.KVGet(ctx, "ai.prompt.ip/ru"); v != "Своя инструкция проверки адреса." {
		t.Fatalf("ai prompt: %q", v)
	}
	if got := m2.f2bTemplates(ctx); len(got) != 1 || got[0].Name != "myapp" {
		t.Fatalf("f2b templates: %+v", got)
	}
	vs, _ := db2.ListVersions(ctx, api.F2BTemplatePrefix+"myapp", 10)
	if len(vs) != 2 {
		t.Fatalf("f2b history: %+v", vs)
	}
	if content, err := control.HistoryRead(m2.cfg.HistoryDir(), vs[1].BlobName); err != nil || !strings.Contains(string(content), "old") {
		t.Fatalf("f2b oldest version: %q %v", content, err)
	}

	// План повторного импорта: всё — совпадения.
	plan, err := m2.ImportPlan(ctx, decoded)
	if err != nil {
		t.Fatal(err)
	}
	conflicts := map[string]int{}
	for _, sec := range plan {
		for _, it := range sec.Items {
			if it.Conflict {
				conflicts[sec.Section]++
			}
		}
	}
	for _, sec := range []string{store.SectionHosts, store.SectionPipelines, store.SectionUsers, store.SectionF2BTemplates, store.SectionEdge} {
		if conflicts[sec] == 0 {
			t.Errorf("plan: no conflict in %s: %+v", sec, plan)
		}
	}

	// Повтор с заменой конвейера и шаблона, остальное — пропуск.
	decoded.Pipelines[0].Content = "v3"
	decoded.F2BTemplates[0].Description = "новое"
	again := m2.ImportHosts(ctx, decoded, store.ImportResolutions{
		store.SectionPipelines:    {"app": "replace"},
		store.SectionF2BTemplates: {"myapp": "replace"},
	})
	if again.Count(store.SectionPipelines).Replaced != 1 || again.Count(store.SectionHosts).Skipped != 1 || again.Count(store.SectionUsers).Skipped == 0 {
		t.Fatalf("again: %+v %v", again.Sections, again.Errors)
	}
	if p, _ := db2.PipelineByHook(ctx, "abcdef0123456789"); p.Content != "v3" {
		t.Fatalf("pipeline not replaced: %q", p.Content)
	}
	if vs, _ := db2.PipelineVersions(ctx, p.ID); len(vs) != 3 {
		t.Fatalf("pipeline history after replace: %d", len(vs))
	}
	if got := m2.f2bTemplates(ctx); got[0].Description != "новое" {
		t.Fatalf("template not replaced: %+v", got)
	}
	if hosts2, _ := db2.ListHosts(ctx); len(hosts2) != 1 {
		t.Fatalf("hosts duplicated: %d", len(hosts2))
	}
}
