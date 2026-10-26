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

// Свои лимиты (1.17.10): токен видит «занято из» по всем счётчикам —
// суммы, машины, порты, маршруты Fly, namespace, кластеры; MCP my_usage
// виден и токену чтения; без токена — отказ.
func TestTokenSelfUsage(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
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
	rec = do("POST", "/api/hub/tokens", `{"name":"client","role":"custom","perms":{"actions":["vm.manage","vm.ports"],"limits":{"vms":3,"ports":2,"cpu":8}}}`, session)
	var c struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageVM, HostID: 5, Ref: "app1", CPU: 2, MemMB: 2048, DiskGB: 20})
	_ = srv.savePortAllocs(ctx, []PortAlloc{{Host: "kvm1", Port: 20000, Proto: "tcp", VM: "app1", VMPort: 22, Token: "client"}, {Host: "kvm1", Port: 20001, Proto: "tcp", VM: "x", VMPort: 22, Token: "other"}})

	rec = do("GET", "/api/hub/tokens/self/usage", "", bearer)
	var v tokenUsageView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 {
		t.Fatalf("self usage: %d %s", rec.Code, rec.Body)
	}
	if v.Used.VMs != 1 || v.Used.CPU != 2 || v.Used.Ports != 1 || v.Limits.Ports != 2 || v.Limits.CPU != 8 || len(v.Actions) != 2 {
		t.Fatalf("view: %+v", v)
	}
	if rec := do("GET", "/api/hub/tokens/self/usage", "", session); rec.Code != 400 {
		t.Fatalf("session: %d", rec.Code)
	}
	// MCP: инструмент есть и у токена по правам, и у токена чтения.
	if rec := do("PUT", "/api/hub/experimental", `{"map":false,"clusters":false,"assistant":false,"mcp":true}`, session); rec.Code != 200 {
		t.Fatalf("mcp on: %d", rec.Code)
	}
	rec = do("POST", "/api/hub/tokens", `{"name":"reader","role":"read"}`, session)
	var rd struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &rd)
	for _, tok := range []string{c.Token, rd.Token} {
		rec := do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) })
		if !strings.Contains(rec.Body.String(), `"my_usage"`) {
			t.Errorf("my_usage missing: %.200s", rec.Body)
		}
	}
	rec = do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"my_usage","arguments":{}}}`, bearer)
	if !strings.Contains(rec.Body.String(), `ports`) || strings.Contains(rec.Body.String(), `"isError":true`) {
		t.Fatalf("my_usage call: %s", rec.Body)
	}
}
