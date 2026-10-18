package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// callJSON — обработчик хаба напрямую (id — параметр {id} маршрута).
func callJSON(t *testing.T, h http.HandlerFunc, method, id string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if id != "" {
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

const envBlockSpec = `repo: https://github.com/org/app.git
ref: main
action: compose
compose:
  file: deploy/docker-compose.yml
  project: app
  hosts: [localhost]
  env:
    AUTH_DOMAIN: auth.example.com   # имя
    DB_PASS: plain-secret-value-1
    ADMIN_SECRET: generate:hex32
`

// Значения блока env уходят в .env конвейера; в описании, его версиях и
// журнале действий их нет.
func TestPipelineEnvBlockSave(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	code, out := callJSON(t, srv.handlePipelineCreate, "POST", "", map[string]any{"name": "app", "content": envBlockSpec})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	id := int64(out["id"].(float64))
	pl, _ := db.PipelineByID(ctx, id)
	if strings.Contains(pl.Content, "plain-secret-value-1") || strings.Contains(pl.Content, "auth.example.com") || strings.Contains(pl.Content, "generate:") {
		t.Fatalf("value in content:\n%s", pl.Content)
	}
	if !strings.Contains(pl.Content, `AUTH_DOMAIN: "@secret"   # имя`) || out["content"] != pl.Content {
		t.Fatalf("stripped content:\n%s\n%v", pl.Content, out["content"])
	}
	env, _ := srv.pipelineEnv(pl)
	vals := envValues(env)
	if vals["AUTH_DOMAIN"] != "auth.example.com" || vals["DB_PASS"] != "plain-secret-value-1" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(vals["ADMIN_SECRET"]) {
		t.Fatalf("env: %q", *env)
	}
	gen := vals["ADMIN_SECRET"]

	// Та же правка ещё раз — ничего не меняет; generate: у заданного —
	// пропуск; другое значение — 409, с replace_env — замена.
	sid := strconv.FormatInt(id, 10)
	code, out = callJSON(t, srv.handlePipelineUpdate, "PUT", sid, map[string]any{"content": envBlockSpec})
	if code != 200 {
		t.Fatalf("same update: %d %v", code, out)
	}
	pl, _ = db.PipelineByID(ctx, id)
	env, _ = srv.pipelineEnv(pl)
	if envValues(env)["ADMIN_SECRET"] != gen {
		t.Fatal("generated value changed on resave")
	}
	changed := strings.Replace(envBlockSpec, "plain-secret-value-1", "other-secret-value-2", 1)
	code, out = callJSON(t, srv.handlePipelineUpdate, "PUT", sid, map[string]any{"content": changed})
	if code != http.StatusConflict || !strings.Contains(string(mustJSON(out["env_conflict"])), "DB_PASS") {
		t.Fatalf("conflict: %d %v", code, out)
	}
	code, out = callJSON(t, srv.handlePipelineUpdate, "PUT", sid, map[string]any{"content": changed, "replace_env": true})
	if code != 200 {
		t.Fatalf("replace: %d %v", code, out)
	}
	pl, _ = db.PipelineByID(ctx, id)
	env, _ = srv.pipelineEnv(pl)
	if envValues(env)["DB_PASS"] != "other-secret-value-2" {
		t.Fatalf("not replaced: %q", *env)
	}
	vs, _ := db.PipelineVersions(ctx, id)
	for _, v := range vs {
		if strings.Contains(v.Content, "secret-value") {
			t.Fatalf("value in version %d", v.ID)
		}
	}
	audit, _ := db.ListAudit(ctx, store.AuditFilter{Limit: 100})
	for _, a := range audit {
		if strings.Contains(string(mustJSON(a)), "secret-value") || strings.Contains(string(mustJSON(a)), gen) {
			t.Fatalf("value in audit: %+v", a)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// Конфиг из описания: на хосте — с подставленным секретом и правами,
// в истории конфигов хоста — нет; в compose — том и метка.
func TestDeployComposeConfigs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.MkdirAll(filepath.Join(repo, "deploy"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "deploy", "docker-compose.yml"), []byte("services:\n  web:\n    image: nginx\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "deploy", "extra.ini"), []byte("tag={{nkt.tag}}\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	content := "repo: " + repo + "\nref: main\naction: compose\ncompose:\n  file: deploy/docker-compose.yml\n  project: cfg\n  hosts: [localhost]\n  wait_timeout: 1m\n" +
		"  configs:\n    web:\n      - target: /etc/nginx/conf.d/app.conf\n        mode: \"0440\"\n        content: |\n          # token ${TOKEN}; host $host; $${KEEP}\n" +
		"      - target: /etc/app/extra.ini\n        file: deploy/extra.ini\n"
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	pid, err := db.CreatePipeline(ctx, store.Pipeline{Name: "cfg", Content: content, HookID: "hook-cfg-1", HookSecret: secret, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := db.PipelineByID(ctx, pid)
	// Без TOKEN в .env — отказ до хостов.
	d, err := srv.startDeployment(ctx, pl, store.Deployment{Trigger: "manual", Author: "admin"}, false)
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if got := waitDeployment(t, wctx, db, d.ID); got.Status == store.DeploySucceeded {
		t.Fatal("deployed without TOKEN")
	}
	env := "TOKEN=tok-secret-123\n"
	if err := srv.setPipelineEnv(ctx, pl, "admin", "t", &env); err != nil {
		t.Fatal(err)
	}
	pl, _ = db.PipelineByID(ctx, pid)
	d, err = srv.startDeployment(ctx, pl, store.Deployment{Trigger: "manual", Tag: "v9", Author: "admin"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitDeployment(t, wctx, db, d.ID); got.Status != store.DeploySucceeded {
		t.Fatalf("deployment: %+v\n%s", got, jobLogText(t, ctx, db, d.JobID))
	}
	stack := filepath.Join(root, "srv", "compose", "cfg")
	conf, err := os.ReadFile(filepath.Join(stack, ".nkt-configs", "web-1-app.conf"))
	if err != nil || string(conf) != "# token tok-secret-123; host $host; ${KEEP}\n" {
		t.Fatalf("config: %q %v", conf, err)
	}
	if st, _ := os.Stat(filepath.Join(stack, ".nkt-configs", "web-1-app.conf")); st.Mode().Perm() != 0o440 {
		t.Fatalf("mode %v", st.Mode())
	}
	extra, _ := os.ReadFile(filepath.Join(stack, ".nkt-configs", "web-2-extra.ini"))
	compose, _ := os.ReadFile(filepath.Join(stack, "docker-compose.yml"))
	if string(extra) != "tag=v9\n" || !strings.Contains(string(compose), "./.nkt-configs/web-1-app.conf:/etc/nginx/conf.d/app.conf:ro") || !strings.Contains(string(compose), "nkt.config.web-1-app.conf") {
		t.Fatalf("extra %q compose:\n%s", extra, compose)
	}
	if log := jobLogText(t, ctx, db, d.JobID); strings.Contains(log, "tok-secret-123") {
		t.Fatalf("secret in log:\n%s", log)
	}
	// История конфигов хоста — без готового файла с секретом.
	vs, _ := db.ListVersions(ctx, "/srv/compose/cfg/.nkt-configs/web-1-app.conf", 10)
	if len(vs) != 0 {
		t.Fatalf("rendered config in history: %d", len(vs))
	}
}

// Сухой прогон черновика с блоком env: значения — не в параметрах
// задания, а прогон их видит.
func TestDryRunEnvBlockNotInParams(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не установлен")
	}
	deploy.AllowLocalReposForTest(true)
	defer deploy.AllowLocalReposForTest(false)
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	repo := t.TempDir()
	testGit(t, repo, "init", "-q", "-b", "main")
	_ = os.MkdirAll(filepath.Join(repo, "deploy"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "deploy", "docker-compose.yml"), []byte("services:\n  web:\n    image: nginx\n    environment:\n      A: ${DRY_SECRET}\n"), 0o644)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-q", "-m", "one")
	content := "repo: " + repo + "\nref: main\naction: compose\ncompose:\n  file: deploy/docker-compose.yml\n  project: dryenv\n  hosts: [localhost]\n  env:\n    DRY_SECRET: dry-secret-value-77\n"
	code, out := callJSON(t, srv.handlePipelineDryRun, "POST", "", map[string]any{"content": content})
	if code != 200 {
		t.Fatalf("dry run: %d %v", code, out)
	}
	jid := int64(out["job_id"].(float64))
	j := waitJobDone(t, db, jid)
	log := jobLogText(t, ctx, db, jid)
	if strings.Contains(j.Params, "dry-secret-value-77") || strings.Contains(log, "dry-secret-value-77") {
		t.Fatalf("value in params/log: %s\n%s", j.Params, log)
	}
	if !strings.Contains(log, "несохранёнными значениями блока env") || strings.Contains(log, "нет DRY_SECRET") {
		t.Fatalf("log:\n%s", log)
	}
}
