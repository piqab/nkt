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
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// Fly для namespace клиента (1.17.13): без ingress у кластера — отказ;
// ingress задаёт только администратор и только на узел кластера; маршрут
// на свой namespace ведёт на ingress (узел-машина — цель vm на её хосте);
// чужой namespace — отказ.
func TestOwnFlyNamespace(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	srv.jobs.Register(KindFlyApply, noopRunner{})
	h := srv.Handler()
	ctx := context.Background()
	if _, err := srv.putEdge(ctx, EdgeSettings{Enabled: true, Address: "203.0.113.10:8444", Roles: []string{EdgeRoleFly}}); err != nil {
		t.Fatal(err)
	}
	kvm, _ := db.CreateHost(ctx, "kvm1", "203.0.113.20", 22, "root", store.HostAuthKey, []byte("x"))
	node, _ := db.CreateHost(ctx, "shared-w-1", "192.168.130.11", 22, "deploy", store.HostAuthKey, []byte("x"))
	_ = db.SetHostParent(ctx, node, kvm)
	clID, _ := db.CreateCluster(ctx, store.Cluster{Name: "shared", HostID: kvm, Flavor: "k3s", Topology: "cp1", Status: "ready", SpecJSON: `{"cni":"cilium"}`})
	_ = db.SetHostCluster(ctx, node, clID, RoleWorker)

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
	rec = do("POST", "/api/hub/tokens", `{"name":"client","role":"custom","perms":{"actions":["fly.own","k8s.namespace"],"fly_domain":"c.example.com","limits":{"fly_routes":2,"k8s_namespaces":1}}}`, session)
	var c struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	_ = srv.saveOwnNamespaces(ctx, []OwnNamespace{{ClusterID: clID, Cluster: "shared", Namespace: "shop", TokenID: c.ID, Token: "client"}})
	ing := "/api/hub/clusters/" + strconv.FormatInt(clID, 10) + "/client-ingress"
	body := `{"name":"shop.c.example.com","cluster":"shared","namespace":"shop"}`

	if rec := do("POST", "/api/hub/own/fly", body, bearer); rec.Code != 403 || !strings.Contains(rec.Body.String(), "shared") {
		t.Fatalf("no ingress: %d %s", rec.Code, rec.Body)
	}
	if rec := do("PUT", ing, `{"node":"shared-w-1","port":80}`, bearer); rec.Code != 403 {
		t.Fatalf("token sets ingress: %d", rec.Code)
	}
	if rec := do("PUT", ing, `{"node":"kvm1","port":80}`, session); rec.Code != 400 {
		t.Fatalf("ingress on a non-node: %d %s", rec.Code, rec.Body)
	}
	if rec := do("PUT", ing, `{"node":"shared-w-1","port":80}`, session); rec.Code != 200 {
		t.Fatalf("ingress: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/hub/own/fly", `{"name":"x.c.example.com","cluster":"shared","namespace":"kube-system"}`, bearer); rec.Code != 403 {
		t.Fatalf("foreign namespace: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/hub/own/fly", body, bearer); rec.Code != 200 {
		t.Fatalf("route: %d %s", rec.Code, rec.Body)
	}
	d := srv.flyDoc(ctx)
	if len(d.Routes) != 1 || d.Routes[0].Targets[0].Kind != "vm" || d.Routes[0].Targets[0].Ref != "shared-w-1" || d.Routes[0].Targets[0].HostID != kvm || d.Routes[0].Targets[0].Port != 80 {
		t.Fatalf("route: %+v", d.Routes)
	}

	sc, issues := script.Parse("fly own shop.c.example.com cluster shared namespace shop\nfly own app.c.example.com machine app1 port 8080\n", nil)
	if len(issues) > 0 || len(sc.Steps) != 2 || sc.Steps[1].Args["port"] != "8080" {
		t.Fatalf("parse: %v %+v", issues, sc.Steps)
	}
	if _, issues := script.Parse("fly own app.c.example.com machine app1 port 0\n", nil); len(issues) == 0 {
		t.Fatal("bad port accepted")
	}
}
