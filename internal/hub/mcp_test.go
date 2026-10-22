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

// MCP: только по токену; инструменты — по роли и пределу токена; вызов
// инструмента проходит те же проверки, что прямой вызов API.
func TestMCP(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	webID, _ := db.CreateHost(ctx, "web1", "203.0.113.10", 22, "root", store.HostAuthPassword, []byte("x"))
	_ = db.SetHostGroup(ctx, webID, "prod")
	otherID, _ := db.CreateHost(ctx, "web2", "203.0.113.11", 22, "root", store.HostAuthPassword, []byte("x"))

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
	rpc := func(set func(*http.Request), method string, params any) mcpResponse {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		rec := do("POST", "/api/mcp", string(raw), set)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", method, rec.Code, rec.Body)
		}
		var resp mcpResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	tools := func(set func(*http.Request)) map[string]bool {
		t.Helper()
		resp := rpc(set, "tools/list", nil)
		var res struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		out := map[string]bool{}
		for _, tl := range res.Tools {
			out[tl.Name] = true
		}
		return out
	}
	call := func(set func(*http.Request), name string, args map[string]any) (string, bool) {
		t.Helper()
		resp := rpc(set, "tools/call", map[string]any{"name": name, "arguments": args})
		var res struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		raw, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(raw, &res)
		if len(res.Content) == 0 {
			t.Fatalf("%s: no content: %s", name, raw)
		}
		return res.Content[0].Text, res.IsError
	}

	// Сессия браузера — нет: только токен.
	if rec := do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, session); rec.Code != http.StatusForbidden {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	read := token(`{"name":"reader","role":"read"}`)
	admin := token(`{"name":"agent","role":"admin"}`)
	scoped := token(`{"name":"prod-agent","role":"admin","groups":["prod"]}`)

	init := rpc(read, "initialize", map[string]any{"protocolVersion": "2025-03-26"})
	if b, _ := json.Marshal(init.Result); !strings.Contains(string(b), `"protocolVersion":"2025-03-26"`) || !strings.Contains(string(b), `"tools"`) {
		t.Fatalf("initialize: %s", b)
	}
	if rec := do("POST", "/api/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, read); rec.Code != http.StatusAccepted {
		t.Fatalf("notification: %d", rec.Code)
	}
	if resp := rpc(read, "nope", nil); resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("unknown method: %+v", resp)
	}

	// Чтение: только инструменты чтения.
	rt := tools(read)
	if !rt["list_hosts"] || !rt["host_findings"] || rt["run_script"] || rt["migrate_stack"] || rt["service_action"] {
		t.Fatalf("read tools: %v", rt)
	}
	// Предел по хостам: без общей карты Traffic Fly.
	st := tools(scoped)
	if !st["run_script"] || st["fly_apply"] || st["fly_map"] {
		t.Fatalf("scoped tools: %v", st)
	}
	if at := tools(admin); !at["fly_apply"] || !at["migrate_stack"] {
		t.Fatalf("admin tools: %v", at)
	}

	// Список хостов — с пределом токена.
	text, isErr := call(scoped, "list_hosts", nil)
	if isErr || !strings.Contains(text, "web1") || strings.Contains(text, "web2") {
		t.Fatalf("scoped list_hosts: %v %s", isErr, text)
	}
	text, _ = call(admin, "list_hosts", nil)
	if !strings.Contains(text, "web2") {
		t.Fatalf("admin list_hosts: %s", text)
	}
	// Чужой хост — отказ токена, как у прямого вызова.
	if text, isErr := call(scoped, "host_findings", map[string]any{"host_id": otherID}); !isErr || !strings.Contains(text, "HTTP 403") {
		t.Fatalf("scoped other host: %v %s", isErr, text)
	}
	// Инструмент не по роли — отказ без вызова.
	if text, isErr := call(read, "run_script", map[string]any{"script_id": 1}); !isErr || !strings.Contains(text, "not allowed") {
		t.Fatalf("read run_script: %v %s", isErr, text)
	}
	// Аргумент пути — только простое значение.
	if text, isErr := call(admin, "host_findings", map[string]any{"host_id": "1/../../hub/tokens"}); !isErr || !strings.Contains(text, "plain value") {
		t.Fatalf("path arg: %v %s", isErr, text)
	}
	// Администратор: проверка сценария — сразу, без подтверждения.
	text, isErr = call(admin, "check_script", map[string]any{"content": "on web1 package install htop\n"})
	if isErr || !strings.Contains(text, "steps") {
		t.Fatalf("check_script: %v %s", isErr, text)
	}

	// Пометка внутреннего вызова без секрета — отказ.
	if rec := do("GET", "/api/hub/hosts", "", func(r *http.Request) { r.Header.Set(mcpInternalHeader, "wrong:1") }); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged internal: %d", rec.Code)
	}
}
