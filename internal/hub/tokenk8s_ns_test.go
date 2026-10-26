package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// Свой namespace (1.17.8): манифест — PSA restricted, политика «только
// свой namespace», учётная запись с ролью edit в нём и её секрет, квота;
// рендер helm — namespace подставлен, кластерные виды не проходят;
// свои namespace открыты и манифестам токена.
func TestOwnNamespaceManifest(t *testing.T) {
	m := ownNSManifest("shop", "client", store.TokenLimits{K8sCPU: 2, K8sMemMB: 2048, K8sPods: 10, ServiceCPU: 0.5, ServiceMemMB: 256})
	for _, want := range []string{
		"pod-security.kubernetes.io/enforce: restricted", "kind: NetworkPolicy", "policyTypes: [Ingress]",
		"kind: ServiceAccount", "name: nkt-client", "kind: ClusterRole\n  name: edit", "type: kubernetes.io/service-account-token",
		"kind: ResourceQuota", "pods: \"10\"", "kind: LimitRange",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("no %q in:\n%s", want, m)
		}
	}
	if strings.Count(m, "kind: Namespace") != 1 {
		t.Errorf("namespace twice:\n%s", m)
	}

	rendered := "---\n# Source: x\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n---\n---\napiVersion: v1\nkind: Service\nmetadata:\n  name: web\n  namespace: shop\n"
	out, err := withNamespace(rendered, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if ns, err := tokenK8sNamespaces(out, []string{"shop"}); err != nil || len(ns) != 1 {
		t.Fatalf("%v %v\n%s", ns, err, out)
	}
	bad, _ := withNamespace("apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: x\n", "shop")
	if _, err := tokenK8sNamespaces(bad, []string{"shop"}); err == nil {
		t.Fatal("cluster-wide kind from a chart accepted")
	}
	other, _ := withNamespace("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n  namespace: kube-system\n", "shop")
	if _, err := tokenK8sNamespaces(other, []string{"shop"}); err == nil {
		t.Fatal("foreign namespace from a chart accepted")
	}
}

func TestOwnNamespaceRequests(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	clID, err := db.CreateCluster(ctx, store.Cluster{Name: "shared", Flavor: "k3s", Topology: "single", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		set(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("POST", "/api/auth/login", `{"username":"admin","password":"admin-password-1234"}`, func(*http.Request) {})
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	session := func(r *http.Request) { r.AddCookie(cookie) }
	token := func(body string) func(*http.Request) {
		rec := do("POST", "/api/hub/tokens", body, session)
		var c struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != 200 {
			t.Fatalf("token: %d %s", rec.Code, rec.Body)
		}
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	}
	// k8s.apply без заданных namespace — можно, если есть свои namespace.
	none := token(`{"name":"none","role":"custom","perms":{"actions":["k8s.namespace","k8s.apply"]}}`)
	if rec := do("POST", "/api/hub/k8s/own", `{"cluster":"shared","namespace":"shop"}`, none); rec.Code != 403 || !strings.Contains(rec.Body.String(), "0") {
		t.Fatalf("limit 0: %d %s", rec.Code, rec.Body)
	}
	b := token(`{"name":"client","role":"custom","perms":{"actions":["k8s.namespace"],"clusters":["shared"],"limits":{"k8s_namespaces":1}}}`)
	for _, ns := range []string{"kube-x", "default", "nkt-sys", "Bad_Name"} {
		if rec := do("POST", "/api/hub/k8s/own", `{"cluster":"shared","namespace":"`+ns+`"}`, b); rec.Code != 400 {
			t.Errorf("%s: %d %s", ns, rec.Code, rec.Body)
		}
	}
	if rec := do("POST", "/api/hub/k8s/own", `{"cluster":"other","namespace":"shop"}`, b); rec.Code != 403 {
		t.Fatalf("unknown cluster: %d %s", rec.Code, rec.Body)
	}
	// Чужой namespace — ни логов, ни удаления, ни helm.
	if rec := do("DELETE", "/api/hub/k8s/own/shared/kube-system", "", b); rec.Code != 403 {
		t.Fatalf("delete foreign: %d %s", rec.Code, rec.Body)
	}
	if rec := do("GET", "/api/hub/k8s/own/shared/monitoring/logs?pod=x", "", b); rec.Code != 403 {
		t.Fatalf("logs foreign: %d %s", rec.Code, rec.Body)
	}
	// Свой (по записи) — открыт манифестам токена.
	var tok store.APIToken
	list, _ := db.ListAPITokens(ctx)
	for _, x := range list {
		if x.Name == "client" {
			tok = x
		}
	}
	_ = srv.saveOwnNamespaces(ctx, []OwnNamespace{{ClusterID: clID, Cluster: "shared", Namespace: "shop", TokenID: tok.ID, Token: "client"}})
	if got := srv.tokenAllowedNamespaces(ctx, tok); len(got) != 1 || got[0] != "shop" {
		t.Fatalf("allowed: %v", got)
	}
}
