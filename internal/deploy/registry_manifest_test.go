package deploy

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryHost(t *testing.T) {
	for img, want := range map[string]string{
		"ghcr.io/piqab/rgstr:latest":         "ghcr.io",
		"postgres:16":                        "docker.io",
		"org/app":                            "docker.io",
		"docker.io/library/redis":            "docker.io",
		"localhost:5000/app":                 "localhost:5000",
		"registry.example.com/a/b@sha256:00": "registry.example.com",
	} {
		if got := RegistryHost(img); got != want {
			t.Errorf("%s: %s, ждали %s", img, got, want)
		}
	}
}

// Закрытый registry: 401 с вызовом Bearer → токен по логину и паролю →
// HEAD манифеста с токеном.
func TestManifestExistsBearer(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			u, p, _ := r.BasicAuth()
			if u != "piqab" || p != "secret" || !strings.Contains(r.URL.RawQuery, "scope=") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"token":"tok"}`))
		case r.Header.Get("Authorization") != "Bearer tok":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="reg",scope="repository:piqab/app:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead && r.URL.Path == "/v2/piqab/app/manifests/1.0":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := registryClient
	registryClient = srv.Client()
	defer func() { registryClient = old }()
	host := strings.TrimPrefix(srv.URL, "https://")
	ok, err := ManifestExists(context.Background(), host+"/piqab/app:1.0", RegistryAccess{Cred: "piqab:secret"})
	if err != nil || !ok {
		t.Fatalf("есть: %v %v", ok, err)
	}
	ok, err = ManifestExists(context.Background(), host+"/piqab/app:2.0", RegistryAccess{Cred: "piqab:secret"})
	if err != nil || ok {
		t.Fatalf("нет тега: %v %v", ok, err)
	}
	if _, err := ManifestExists(context.Background(), host+"/piqab/app:1.0", RegistryAccess{Cred: "piqab:wrong"}); err == nil {
		t.Error("неверный ключ принят")
	}
	if err := RegistryLogin(context.Background(), host, RegistryAccess{Cred: "piqab:secret"}); err != nil {
		t.Errorf("вход: %v", err)
	}
	if err := RegistryLogin(context.Background(), host, RegistryAccess{Cred: "piqab:wrong"}); err == nil {
		t.Error("вход с неверным ключом")
	}

	// Свой CA registry (сертификат тестового сервера) — без него TLS не
	// проходит, с ним — вход есть.
	registryClient = old
	if err := RegistryLogin(context.Background(), host, RegistryAccess{Cred: "piqab:secret"}); err == nil {
		t.Error("чужой сертификат принят без CA")
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	if err := RegistryLogin(context.Background(), host, RegistryAccess{Cred: "piqab:secret", CA: ca}); err != nil {
		t.Errorf("со своим CA: %v", err)
	}
}
