package deploy

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Индекс манифестов с архитектурами; вход — по вызову Bearer.
func TestManifestPlatforms(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"token":"t1"}`))
		case r.Header.Get("Authorization") != "Bearer t1":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="reg",scope="repository:org/app:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v2/org/app/manifests/1.0":
			_, _ = w.Write([]byte(`{"manifests":[{"platform":{"architecture":"arm64","os":"linux"}},{"platform":{"architecture":"amd64","os":"linux"}},{"platform":{"architecture":"unknown","os":"unknown"}},{"platform":{"architecture":"amd64","os":"windows"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	host := strings.TrimPrefix(srv.URL, "https://")
	acc := RegistryAccess{CA: ca}
	ok, arches, err := ManifestPlatforms(context.Background(), host+"/org/app:1.0", acc)
	if err != nil || !ok || strings.Join(arches, ",") != "amd64,arm64" {
		t.Fatalf("%v %v %v", ok, arches, err)
	}
	ok, _, err = ManifestPlatforms(context.Background(), host+"/org/app:2.0", acc)
	if err != nil || ok {
		t.Fatalf("missing tag: %v %v", ok, err)
	}
}

// Настоящий Docker Hub (по NKT_NET_TEST=1): индекс postgres — быстро.
func TestManifestPlatformsDockerHub(t *testing.T) {
	if os.Getenv("NKT_NET_TEST") == "" {
		t.Skip("сеть: NKT_NET_TEST=1")
	}
	for _, img := range []string{"postgres:17-alpine", "lakhansamani/authorizer:2.4.2"} {
		start := time.Now()
		ok, arches, err := ManifestPlatforms(context.Background(), img, RegistryAccess{})
		t.Logf("%s: %v %v %v in %v", img, ok, arches, err, time.Since(start))
		if err != nil || !ok {
			t.Fatalf("%s: %v %v", img, ok, err)
		}
	}
}
