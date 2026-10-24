package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Токен «по правам»: проверка прав при создании; разрешённое действие
// проходит (в том числе за RequireAdmin), неразрешённое — 403 с
// названием действия; список конвейеров; MCP показывает только
// разрешённые инструменты; учёт и «освободить».
func TestCustomTokenPermissions(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	content := "repo: https://codeberg.org/me/shop.git\\nref: main\\naction: compose\\ncompose:\\n  file: compose.yaml\\n  project: shop\\n  hosts: [localhost]\\n"
	shopID, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: strings.ReplaceAll(content, "\\n", "\n"), HookID: "hook-perm-0001", HookSecret: secret, Author: "admin"})
	blogID, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "blog", Content: strings.ReplaceAll(strings.ReplaceAll(content, "shop", "blog"), "\\n", "\n"), HookID: "hook-perm-0002", HookSecret: secret, Author: "admin"})

	do := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if set != nil {
			set(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("POST", "/api/auth/login", `{"username":"admin","password":"admin-password-1234"}`, nil)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	session := func(r *http.Request) { r.AddCookie(cookie) }
	expect := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: want %d, got %d %s", what, code, rec.Code, rec.Body)
		}
	}

	// Проверка прав при создании.
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":["rm.rf"]}}`, session), 400, "unknown action")
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":[]}}`, session), 400, "no actions")
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":["pipeline.deploy"],"pipelines":["nope"]}}`, session), 400, "unknown pipeline")
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":["k8s.apply"]}}`, session), 400, "k8s without namespace")
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":["k8s.apply"],"namespaces":["kube-system"]}}`, session), 400, "system namespace")
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":["pipeline.deploy"],"limits":{"cpu":4}}}`, session), 400, "cpu total without service cap")
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":["vm.manage"],"limits":{"vms":-1}}}`, session), 400, "negative limit")

	rec = do("POST", "/api/hub/tokens", `{"name":"ci-shop","role":"custom","perms":{"actions":["pipeline.power","vm.manage"],"pipelines":["shop"],
		"limits":{"cpu":4,"mem_mb":4096,"vms":1,"vm_cpu":2,"vm_mem_mb":2048,"vm_disk_gb":20,"deploys_per_hour":3}}}`, session)
	expect(rec, 200, "create custom")
	var c struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }

	// Чтение — как у токена чтения; действие вне прав — 403 с названием.
	expect(do("GET", "/api/hub/pipelines", "", bearer), 200, "read pipelines")
	rec = do("POST", "/api/hub/pipelines/"+strconv.FormatInt(shopID, 10)+"/deploy", `{}`, bearer)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "pipeline.deploy") {
		t.Fatalf("deploy not allowed: %d %s", rec.Code, rec.Body)
	}
	// Разрешённое действие проходит RequireAdmin; чужой конвейер — нет.
	rec = do("POST", "/api/hub/pipelines/"+strconv.FormatInt(blogID, 10)+"/stop", `{}`, bearer)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "blog") {
		t.Fatalf("other pipeline: %d %s", rec.Code, rec.Body)
	}
	rec = do("POST", "/api/hub/pipelines/"+strconv.FormatInt(shopID, 10)+"/stop", `{}`, bearer)
	if rec.Code == 403 || rec.Code == 401 {
		t.Fatalf("allowed action denied: %d %s", rec.Code, rec.Body)
	}
	// Управлять хабом — нет.
	expect(do("GET", "/api/hub/tokens", "", bearer), 403, "tokens")
	expect(do("POST", "/api/hosts/local/services/ssh/restart", "", bearer), 403, "service not allowed")

	// MCP: инструменты — по правам.
	if rec := do("PUT", "/api/hub/experimental", `{"map":false,"clusters":false,"assistant":false,"mcp":true}`, session); rec.Code != 200 {
		t.Fatalf("mcp on: %d", rec.Code)
	}
	rec = do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, bearer)
	list := rec.Body.String()
	for _, want := range []string{"pipeline_power", "create_vm", "vm_power", "list_hosts"} {
		if !strings.Contains(list, `"`+want+`"`) {
			t.Errorf("tool %s missing: %.300s", want, list)
		}
	}
	for _, not := range []string{"deploy_pipeline", "run_script", "fly_apply", "container_action"} {
		if strings.Contains(list, `"`+not+`"`) {
			t.Errorf("tool %s shown", not)
		}
	}

	// Потолки машины: одна машина вне потолка — отказ; своя — учёт.
	tok, _ := db.APITokenByID(ctx, c.ID)
	host := store.Host{ID: 77, Name: "kvm1"}
	if err := srv.tokenVMCheck(ctx, tok, host, vmcreate.Spec{Name: "big", VCPUs: 4, MemoryMB: 1024, DiskGB: 10}); err == nil {
		t.Fatal("vm over vcpu limit accepted")
	}
	if err := srv.tokenVMCheck(ctx, tok, host, vmcreate.Spec{Name: "app1", VCPUs: 2, MemoryMB: 2048, DiskGB: 20}); err != nil {
		t.Fatal(err)
	}
	if err := srv.tokenVMCheck(ctx, tok, host, vmcreate.Spec{Name: "app2", VCPUs: 1, MemoryMB: 512, DiskGB: 5}); err == nil || !strings.Contains(err.Error(), "1") {
		t.Fatalf("second vm over count: %v", err)
	}
	if err := srv.tokenOwnsVM(ctx, tok, "77", "app1"); err != nil {
		t.Fatalf("own vm: %v", err)
	}
	if err := srv.tokenOwnsVM(ctx, tok, "77", "other"); err == nil {
		t.Fatal("foreign vm allowed")
	}
	// Суммой: стек на 3 CPU при занятых 2 — больше 4.
	err := srv.tokenFits(ctx, tok, store.TokenUsage{TokenID: tok.ID, Kind: store.UsageCompose, HostID: 1, Ref: "shop", Label: "shop@web1", CPU: 3, MemMB: 256})
	if err == nil || !strings.Contains(err.Error(), "shop@web1") {
		t.Fatalf("sum over cpu: %v", err)
	}
	// Окно токена: занято и «освободить».
	rec = do("GET", "/api/hub/tokens/"+strconv.FormatInt(c.ID, 10)+"/usage", "", session)
	var view tokenUsageView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if rec.Code != 200 || view.Used.VMs != 1 || view.Used.CPU != 2 {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}
	expect(do("POST", "/api/hub/tokens/"+strconv.FormatInt(c.ID, 10)+"/usage/release", `{"kind":"vm","host_id":77,"ref":"app1"}`, session), 200, "release")
	if u, _ := db.ListTokenUsage(ctx, c.ID); len(u) != 0 {
		t.Fatalf("after release: %+v", u)
	}
	// Действий в час — не больше трёх (одно уже было — stop).
	for i := 0; i < 2; i++ {
		_ = do("POST", "/api/hub/pipelines/"+strconv.FormatInt(shopID, 10)+"/start", `{}`, bearer)
	}
	if rec := do("POST", "/api/hub/pipelines/"+strconv.FormatInt(shopID, 10)+"/start", `{}`, bearer); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate: %d %s", rec.Code, rec.Body)
	}
	// Удалён токен — учёт уходит с ним.
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageVM, HostID: 77, Ref: "x"})
	expect(do("DELETE", "/api/hub/tokens/"+strconv.FormatInt(c.ID, 10), "", session), 200, "delete token")
	if u, _ := db.ListTokenUsage(ctx, c.ID); len(u) != 0 {
		t.Fatalf("usage after delete: %+v", u)
	}
}

// k8s для токена: свои namespace, без кластерных видов и доступа к узлу;
// квота namespace — ResourceQuota и LimitRange по потолкам.
func TestTokenK8sRules(t *testing.T) {
	ok := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: team-a\nspec:\n  template:\n    spec:\n      containers:\n        - name: web\n          image: nginx\n"
	if nss, err := tokenK8sNamespaces(ok, []string{"team-a"}); err != nil || len(nss) != 1 || nss[0] != "team-a" {
		t.Fatalf("ok: %v %v", nss, err)
	}
	for what, bad := range map[string]string{
		"other ns":      strings.Replace(ok, "team-a", "team-b", 1),
		"no ns":         strings.Replace(ok, "  namespace: team-a\n", "", 1),
		"cluster role":  "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: x\n",
		"quota":         "apiVersion: v1\nkind: ResourceQuota\nmetadata:\n  name: x\n  namespace: team-a\n",
		"host network":  strings.Replace(ok, "      containers:", "      hostNetwork: true\n      containers:", 1),
		"host path":     strings.Replace(ok, "      containers:", "      volumes:\n        - name: h\n          hostPath:\n            path: /\n      containers:", 1),
		"privileged":    strings.Replace(ok, "          image: nginx\n", "          image: nginx\n          securityContext:\n            privileged: true\n", 1),
		"foreign ns obj": "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: other\n",
	} {
		if _, err := tokenK8sNamespaces(bad, []string{"team-a"}); err == nil {
			t.Errorf("%s accepted", what)
		}
	}
	q := tokenK8sQuotaManifest("team-a", "ci", store.TokenLimits{K8sCPU: 2, K8sMemMB: 4096, K8sStorageGB: 20, K8sPods: 10, ServiceCPU: 0.5, ServiceMemMB: 512})
	for _, want := range []string{"kind: Namespace", "kind: ResourceQuota", `limits.cpu: "2"`, `limits.memory: "4096Mi"`, `requests.storage: "20Gi"`, `pods: "10"`, "kind: LimitRange", `memory: "512Mi"`} {
		if !strings.Contains(q, want) {
			t.Errorf("quota manifest lacks %s:\n%s", want, q)
		}
	}
	if _, err := tokenK8sNamespaces(q, []string{"team-a"}); err == nil {
		t.Error("token could apply its own quota")
	}
}
