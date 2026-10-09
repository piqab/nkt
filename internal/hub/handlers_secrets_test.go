package hub

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// «Секреты»: ключи репозиториев (основной и по префиксу) и registry
// списком — добавить, поправить (пустой токен — прежний), удалить; в
// ответе только вид ключа и хвост токена, сами значения не уходят.
func TestPipelineSecretsAPI(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	pid, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "app", Content: "repo: https://git.example.com/team/app.git\nref: main\naction: compose\n",
		HookID: "hook-sec-1", HookSecret: secret, Author: "admin"})
	call := func(h http.HandlerFunc, method, query string, body any) (int, string) {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, "/x"+query, &buf)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", strconv.FormatInt(pid, 10))
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code, rec.Body.String()
	}
	ok := func(code int, body string) {
		t.Helper()
		if code != http.StatusOK {
			t.Fatalf("%d %s", code, body)
		}
	}
	ok(call(srv.handlePipelineSecretRepo, "PUT", "", map[string]string{"token": "ghp_mainmain1234"}))
	ok(call(srv.handlePipelineSecretRepo, "PUT", "", map[string]string{"prefix": "https://github.com/vendor", "ssh_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n"}))
	ok(call(srv.handlePipelineSecretRegistry, "PUT", "", map[string]string{"host": "Harbor.example.com:8443", "user": "robot$team+deploy", "token": "tok-harbor-9f3c",
		"ca": testCAPEM(t)}))
	ok(call(srv.handlePipelineSecretRegistry, "PUT", "", map[string]string{"host": "ghcr.io", "user": "piqab", "token": "ghp_registry7Qe"}))

	code, body := call(srv.handlePipelineSecrets, "GET", "", nil)
	ok(code, body)
	for _, leak := range []string{"ghp_mainmain1234", "OPENSSH", "tok-harbor-9f3c", "BEGIN CERTIFICATE"} {
		if strings.Contains(body, leak) {
			t.Fatalf("значение в ответе: %s\n%s", leak, body)
		}
	}
	var got struct {
		Repos      []secretsRepo     `json:"repos"`
		Registries []secretsRegistry `json:"registries"`
	}
	_ = json.Unmarshal([]byte(body), &got)
	if len(got.Repos) != 2 || got.Repos[0].Prefix != "" || got.Repos[0].Hint != "1234" || got.Repos[1].Prefix != "github.com/vendor/" || got.Repos[1].Kind != "ssh" {
		t.Fatalf("repos: %+v", got.Repos)
	}
	if len(got.Registries) != 2 || got.Registries[1].Host != "harbor.example.com:8443" || !got.Registries[1].CA || got.Registries[1].Hint != "9f3c" {
		t.Fatalf("registries: %+v", got.Registries)
	}

	// Правка без токена — прежний токен и CA; смена логина.
	ok(call(srv.handlePipelineSecretRegistry, "PUT", "", map[string]string{"host": "harbor.example.com:8443", "old_host": "harbor.example.com:8443", "user": "robot$team+ci"}))
	pl, _ := db.PipelineByID(ctx, pid)
	keys := srv.pipelineRegistries(pl)
	if len(keys) != 2 || keys[1].User != "robot$team+ci" || keys[1].Token != "tok-harbor-9f3c" || keys[1].CA == "" {
		t.Fatalf("правка: %+v", keys)
	}
	ok(call(srv.handlePipelineSecretRegistry, "DELETE", "?host=ghcr.io", nil))
	ok(call(srv.handlePipelineSecretRepo, "DELETE", "?prefix=github.com/vendor/", nil))
	pl, _ = db.PipelineByID(ctx, pid)
	if len(srv.pipelineRegistries(pl)) != 1 || len(srv.pipelineRepoCreds(pl)) != 0 || srv.pipelineCred(pl).Token == "" {
		t.Fatalf("удаление: %+v %+v", srv.pipelineRegistries(pl), srv.pipelineRepoCreds(pl))
	}
	for _, bad := range []map[string]string{
		{"host": "evil host", "user": "u", "token": "t"},
		{"host": "ghcr.io", "user": "a:b", "token": "t"},
		{"host": "ghcr.io", "user": "u", "token": "t", "ca": "not pem"},
	} {
		if code, _ := call(srv.handlePipelineSecretRegistry, "PUT", "", bad); code != http.StatusBadRequest {
			t.Errorf("принято: %v (%d)", bad, code)
		}
	}
	if code, _ := call(srv.handlePipelineSecretRepo, "PUT", "", map[string]string{"prefix": "github.com/x"}); code != http.StatusBadRequest {
		t.Error("пустой ключ принят")
	}
}

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "harbor CA"}, IsCA: true,
		NotBefore: time.Now(), NotAfter: time.Now().AddDate(1, 0, 0), BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
