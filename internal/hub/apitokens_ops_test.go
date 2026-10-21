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
	"github.com/piqab/nkt/internal/store"
)

// Токену — сценарии, переносы, Traffic Fly и кластеры: роли, пределы по
// хостам, закрытое (создание кластеров, kubeconfig, правка сценариев).
func TestAPITokenOps(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	webID, err := db.CreateHost(ctx, "web1", "203.0.113.10", 22, "root", store.HostAuthPassword, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.SetHostGroup(ctx, webID, "prod")
	otherID, _ := db.CreateHost(ctx, "web2", "203.0.113.11", 22, "root", store.HostAuthPassword, []byte("x"))

	do := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
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
	token := func(body string) func(*http.Request) {
		t.Helper()
		rec := do("POST", "/api/hub/tokens", body, session)
		if rec.Code != 200 {
			t.Fatalf("token %s: %d %s", body, rec.Code, rec.Body)
		}
		var c struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	}
	expect := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: want %d, got %d %s", what, code, rec.Code, rec.Body)
		}
	}
	// Пропущен токеном: в тестовом хабе исполнители заданий не все, поэтому
	// «разрешено» — не 401 и не 403.
	passes := func(rec *httptest.ResponseRecorder, what string) {
		t.Helper()
		if rec.Code == 401 || rec.Code == 403 {
			t.Fatalf("%s: denied %d %s", what, rec.Code, rec.Body)
		}
	}
	script := func(name, content string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"name": name, "content": content})
		rec := do("POST", "/api/hub/scripts", string(body), session)
		expect(rec, 200, "script "+name)
		var c struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return strconv.FormatInt(c.ID, 10)
	}
	onWeb := script("on-web", "on web1 packages install htop")
	onOther := script("on-other", "on web2 packages install htop")
	global := script("global", "group lab")
	fly := script("fly", "fly apply")

	// Кластеры: lab — на web1, other — на web2.
	lab, _ := db.CreateCluster(ctx, store.Cluster{Name: "lab", HostID: webID, Flavor: "k3s", Topology: "single", SpecJSON: "{}"})
	_ = db.SetHostCluster(ctx, webID, lab, "control-plane")
	other, _ := db.CreateCluster(ctx, store.Cluster{Name: "other", HostID: otherID, Flavor: "k3s", Topology: "single", SpecJSON: "{}"})
	_ = db.SetHostCluster(ctx, otherID, other, "control-plane")

	read := token(`{"name":"reader","role":"read"}`)
	expect(do("GET", "/api/hub/scripts", "", read), 200, "read: scripts")
	expect(do("GET", "/api/hub/scripts/"+onWeb, "", read), 200, "read: script")
	expect(do("POST", "/api/hub/scripts/"+onWeb+"/run", `{"dry_run":true}`, read), 403, "read: run")
	expect(do("GET", "/api/hub/migrations", "", read), 200, "read: migrations")
	expect(do("GET", "/api/hub/migrate/live", "", read), 200, "read: live")
	expect(do("POST", "/api/hub/migrate", `{"from":1,"to":2,"container":"x"}`, read), 403, "read: migrate")
	expect(do("GET", "/api/hub/fly", "", read), 200, "read: fly")
	expect(do("POST", "/api/hub/fly/apply", `{"dry_run":true}`, read), 403, "read: fly apply")
	expect(do("GET", "/api/hub/clusters", "", read), 200, "read: clusters")
	expect(do("POST", "/api/hub/k8s/manifests/diff", `{"name":"ns","content":"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: x\n","clusters":[1]}`, read), 403, "read: manifest diff")

	admin := token(`{"name":"automation","role":"admin"}`)
	expect(do("POST", "/api/hub/scripts/check", `{"content":"on web1 packages install htop"}`, admin), 200, "admin: check")
	passes(do("POST", "/api/hub/scripts/"+fly+"/run", `{"dry_run":true}`, admin), "admin: run fly script")
	expect(do("GET", "/api/hub/fly/status", "", admin), 200, "admin: fly status")
	passes(do("POST", "/api/hub/fly/apply", `{"dry_run":true}`, admin), "admin: fly apply")
	// По-прежнему закрыто.
	expect(do("POST", "/api/hub/scripts", `{"name":"x","content":"group y"}`, admin), 403, "admin: create script")
	expect(do("PUT", "/api/hub/scripts/"+onWeb, `{"name":"on-web","content":"group y"}`, admin), 403, "admin: edit script")
	expect(do("POST", "/api/hub/clusters", `{}`, admin), 403, "admin: create cluster")
	expect(do("GET", "/api/hub/clusters/"+strconv.FormatInt(lab, 10)+"/kubeconfig", "", admin), 403, "admin: kubeconfig")
	expect(do("DELETE", "/api/hub/clusters/"+strconv.FormatInt(lab, 10), "", admin), 403, "admin: delete cluster")
	expect(do("POST", "/api/hub/fly/versions/1/restore", "", admin), 403, "admin: fly restore")

	// Предел: группа prod (только web1).
	scoped := token(`{"name":"prod-only","role":"admin","groups":["prod"]}`)
	passes(do("POST", "/api/hub/scripts/"+onWeb+"/run", `{"dry_run":true}`, scoped), "scoped: run on web1")
	expect(do("POST", "/api/hub/scripts/"+onOther+"/run", `{"dry_run":true}`, scoped), 403, "scoped: run on web2")
	expect(do("POST", "/api/hub/scripts/"+global+"/run", `{"dry_run":true}`, scoped), 403, "scoped: group")
	expect(do("POST", "/api/hub/scripts/"+fly+"/run", `{"dry_run":true}`, scoped), 403, "scoped: fly script")
	expect(do("GET", "/api/hub/fly", "", scoped), 403, "scoped: fly")
	expect(do("POST", "/api/hub/migrate/preview", `{"from":`+strconv.FormatInt(webID, 10)+`,"to":`+strconv.FormatInt(otherID, 10)+`,"container":"x"}`, scoped), 403, "scoped: preview to web2")
	rec = do("GET", "/api/hub/clusters", "", scoped)
	expect(rec, 200, "scoped: clusters")
	var cls []struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cls)
	if len(cls) != 1 || cls[0].Name != "lab" {
		t.Fatalf("scoped clusters: %s", rec.Body)
	}
	expect(do("GET", "/api/hub/clusters/"+strconv.FormatInt(other, 10)+"/nodes", "", scoped), 403, "scoped: other nodes")
	expect(do("POST", "/api/hub/k8s/manifests/diff", `{"name":"ns","content":"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: x\n","clusters":[`+strconv.FormatInt(other, 10)+`]}`, scoped), 403, "scoped: other diff")
}
