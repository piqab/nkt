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

// Изоляция машин токенов (1.17.2): создание машины — только на хостах
// области токена; токен «по правам» видит из машин только свои, токен с
// пределом по хостам — и свои машины вне списка; сценарий от токена
// проверяется теми же правами, что и API; удалённый токен останавливает
// своё задание.
func TestTokenMachineIsolation(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	kvm1, _ := db.CreateHost(ctx, "kvm1", "10.0.0.1", 22, "root", store.HostAuthPassword, []byte("x"))
	kvm2, _ := db.CreateHost(ctx, "kvm2", "10.0.0.2", 22, "root", store.HostAuthPassword, []byte("x"))
	mine, _ := db.CreateHost(ctx, "app1", "10.0.1.1", 22, "root", store.HostAuthPassword, []byte("x"))
	foreign, _ := db.CreateHost(ctx, "app2", "10.0.1.2", 22, "root", store.HostAuthPassword, []byte("x"))
	_ = db.SetHostParent(ctx, mine, kvm2)
	_ = db.SetHostParent(ctx, foreign, kvm2)

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
	create := func(body string) (int64, func(*http.Request)) {
		t.Helper()
		rec := do("POST", "/api/hub/tokens", body, session)
		var c struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != 200 {
			t.Fatalf("token: %d %s", rec.Code, rec.Body)
		}
		return c.ID, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	}

	// Токен с пределом по хостам: создать машину на чужом хосте — 403.
	scopedID, scoped := create(`{"name":"scoped","role":"custom","hosts":[` + strconv.FormatInt(kvm1, 10) + `],
		"perms":{"actions":["vm.manage"],"limits":{"vms":2,"vm_cpu":2,"vm_mem_mb":2048,"vm_disk_gb":20}}}`)
	spec := `{"name":"x1","image_id":"ubuntu-24.04","disk_gb":5,"memory_mb":512,"vcpus":1,"user":"app"}`
	rec = do("POST", "/api/hub/vm/provision", `{"host_id":`+strconv.FormatInt(kvm2, 10)+`,"spec":`+spec+`}`, scoped)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("provision outside scope: %d %s", rec.Code, rec.Body)
	}
	if u, _ := db.ListTokenUsage(ctx, scopedID); len(u) != 0 {
		t.Fatalf("usage after denied provision: %+v", u)
	}

	// Своя машина на kvm2 доступна токену с пределом по хостам; чужая — нет.
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: scopedID, Kind: store.UsageVM, HostID: kvm2, Ref: "app1"})
	tok, _ := db.APITokenByID(ctx, scopedID)
	allow := srv.scopeFilter(withToken(ctx, tok))
	switch {
	case !allow(kvm1):
		t.Error("scoped: own host hidden")
	case allow(kvm2):
		t.Error("scoped: host outside the list shown")
	case !allow(mine):
		t.Error("scoped: own machine hidden")
	case allow(foreign):
		t.Error("scoped: foreign machine shown")
	}
	if !srv.tokenHostAllowed(ctx, tok, strconv.FormatInt(mine, 10)) {
		t.Error("scoped: own machine not allowed")
	}

	// Токен «по правам» без списка хостов: обычные хосты — да, машины —
	// только свои.
	freeID, _ := create(`{"name":"free","role":"custom","perms":{"actions":["vm.manage"],"limits":{"vms":1,"vm_cpu":2,"vm_mem_mb":2048,"vm_disk_gb":20}}}`)
	free, _ := db.APITokenByID(ctx, freeID)
	allow = srv.scopeFilter(withToken(ctx, free))
	switch {
	case !allow(kvm2):
		t.Error("limited: plain host hidden")
	case allow(mine), allow(foreign):
		t.Error("limited: another token's machine shown")
	}
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: freeID, Kind: store.UsageVM, HostID: kvm2, Ref: "app2"})
	if allow = srv.scopeFilter(withToken(ctx, free)); !allow(foreign) || allow(mine) {
		t.Error("limited: ownership by usage")
	}

	// Сценарий от токена: шагам нужны те же действия, что и API.
	tctx, err := srv.tokenScriptContext(ctx, "token:free")
	if err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]string{
		"on kvm2 vm create x2 image ubuntu cpu 1 mem 512 disk 5": "",
		"on kvm2 vm start app2":                                  "",
		"on kvm2 k8s create lab flavor k3s nodes single":         store.TokenActK8sCluster,
		"host web9 192.0.2.10 user root key hub":                 "web9",
	} {
		sc, issues := script.Parse(text, nil)
		if len(issues) > 0 {
			t.Fatalf("%s: %v", text, issues)
		}
		err := srv.scriptScopeError(tctx, sc)
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: %v", text, err)
		case want != "" && err == nil:
			t.Errorf("%s: accepted", text)
		}
	}
	// Задание токена, которого больше нет, — отказ; автор-человек — как было.
	if _, err := srv.tokenScriptContext(ctx, "token:gone"); err == nil {
		t.Error("gone token accepted")
	}
	if c, err := srv.tokenScriptContext(ctx, "admin"); err != nil {
		t.Error(err)
	} else if _, ok := tokenFromContext(c); ok {
		t.Error("user job got a token")
	}
}
