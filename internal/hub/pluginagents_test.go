package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/plugin"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// «Для агентов»: инструменты плагина — те же, что в tools/list MCP (имя,
// описание, схема, подсказки), у меняющих — действие токена; операции без
// mcp — нет; команды сценариев — из манифеста.
func TestPluginAgents(t *testing.T) {
	srv, _, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	const secret = "plugin-secret-0123456789"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !plugin.Verify(secret, r, body, time.Now()) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(plugin.Manifest{Protocol: 1, Name: "my-dns", Title: "My DNS", Version: "2.0",
			Ops: []plugin.Op{
				{Name: "zones", ReadOnly: true, MCP: true, Description: "List zones."},
				{Name: "record_delete", MCP: true, Destructive: true, Description: "Delete a record.",
					Input: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}}},
				{Name: "internal", ReadOnly: true},
			},
			Commands: []plugin.Command{{Word: "mydns", Usage: "mydns rm NAME", Example: "mydns rm a.example.com"}}})
	}))
	defer fake.Close()
	u, _ := url.Parse(fake.URL)
	port, _ := strconv.Atoi(u.Port())
	enc, _ := secretbox.Encrypt(srv.hub.key, []byte(secret))
	_ = srv.savePlugins(ctx, []pluginConf{{Name: "my-dns", Host: localHostName, Port: port, SecretEnc: enc}})
	forgetManifests("my-dns")

	login := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"admin-password-1234"}`))
	login.Header.Set("Content-Type", "application/json")
	lr := httptest.NewRecorder()
	h.ServeHTTP(lr, login)
	req := httptest.NewRequest("GET", "/api/hub/plugins/my-dns/agents", nil)
	for _, c := range lr.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			req.AddCookie(c)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("agents: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Tools []struct {
			Tool        map[string]any `json:"tool"`
			Action      string         `json:"action"`
			Destructive bool           `json:"destructive"`
		} `json:"tools"`
		Commands []plugin.Command `json:"commands"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Tools) != 2 || out.Tools[0].Tool["name"] != "my_dns_zones" || out.Tools[0].Action != "" ||
		out.Tools[1].Tool["name"] != "my_dns_record_delete" || out.Tools[1].Action != "plugin.my-dns" || !out.Tools[1].Destructive {
		t.Fatalf("tools: %s", rec.Body)
	}
	if len(out.Commands) != 1 || out.Commands[0].Example == "" {
		t.Fatalf("commands: %+v", out.Commands)
	}
	// Ровно то, что получает агент с токеном администратора.
	mcp := srv.pluginTools(ctx, store.APIToken{Role: store.TokenRoleAdmin})
	a, _ := json.Marshal(mcp)
	b, _ := json.Marshal([]map[string]any{out.Tools[0].Tool, out.Tools[1].Tool})
	if string(a) != string(b) {
		t.Fatalf("differs from MCP:\n%s\n%s", a, b)
	}
}
