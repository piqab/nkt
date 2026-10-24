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
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Токен «по правам» с «создание конвейеров»: создаёт compose на своих
// хостах, правит и удаляет только свои; чужой — 403; k8s — нет.
func TestTokenCreatesOwnPipelines(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	compose := "repo: https://codeberg.org/me/NAME.git\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: NAME\n  hosts: [localhost]\n"
	foreignID, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "foreign", Content: strings.ReplaceAll(compose, "NAME", "foreign"), HookID: "hook-own-0001", HookSecret: secret, Author: "admin"})

	do := func(method, path string, body any, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		if s, ok := body.(string); ok {
			raw = []byte(s)
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
	rec = do("POST", "/api/hub/tokens", `{"name":"agent","role":"custom","perms":{"actions":["pipeline.create","pipeline.deploy"],"pipelines":["foreign"]}}`, session)
	if rec.Code != 200 {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}
	var c struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	expect := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: want %d, got %d %s", what, code, rec.Code, rec.Body)
		}
	}

	// Примеры — чтение.
	rec = do("GET", "/api/hub/pipelines/examples?key=forgejo", "", bearer)
	expect(rec, 200, "examples")
	if !strings.Contains(rec.Body.String(), "content") {
		t.Fatalf("example without content: %s", rec.Body)
	}

	// Не compose — отказ.
	expect(do("POST", "/api/hub/pipelines", map[string]string{"name": "k", "content": "repo: https://codeberg.org/me/k.git\nref: main\naction: manifest\nmanifests: [k.yaml]\nclusters: [prod]\n"}, bearer), 403, "manifest pipeline")
	// Свой compose — создан, автор — токен.
	rec = do("POST", "/api/hub/pipelines", map[string]string{"name": "mine", "content": strings.ReplaceAll(compose, "NAME", "mine")}, bearer)
	expect(rec, 200, "create own")
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	p, err := db.PipelineByID(ctx, created.ID)
	if err != nil || p.Author != "token:agent" {
		t.Fatalf("author: %+v %v", p, err)
	}
	// Свой — правится, ответ с диффом.
	rec = do("PUT", "/api/hub/pipelines/"+strconv.FormatInt(created.ID, 10), map[string]string{"content": strings.ReplaceAll(compose, "NAME", "mine") + "# note\n"}, bearer)
	expect(rec, 200, "update own")
	if !strings.Contains(rec.Body.String(), "+# note") {
		t.Fatalf("no diff: %s", rec.Body)
	}
	// Чужой — нет, хотя он в списке токена (выкладывать — можно).
	rec = do("PUT", "/api/hub/pipelines/"+strconv.FormatInt(foreignID, 10), map[string]string{"content": strings.ReplaceAll(compose, "NAME", "foreign")}, bearer)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "foreign") {
		t.Fatalf("foreign update: %d %s", rec.Code, rec.Body)
	}
	expect(do("POST", "/api/hub/pipelines/"+strconv.FormatInt(foreignID, 10)+"/remove", `{"all":true}`, bearer), 403, "foreign remove")
	// Свой — в правах выкладки, хотя его нет в списке pipelines.
	list, _ := db.ListAPITokens(ctx)
	var tok store.APIToken
	for _, x := range list {
		if x.Name == "agent" {
			tok = x
		}
	}
	if err := tokenPipelineAllowed(tok, p); err != nil || tok.Name == "" {
		t.Fatalf("own pipeline not allowed to deploy: %v", err)
	}
	if err := tokenPipelineAllowed(tok, store.Pipeline{Name: "third", Author: "admin"}); err == nil {
		t.Fatal("pipeline outside the list allowed")
	}
	// Сухой прогон текста — можно (compose на своих хостах).
	rec = do("POST", "/api/hub/pipelines/dryrun", map[string]string{"content": "repo: https://codeberg.org/me/k.git\nref: main\naction: manifest\nmanifests: [k.yaml]\nclusters: [prod]\n"}, bearer)
	expect(rec, 403, "dry run of a manifest text")
}
