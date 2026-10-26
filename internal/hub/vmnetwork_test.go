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

// Сеть машин (1.17.5): пул портов хоста — только администратору, с
// проверкой диапазона; порт токену — только из пула, в его потолке и на
// свою машину, неудачный проброс не оставляет учёта; свои маршруты Fly —
// имя в домене токена, своя машина, потолок, чужой маршрут не удалить.
func TestVMPortsAndOwnFly(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	srv.jobs.Register(KindFlyApply, noopRunner{})
	h := srv.Handler()
	ctx := context.Background()
	kvm, _ := db.CreateHost(ctx, "kvm1", "203.0.113.20", 22, "root", store.HostAuthKey, []byte("x"))
	app1, _ := db.CreateHost(ctx, "app1", "10.0.1.1", 22, "deploy", store.HostAuthKey, []byte("x"))
	app9, _ := db.CreateHost(ctx, "app9", "10.0.1.9", 22, "deploy", store.HostAuthKey, []byte("x"))
	_ = db.SetHostParent(ctx, app1, kvm)
	_ = db.SetHostParent(ctx, app9, kvm)
	gw, err := srv.putEdge(ctx, EdgeSettings{Enabled: true, Address: "203.0.113.10:8444", Roles: []string{EdgeRoleFly}})
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
	expect := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: want %d, got %d %s", what, code, rec.Code, rec.Body)
		}
	}
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":["fly.own"]}}`, session), 400, "fly.own without a domain")
	rec = do("POST", "/api/hub/tokens", `{"name":"client","role":"custom","perms":{"actions":["vm.manage","vm.ports","fly.own"],"fly_domain":"C.Example.com.",
		"limits":{"ports":1,"fly_routes":1}}}`, session)
	var c struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != 200 {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}
	if tok, _ := db.APITokenByID(ctx, c.ID); tok.Perms.FlyDomain != "c.example.com" {
		t.Fatalf("domain not normalized: %q", tok.Perms.FlyDomain)
	}
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageVM, HostID: kvm, Ref: "app1", CPU: 1, MemMB: 512, DiskGB: 5})
	vm1 := "/api/hub/hosts/" + strconv.FormatInt(app1, 10) + "/vm"
	vm9 := "/api/hub/hosts/" + strconv.FormatInt(app9, 10) + "/vm"
	pool := "/api/hub/hosts/" + strconv.FormatInt(kvm, 10) + "/port-pool"

	// Пул: токену нельзя; диапазон проверяется.
	expect(do("PUT", pool, `{"from":20000,"to":20009}`, bearer), 403, "token sets the pool")
	expect(do("PUT", pool, `{"from":40500,"to":40600}`, session), 400, "pool over Fly ports")
	expect(do("PUT", pool, `{"from":80,"to":90}`, session), 400, "privileged ports")
	expect(do("PUT", pool, `{"from":20000,"to":40000}`, session), 400, "pool too large")

	// Порт: без пула — 409; чужая машина — 403.
	expect(do("POST", vm1+"/ports", `{"vm_port":22}`, bearer), http.StatusConflict, "no pool")
	expect(do("PUT", pool, `{"from":20000,"to":20009}`, session), 200, "pool")
	expect(do("POST", vm9+"/ports", `{"vm_port":22}`, bearer), 403, "foreign machine")
	expect(do("POST", vm1+"/ports", `{"vm_port":22,"proto":"sctp"}`, bearer), 400, "bad proto")
	// Хост не отвечает (адреса нет) — порт не числится выданным.
	if rec := do("POST", vm1+"/ports", `{"vm_port":22}`, bearer); rec.Code == 200 {
		t.Fatalf("open without a host: %s", rec.Body)
	}
	if got := srv.portAllocs(ctx); len(got) != 0 {
		t.Fatalf("alloc kept after a failed apply: %+v", got)
	}
	// Потолок портов: один уже занят.
	_ = srv.savePortAllocs(ctx, []PortAlloc{{Host: "kvm1", Port: 20000, Proto: "tcp", VM: "app1", VMPort: 22, Token: "client"}})
	expect(do("POST", vm1+"/ports", `{"vm_port":80}`, bearer), 403, "ports limit")
	rec = do("GET", vm1+"/ports", "", bearer)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"port":20000`) || !strings.Contains(rec.Body.String(), "203.0.113.20") {
		t.Fatalf("ports: %d %s", rec.Code, rec.Body)
	}

	// Fly: имя вне домена, чужая машина — отказ; своё — маршрут HTTPS с TLS.
	expect(do("POST", "/api/hub/own/fly", `{"name":"app.other.com","host_id":`+strconv.FormatInt(app1, 10)+`,"port":8080}`, bearer), 403, "name outside the domain")
	expect(do("POST", "/api/hub/own/fly", `{"name":"evilc.example.com","host_id":`+strconv.FormatInt(app1, 10)+`,"port":8080}`, bearer), 403, "suffix without a dot")
	expect(do("POST", "/api/hub/own/fly", `{"name":"app.c.example.com","host_id":`+strconv.FormatInt(app9, 10)+`,"port":8080}`, bearer), 403, "foreign machine route")
	rec = do("POST", "/api/hub/own/fly", `{"name":"app.c.example.com","host_id":`+strconv.FormatInt(app1, 10)+`,"port":8080}`, bearer)
	expect(rec, 200, "own route")
	d := srv.flyDoc(ctx)
	if len(d.Routes) != 1 || d.Routes[0].ID != "t"+strconv.FormatInt(c.ID, 10)+"-1" || !d.Routes[0].TLS || d.Routes[0].Gateway != gw.ID ||
		d.Routes[0].Targets[0].Kind != "vm" || d.Routes[0].Targets[0].Ref != "app1" || d.Routes[0].Targets[0].HostID != kvm {
		t.Fatalf("route: %+v", d.Routes)
	}
	expect(do("POST", "/api/hub/own/fly", `{"name":"b.c.example.com","host_id":`+strconv.FormatInt(app1, 10)+`,"port":8081}`, bearer), 403, "fly routes limit")
	// Маршрут администратора токену не удалить.
	admin := FlyRoute{ID: "site", Gateway: gw.ID, Kind: "https", Names: []string{"site.example.com"}, Port: 443, Algorithm: "roundrobin",
		Check: d.Routes[0].Check, Targets: []FlyTarget{{ID: "a", HostID: kvm, Kind: "host", Port: 80}}}
	if _, err := srv.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: append(d.Routes, admin)}, "admin", ""); err != nil {
		t.Fatal(err)
	}
	expect(do("DELETE", "/api/hub/own/fly/site", "", bearer), 403, "admin route")
	expect(do("DELETE", "/api/hub/own/fly/"+d.Routes[0].ID, "", bearer), 200, "own route delete")
	if d := srv.flyDoc(ctx); len(d.Routes) != 1 || d.Routes[0].ID != "site" {
		t.Fatalf("after delete: %+v", d.Routes)
	}
}
