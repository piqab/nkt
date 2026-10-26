package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/catalog"
	"github.com/piqab/nkt/internal/catalog/catalogtest"
)

// Плагины и примеры конвейеров хаб берёт из каталога: тестам — каталог из
// examples/ репозитория, подписанный временным ключом.
func TestMain(m *testing.M) {
	if err := catalogtest.Use(); err != nil {
		fmt.Fprintln(os.Stderr, "catalog:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func pubPEM(k ed25519.PublicKey) string {
	der, _ := x509.MarshalPKIXPublicKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// Каталог в хабе: список с поставленными плагинами, справка и скриншоты из
// копии, настройки источника с проверкой, архив без интернета (только с
// доверенным ключом), скачивание с адреса из настроек, несовместимый nkt.
func TestCatalogEndpoints(t *testing.T) {
	defer func() { _ = catalogtest.Use() }()
	srv, _, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	do := func(method, path string, body []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	var cookie *http.Cookie
	for _, c := range do("POST", "/api/auth/login", []byte(`{"username":"admin","password":"admin-password-1234"}`), nil).Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}

	rec := do("GET", "/api/hub/catalog", nil, cookie)
	var list struct {
		URL     string `json:"url"`
		Plugins []struct {
			Key        string   `json:"key"`
			Version    string   `json:"version"`
			Compatible bool     `json:"compatible"`
			Readme     bool     `json:"readme"`
			Screens    []string `json:"screens"`
			Tests      string   `json:"tests"`
		} `json:"plugins"`
		Examples []struct {
			Key string          `json:"key"`
			UI  json.RawMessage `json:"ui"`
		} `json:"examples"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Plugins) != 8 || list.URL != DefaultCatalogURL || len(list.Examples) != 15 || list.Examples[0].Key != "httpbin" {
		t.Fatalf("catalog: %v %s", err, rec.Body)
	}
	var yc string
	for _, p := range list.Plugins {
		if !p.Readme || p.Tests != "passed" || !p.Compatible {
			t.Errorf("%s: %+v", p.Key, p)
		}
		if p.Key == "yandex-cloud" && len(p.Screens) == 1 {
			yc = p.Screens[0]
		}
	}
	if yc == "" {
		t.Fatal("no yandex-cloud screenshot")
	}
	if rec := do("GET", "/api/hub/catalog/files/yandex-cloud/"+yc, nil, cookie); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("screen: %d %s", rec.Code, rec.Header())
	}
	if rec := do("GET", "/api/hub/catalog/files/windows/README.md", nil, cookie); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Windows") {
		t.Fatalf("readme: %d", rec.Code)
	}
	for _, p := range []string{"windows/deploy/nkt-plugin.py", "windows/../../etc/passwd", "nope/README.md"} {
		if rec := do("GET", "/api/hub/catalog/files/"+p, nil, cookie); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	// Источник: плохой адрес и ключ — 400; «Сверить» ничего не пишет.
	if rec := do("POST", "/api/hub/catalog/settings", []byte(`{"url":"ftp://x"}`), cookie); rec.Code != 400 {
		t.Fatalf("bad url: %d", rec.Code)
	}
	if rec := do("POST", "/api/hub/catalog/settings", []byte(`{"keys":"nope"}`), cookie); rec.Code != 400 {
		t.Fatalf("bad keys: %d", rec.Code)
	}

	// Архив, подписанный чужим (не доверенным) ключом, — отказ.
	index, sig, files, pub, err := catalogtest.Signed(catalogtest.ExamplesDir())
	if err != nil {
		t.Fatal(err)
	}
	var arch bytes.Buffer
	gz := gzip.NewWriter(&arch)
	tw := tar.NewWriter(gz)
	put := func(name string, b []byte) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(b)
	}
	put("catalog/index.json", index)
	put("catalog/index.json.sig", sig)
	for p, b := range files {
		put("catalog/"+p, b)
	}
	_ = tw.Close()
	_ = gz.Close()
	if rec := do("POST", "/api/hub/catalog/upload", arch.Bytes(), cookie); rec.Code != 400 {
		t.Fatalf("untrusted archive: %d %s", rec.Code, rec.Body)
	}
	// Свой ключ в настройках — архив принят, копия на диске хаба.
	body, _ := json.Marshal(map[string]string{"keys": pubPEM(pub)})
	if rec := do("POST", "/api/hub/catalog/settings", body, cookie); rec.Code != 200 {
		t.Fatalf("keys: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/hub/catalog/upload", arch.Bytes(), cookie); rec.Code != 200 {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body)
	}
	if catalog.Current().Source != "upload" {
		t.Fatal("upload not current")
	}
	if _, err := catalog.Load(srv.catalogDir(), srv.catalogKeys(ctx)); err != nil {
		t.Fatalf("copy: %v", err)
	}

	// Скачивание с адреса из настроек.
	cat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/c/")
		switch p {
		case "index.json":
			_, _ = w.Write(index)
		case "index.json.sig":
			_, _ = w.Write(sig)
		default:
			if b, ok := files[p]; ok {
				_, _ = w.Write(b)
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer cat.Close()
	body, _ = json.Marshal(map[string]string{"url": cat.URL + "/c/", "keys": pubPEM(pub)})
	if rec := do("POST", "/api/hub/catalog/settings", body, cookie); rec.Code != 200 {
		t.Fatalf("url: %d %s", rec.Code, rec.Body)
	}
	if err := srv.refreshCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if catalog.Current().Source != cat.URL+"/c/" {
		t.Fatalf("source %s", catalog.Current().Source)
	}
	rec = do("GET", "/api/hub/catalog/settings", nil, cookie)
	if !strings.Contains(rec.Body.String(), `"history":[{`) {
		t.Fatalf("history: %s", rec.Body)
	}

	// Плагину нужен nkt новее хаба — установка 409.
	srv.hub.version = "1.15.0"
	defer func() { srv.hub.version = "" }()
	rec = do("POST", "/api/hub/plugins/install", []byte(`{"package":"windows","host":"localhost"}`), cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "1.16.0") {
		t.Fatalf("incompatible: %d %s", rec.Code, rec.Body)
	}
	// Каталога нет — понятная ошибка.
	catalog.SetCurrent(nil)
	srv.hub.version = ""
	rec = do("POST", "/api/hub/plugins/install", []byte(`{"package":"windows","host":"localhost"}`), cookie)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "catalog") && !strings.Contains(rec.Body.String(), "каталог") {
		t.Fatalf("no catalog: %d %s", rec.Code, rec.Body)
	}
}
