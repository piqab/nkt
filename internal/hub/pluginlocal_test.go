package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/catalog"
	"github.com/piqab/nkt/internal/store"
)

// sampleTgz — examples/_samples/url-check архивом; edit меняет файлы.
func sampleTgz(t *testing.T, edit func(rel string, b []byte) []byte) []byte {
	t.Helper()
	dir := filepath.Join("..", "..", "examples", "_samples", "url-check")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || strings.Contains(p, "__pycache__") {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		b, _ := os.ReadFile(p)
		if edit != nil {
			b = edit(rel, b)
		}
		_ = tw.WriteHeader(&tar.Header{Name: "url-check/" + rel, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(b)
		return nil
	})
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// Свой плагин: проверка → код и жетон → «Код просмотрен» → сохранение;
// в каталоге и в установке — «local:url-check»; та же версия и имя из
// каталога — замечания; не администратор — отказ; удаление при
// установленном — 409; в экспорте — plugins.local.
func TestLocalPluginUpload(t *testing.T) {
	defer catalog.SetLocal(map[string]catalog.LocalPlugin{})
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	login := func(user, pass string) *http.Cookie {
		req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.SessionCookie {
				return c
			}
		}
		t.Fatalf("login %s: %s", user, rec.Body)
		return nil
	}
	do := func(method, path string, body []byte, ck *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(ck)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	admin := login("admin", "admin-password-1234")
	hash, _ := auth.HashPassword("viewer-password-1234")
	if _, err := db.CreateUser(ctx, "viewer", hash, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	viewer := login("viewer", "viewer-password-1234")

	archive := sampleTgz(t, nil)
	if rec := do("POST", "/api/hub/plugins/local/check", archive, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer upload: %d", rec.Code)
	}
	rec := do("POST", "/api/hub/plugins/local/check", archive, admin)
	var chk struct {
		Name, Version, Code, Ticket string
		Problems                    []string
		Skipped                     []string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &chk); err != nil || rec.Code != 200 || chk.Name != "url-check" || chk.Ticket == "" || len(chk.Problems) > 0 ||
		!strings.Contains(chk.Code, "def probe") || len(chk.Skipped) != 0 {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/hub/plugins/local/save", []byte(`{"ticket":"`+chk.Ticket+`"}`), admin); rec.Code != 400 {
		t.Fatalf("save without review: %d", rec.Code)
	}
	// Жетон одноразовый: после отказа — заново.
	rec = do("POST", "/api/hub/plugins/local/check", archive, admin)
	_ = json.Unmarshal(rec.Body.Bytes(), &chk)
	if rec := do("POST", "/api/hub/plugins/local/save", []byte(`{"ticket":"`+chk.Ticket+`","reviewed":true}`), admin); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/hub/plugins/local/save", []byte(`{"ticket":"`+chk.Ticket+`","reviewed":true}`), admin); rec.Code != 400 {
		t.Fatalf("ticket reused: %d", rec.Code)
	}
	// В каталоге и в окне установки — свой.
	if b := do("GET", "/api/hub/catalog", nil, admin).Body.String(); !strings.Contains(b, `"key":"local:url-check"`) || !strings.Contains(b, `"local":true`) {
		t.Fatalf("catalog: %s", b)
	}
	if b := do("GET", "/api/hub/plugins/catalog", nil, admin).Body.String(); !strings.Contains(b, `"key":"local:url-check"`) {
		t.Fatalf("install list: %s", b)
	}
	if rec := do("GET", "/api/hub/catalog/files/local:url-check/README.en.md", nil, admin); rec.Code != 200 || !strings.Contains(rec.Body.String(), "URL check") {
		t.Fatalf("readme: %d", rec.Code)
	}
	if pkg, ok := pluginPackage("local:url-check"); !ok || !bytes.Contains(pkg.Content, []byte("def probe")) {
		t.Fatal("package")
	}
	if _, kept := catalog.Local()["url-check"].Files["deploy/test_plugin.py"]; kept {
		t.Fatal("the test is stored on the hub")
	}
	// Та же версия — замечание; имя плагина каталога — замечание.
	rec = do("POST", "/api/hub/plugins/local/check", archive, admin)
	if !strings.Contains(rec.Body.String(), "1.0.0") || strings.Contains(rec.Body.String(), `"ticket"`) {
		t.Fatalf("same version: %s", rec.Body)
	}
	taken := sampleTgz(t, func(rel string, b []byte) []byte {
		if rel == "deploy/nkt-plugin.json" {
			return bytes.Replace(b, []byte(`"name": "url-check"`), []byte(`"name": "windows"`), 1)
		}
		return b
	})
	if rec := do("POST", "/api/hub/plugins/local/check", taken, admin); !strings.Contains(rec.Body.String(), "windows") || strings.Contains(rec.Body.String(), `"ticket"`) {
		t.Fatalf("catalog name: %s", rec.Body)
	}
	// Новая версия — дифф с прежней, история из двух версий.
	v2 := sampleTgz(t, func(rel string, b []byte) []byte {
		b = bytes.Replace(b, []byte(`"version": "1.0.0"`), []byte(`"version": "1.0.1"`), 1)
		return bytes.Replace(b, []byte(`VERSION = "1.0.0"`), []byte(`VERSION = "1.0.1"`), 1)
	})
	rec = do("POST", "/api/hub/plugins/local/check", v2, admin)
	var chk2 struct {
		PrevVersion string `json:"prev_version"`
		PrevCode    string `json:"prev_code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &chk2)
	var tk struct{ Ticket string }
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)
	if chk2.PrevVersion != "1.0.0" || !strings.Contains(chk2.PrevCode, "def probe") || tk.Ticket == "" {
		t.Fatalf("v2 check: %s", rec.Body)
	}
	if rec := do("POST", "/api/hub/plugins/local/save", []byte(`{"ticket":"`+tk.Ticket+`","reviewed":true}`), admin); rec.Code != 200 {
		t.Fatalf("v2 save: %s", rec.Body)
	}
	if lp := catalog.Local()["url-check"]; lp.Plugin.Version != "1.0.1" || len(lp.Versions) != 2 {
		t.Fatalf("history: %+v", lp.Versions)
	}
	// Экспорт везёт свои плагины.
	exp, err := db.ExportHosts(ctx)
	if err != nil || !strings.Contains(exp.Settings["plugins.local"], "url-check") {
		t.Fatalf("export: %v", err)
	}
	// Стоит на хосте — удалить можно только с подтверждением.
	_ = srv.savePlugins(ctx, []pluginConf{{Name: "url-check", Host: localHostName, Port: 9298, Native: "local:url-check"}})
	if rec := do("DELETE", "/api/hub/plugins/local/url-check", nil, admin); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "url-check@localhost") {
		t.Fatalf("delete installed: %d %s", rec.Code, rec.Body)
	}
	if rec := do("DELETE", "/api/hub/plugins/local/url-check?force=1", nil, admin); rec.Code != 200 {
		t.Fatalf("force delete: %d", rec.Code)
	}
	if _, ok := catalog.Local()["url-check"]; ok {
		t.Fatal("still there")
	}
}
