package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/plugin"
)

// fakeDNSPlugin — плагин по протоколу: зоны, правка зоны текстом,
// команда сценария «dns».
func fakeDNSPlugin(t *testing.T, secret string) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	zone := "app A 203.0.113.10\n"
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !plugin.Verify(secret, r, body, time.Now()) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == plugin.PathManifest {
			_ = json.NewEncoder(w).Encode(plugin.Manifest{Protocol: 1, Name: "dns", Title: "DNS (" + r.Header.Get(plugin.HeaderLang) + ")", Version: "0.1",
				Ops: []plugin.Op{
					{Name: "zones", ReadOnly: true, MCP: true, Description: "List zones"},
					{Name: "zone_get", ReadOnly: true},
					{Name: "zone_put"},
					{Name: "zone_add", MCP: true, Input: map[string]any{"type": "object", "properties": map[string]any{"zone": map[string]any{"type": "string"}}}},
				},
				Pages:    []plugin.Page{{ID: "zones", Title: "Zones", Op: "zones"}},
				Commands: []plugin.Command{{Word: "dns", Usage: "dns set NAME TYPE VALUE"}},
				Provides: []string{"dns"},
			})
			return
		}
		var req plugin.Request
		_ = json.Unmarshal(body, &req)
		calls = append(calls, req.Op+" "+req.User)
		resp := plugin.Response{OK: true}
		switch req.Op {
		case "zones":
			resp.View = &plugin.View{Type: "table", Columns: []plugin.Column{{Key: "zone", Title: "Zone"}}, Rows: []plugin.Row{{Cells: map[string]any{"zone": "example.com"}}}}
		case "zone_get":
			resp.Result, _ = json.Marshal(map[string]string{"text": zone})
		case "zone_put":
			zone = req.Args["text"].(string)
			resp.Text = "saved"
		case "zone_add":
			resp.Text = "zone " + req.Args["zone"].(string) + " added"
		case plugin.OpCheck:
			argv, _ := req.Args["argv"].([]any)
			if len(argv) < 5 {
				resp = plugin.Response{Error: "dns set NAME TYPE VALUE"}
			}
		case plugin.OpConnect:
		default:
			resp = plugin.Response{Error: "no op " + req.Op}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestPluginLifecycle(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	const secret = "plugin-secret-0123456789"
	fake, calls := fakeDNSPlugin(t, secret)
	u, _ := url.Parse(fake.URL)
	port, _ := strconv.Atoi(u.Port())

	do := func(method, path string, body any, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if s, ok := body.(string); ok {
			raw = []byte(s)
		} else if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
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

	// Чужой секрет — отказ плагина; верный — манифест и подключение.
	rec = do("POST", "/api/hub/plugins/probe", map[string]any{"host": "localhost", "port": port, "secret": "wrong-secret-0123456789"}, session)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "signature") {
		t.Fatalf("wrong secret: %d %s", rec.Code, rec.Body)
	}
	expect(do("POST", "/api/hub/plugins/probe", map[string]any{"host": "localhost", "port": port, "secret": secret}, session), 200, "probe")
	expect(do("POST", "/api/hub/plugins", map[string]any{"host": "localhost", "port": port, "secret": secret}, session), 200, "connect")
	if !strings.Contains(strings.Join(*calls, ","), "_connect admin") {
		t.Fatalf("no _connect: %v", *calls)
	}
	rec = do("GET", "/api/hub/plugins", "", session)
	if !strings.Contains(rec.Body.String(), `"name":"dns"`) || !strings.Contains(rec.Body.String(), `"zone_add"`) {
		t.Fatalf("list: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("secret in the list")
	}

	// Операции: вид раздела и действие.
	rec = do("POST", "/api/hub/plugins/dns/call", `{"op":"zones"}`, session)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "example.com") {
		t.Fatalf("view: %d %s", rec.Code, rec.Body)
	}
	expect(do("POST", "/api/hub/plugins/dns/call", `{"op":"nope"}`, session), 400, "unknown op")

	// Документ: правка с базой, история, конфликт.
	rec = do("GET", "/api/hub/plugins/dns/doc?get=zone_get&doc=example.com&args="+url.QueryEscape(`{"zone":"example.com"}`), "", session)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "203.0.113.10") {
		t.Fatalf("doc get: %d %s", rec.Code, rec.Body)
	}
	put := map[string]any{"get": "zone_get", "put": "zone_put", "doc": "example.com", "args": map[string]any{"zone": "example.com"},
		"text": "app A 203.0.113.11\n", "base": "app A 203.0.113.10\n", "note": "new address"}
	expect(do("PUT", "/api/hub/plugins/dns/doc", put, session), 200, "doc put")
	expect(do("PUT", "/api/hub/plugins/dns/doc", put, session), 409, "doc changed meanwhile")
	rec = do("GET", "/api/hub/plugins/dns/doc?get=zone_get&doc=example.com", "", session)
	var doc struct {
		Text     string             `json:"text"`
		Versions []pluginDocVersion `json:"versions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.Text != "app A 203.0.113.11\n" || len(doc.Versions) != 2 || doc.Versions[0].Text != "app A 203.0.113.10\n" || doc.Versions[1].Note != "new address" {
		t.Fatalf("history: %+v", doc)
	}

	// Токены: чтение видит только чтение; «по правам» с plugin.dns — и
	// действие; MCP — инструменты плагина.
	if rec := do("PUT", "/api/hub/experimental", `{"map":false,"clusters":false,"assistant":false,"mcp":true}`, session); rec.Code != 200 {
		t.Fatalf("mcp on: %d", rec.Code)
	}
	newToken := func(body string) func(*http.Request) {
		rec := do("POST", "/api/hub/tokens", body, session)
		expect(rec, 200, "token "+body)
		var c struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	}
	reader := newToken(`{"name":"reader","role":"read"}`)
	agent := newToken(`{"name":"dns-agent","role":"custom","perms":{"actions":["plugin.dns"]}}`)
	expect(do("POST", "/api/hub/tokens", `{"name":"x","role":"custom","perms":{"actions":["plugin.mail"]}}`, session), 400, "unknown plugin action")
	expect(do("POST", "/api/hub/plugins/dns/call", `{"op":"zones"}`, reader), 200, "reader view")
	expect(do("POST", "/api/hub/plugins/dns/call", `{"op":"zone_add","args":{"zone":"a.example"}}`, reader), 403, "reader action")
	expect(do("POST", "/api/hub/plugins/dns/call", `{"op":"zone_add","args":{"zone":"a.example"}}`, agent), 200, "agent action")
	if !strings.Contains(strings.Join(*calls, ","), "zone_add token:dns-agent") {
		t.Fatalf("user not passed: %v", *calls)
	}
	rec = do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, reader)
	if !strings.Contains(rec.Body.String(), `"dns_zones"`) || strings.Contains(rec.Body.String(), `"dns_zone_add"`) {
		t.Fatalf("reader tools: %.400s", rec.Body)
	}
	rec = do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dns_zone_add","arguments":{"zone":"b.example"}}}`, agent)
	if !strings.Contains(rec.Body.String(), "zone b.example added") {
		t.Fatalf("mcp call: %s", rec.Body)
	}
	rec = do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"dns_zone_add","arguments":{"zone":"c.example"}}}`, reader)
	if !strings.Contains(rec.Body.String(), `"isError":true`) {
		t.Fatalf("reader mcp action: %s", rec.Body)
	}

	// Сценарии: слово плагина разбирается, «Проверить» спрашивает плагин.
	rec = do("POST", "/api/hub/scripts/check", `{"content":"dns set app.example.com A 203.0.113.12\n"}`, session)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("script check: %d %s", rec.Code, rec.Body)
	}
	rec = do("POST", "/api/hub/scripts/check", `{"content":"dns set app.example.com\n"}`, session)
	if !strings.Contains(rec.Body.String(), "dns set NAME TYPE VALUE") {
		t.Fatalf("plugin check error not shown: %s", rec.Body)
	}

	// Отключение: инструментов и слова больше нет.
	expect(do("DELETE", "/api/hub/plugins/dns", "", session), 200, "disconnect")
	rec = do("POST", "/api/mcp", `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, reader)
	if strings.Contains(rec.Body.String(), `"dns_zones"`) {
		t.Fatal("tool after disconnect")
	}
	rec = do("POST", "/api/hub/scripts/check", `{"content":"dns set app.example.com A 203.0.113.12\n"}`, session)
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("word after disconnect: %s", rec.Body)
	}
	_ = db
}
