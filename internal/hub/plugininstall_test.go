package hub

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/plugin"
)

// Установка без Docker на машину хаба (локальный агент на фикстурах):
// каталог, обязательные поля, секреты не в параметрах задания, файлы и
// служба на «хосте», подключение к плагину (поддельный плагин на 9180
// берёт секрет из записанного файла настроек), удаление.
func TestPluginInstallNative(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:9180")
	if err != nil {
		t.Skip("port 9180 busy:", err)
	}
	srv, db, root := localFixtureHub(t)
	h := srv.Handler()
	envPath := filepath.Join(root, "etc/nkt-plugins/dns.env")
	secret := func() string {
		f, err := os.Open(envPath)
		if err != nil {
			return ""
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "NKT_PLUGIN_SECRET="); ok {
				return v
			}
		}
		return ""
	}
	fake := &httptest.Server{Listener: ln, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !plugin.Verify(secret(), r, body, time.Now()) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == plugin.PathManifest {
			_ = json.NewEncoder(w).Encode(plugin.Manifest{Protocol: 1, Name: "dns", Title: "DNS", Version: "1.0.0", Ops: []plugin.Op{{Name: "zones", ReadOnly: true}}})
			return
		}
		_ = json.NewEncoder(w).Encode(plugin.Response{OK: true})
	})}}
	fake.Start()
	defer fake.Close()

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

	rec = do("GET", "/api/hub/plugins/catalog", nil, session)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"DNS_API_TOKEN"`) {
		t.Fatalf("catalog: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/hub/plugins/install", map[string]any{"package": "technitium", "host": "localhost"}, session); rec.Code != 400 {
		t.Fatalf("without token: %d %s", rec.Code, rec.Body)
	}
	const token = "technitium-api-token-0123456789"
	rec = do("POST", "/api/hub/plugins/install", map[string]any{"package": "technitium", "host": "localhost",
		"settings": map[string]string{"DNS_API_TOKEN": token, "DNS_DOMAIN": "ns1.example.com"}}, session)
	if rec.Code != 200 {
		t.Fatalf("install: %d %s", rec.Code, rec.Body)
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	job := waitJobDone(t, db, started.JobID)
	if job.Status != "succeeded" {
		lines, _ := db.JobLog(t.Context(), job.ID, 0, 100)
		t.Fatalf("job %s: %s %+v", job.Status, job.Error, lines)
	}
	if strings.Contains(string(job.Params), token) || strings.Contains(string(job.Params), secret()) {
		t.Fatalf("secret in job params: %s", job.Params)
	}
	raw, _ := os.ReadFile(envPath)
	if !strings.Contains(string(raw), "DNS_API_TOKEN="+token) || !strings.Contains(string(raw), "DNS_API=http://127.0.0.1:5380/api") {
		t.Fatalf("env:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(root, "opt/nkt-plugins/dns/nkt-plugin.py")); err != nil {
		t.Fatal(err)
	}
	p, ok := srv.pluginByName(t.Context(), "dns")
	if !ok || p.Native != "technitium" || p.Host != localHostName {
		t.Fatalf("connected: %+v %v", p, ok)
	}
	// Обновление и удаление.
	rec = do("POST", "/api/hub/plugins/dns/native/update", nil, session)
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if job := waitJobDone(t, db, started.JobID); job.Status != "succeeded" {
		t.Fatalf("update: %s %s", job.Status, job.Error)
	}
	rec = do("POST", "/api/hub/plugins/dns/native/remove", nil, session)
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if job := waitJobDone(t, db, started.JobID); job.Status != "succeeded" {
		t.Fatalf("remove: %s %s", job.Status, job.Error)
	}
	if _, ok := srv.pluginByName(t.Context(), "dns"); ok {
		t.Fatal("still connected")
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatal("env left on the host")
	}
}
