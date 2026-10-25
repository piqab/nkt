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

// Секрет от плагина: показ один раз — только окну (сессии), токену —
// отказ без to_pipeline; с to_pipeline — хаб пишет значение в .env
// конвейера (права проверены до вызова плагина), в ответе значения нет.
func TestPluginRevealAndStore(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	const secret = "plugin-secret-0123456789"
	calls := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !plugin.Verify(secret, r, body, time.Now()) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == plugin.PathManifest {
			_ = json.NewEncoder(w).Encode(plugin.Manifest{Protocol: 1, Name: "llm", Title: "LLM", Version: "1",
				Ops: []plugin.Op{{Name: "key_generate", Reveals: true, MCP: true}}})
			return
		}
		var req plugin.Request
		_ = json.Unmarshal(body, &req)
		calls++
		resp := plugin.Response{OK: true, Text: "key created"}
		if p, _ := req.Args["to_pipeline"].(string); p != "" {
			env, _ := req.Args["env"].(string)
			resp.Store = &plugin.Store{Pipeline: p, Env: env, Value: "sk-stored-value-123"}
		} else {
			resp.Reveal = &plugin.Reveal{Value: "sk-revealed-value-456"}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer fake.Close()
	u, _ := url.Parse(fake.URL)
	port, _ := strconv.Atoi(u.Port())
	enc, _ := secretbox.Encrypt(srv.hub.key, []byte(secret))
	_ = srv.savePlugins(ctx, []pluginConf{{Name: "llm", Host: localHostName, Port: port, SecretEnc: enc}})
	forgetManifests("llm")
	hookSecret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	envEnc, _ := secretbox.Encrypt(srv.hub.key, []byte("POSTGRES_PASSWORD=pw\n"))
	compose := "repo: https://codeberg.org/me/app.git\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: app\n  hosts: [localhost]\n"
	appID, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "app", Content: compose, HookID: "hook-store-0001", HookSecret: hookSecret, Author: "admin"})
	_ = db.SetPipelineEnv(ctx, appID, envEnc)
	_, _ = db.CreatePipeline(ctx, store.Pipeline{Name: "other", Content: strings.ReplaceAll(compose, "app", "other"), HookID: "hook-store-0002", HookSecret: hookSecret, Author: "admin"})

	do := func(path string, body any, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		if s, ok := body.(string); ok {
			raw = []byte(s)
		}
		req := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		if set != nil {
			set(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("/api/auth/login", `{"username":"admin","password":"admin-password-1234"}`, nil)
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
	call := func(args map[string]any, set func(*http.Request)) *httptest.ResponseRecorder {
		return do("/api/hub/plugins/llm/call", map[string]any{"op": "key_generate", "args": args}, set)
	}

	// Окно: показ один раз.
	rec = call(map[string]any{"alias": "a"}, session)
	expect(rec, 200, "session reveal")
	if !strings.Contains(rec.Body.String(), "sk-revealed-value-456") {
		t.Fatalf("no reveal: %s", rec.Body)
	}
	// Токен «по правам» с plugin.llm и pipeline.create для «app».
	rec = do("/api/hub/tokens", `{"name":"agent","role":"custom","perms":{"actions":["plugin.llm","pipeline.create"],"pipelines":["app"]}}`, session)
	expect(rec, 200, "token")
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token) }
	before := calls
	expect(call(map[string]any{"alias": "b"}, bearer), 403, "token without to_pipeline")
	expect(call(map[string]any{"alias": "b", "to_pipeline": "other", "env": "LLM_KEY"}, bearer), 403, "foreign pipeline")
	expect(call(map[string]any{"alias": "b", "to_pipeline": "app", "env": "bad-name"}, bearer), 403, "bad env name")
	expect(call(map[string]any{"alias": "b", "to_pipeline": "ghost", "env": "LLM_KEY"}, bearer), 403, "no pipeline")
	if calls != before {
		t.Fatalf("plugin called %d times before the rights check", calls-before)
	}
	rec = call(map[string]any{"alias": "b", "to_pipeline": "app", "env": "LLM_KEY"}, bearer)
	expect(rec, 200, "store")
	if strings.Contains(rec.Body.String(), "sk-stored-value-123") || !strings.Contains(rec.Body.String(), "LLM_KEY") {
		t.Fatalf("store reply: %s", rec.Body)
	}
	p, _ := db.PipelineByID(ctx, appID)
	raw, _ := secretbox.Decrypt(srv.hub.key, p.EnvEnc)
	if !strings.Contains(string(raw), "LLM_KEY=sk-stored-value-123") || !strings.Contains(string(raw), "POSTGRES_PASSWORD=pw") {
		t.Fatalf("env:\n%s", raw)
	}
	if v := db.LatestEnvVersion(ctx, appID); v == 0 {
		t.Fatal("no env version")
	}
	// В журнале действий значения нет.
	entries, _ := db.ListAudit(ctx, store.AuditFilter{Limit: 50})
	for _, e := range entries {
		if b, _ := json.Marshal(e); strings.Contains(string(b), "sk-stored-value") || strings.Contains(string(b), "sk-revealed-value") {
			t.Fatalf("secret in audit: %s", b)
		}
	}
}
