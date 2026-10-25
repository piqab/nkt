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
	pluginStartWait = 2 * time.Second
	defer func() { pluginStartWait = 45 * time.Second }()
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
			// «Неверная» настройка — плагин не поднимается (откат настроек).
			if raw, _ := os.ReadFile(envPath); strings.Contains(string(raw), "DNS_DOMAIN=bad.example.com") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
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
	// Каталог хаба новее поставленного — пометка и «Обновить все».
	pluginsMu.Lock()
	list := srv.plugins(t.Context())
	list[0].NativeSHA = "old"
	_ = srv.savePlugins(t.Context(), list)
	pluginsMu.Unlock()
	if rec := do("GET", "/api/hub/plugins", nil, session); !strings.Contains(rec.Body.String(), `"outdated":true`) {
		t.Fatalf("no outdated mark: %s", rec.Body)
	}
	rec = do("POST", "/api/hub/plugins/native/update-all", nil, session)
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if job := waitJobDone(t, db, started.JobID); job.Status != "succeeded" {
		t.Fatalf("update all: %s %s", job.Status, job.Error)
	}
	if rec := do("GET", "/api/hub/plugins", nil, session); strings.Contains(rec.Body.String(), `"outdated":true`) {
		t.Fatalf("still outdated: %s", rec.Body)
	}
	expectCode := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: %d %s", what, rec.Code, rec.Body)
		}
	}
	expectCode(do("POST", "/api/hub/plugins/native/update-all", nil, session), 400, "nothing to update")
	// Второй экземпляр на том же хосте — свой порт (9181).
	rec = do("POST", "/api/hub/plugins/install", map[string]any{"package": "technitium", "host": "localhost", "name": "dns2",
		"settings": map[string]string{"DNS_API_TOKEN": token}}, session)
	expectCode(rec, 200, "second instance")
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	j, _ := db.JobByID(t.Context(), started.JobID)
	if !strings.Contains(string(j.Params), `"port":9181`) {
		t.Fatalf("second port: %s", j.Params)
	}
	waitJobDone(t, db, started.JobID) // не подключится: поддельный плагин только на 9180
	// Хаб в контейнере — на свою машину не ставит.
	hubInContainer = func() bool { return true }
	defer func() { hubInContainer = func() bool { return false } }()
	expectCode(do("POST", "/api/hub/plugins/install", map[string]any{"package": "technitium", "host": "localhost", "name": "dns3",
		"settings": map[string]string{"DNS_API_TOKEN": token}}, session), 400, "hub in a container")
	hubInContainer = func() bool { return false }
	// «Настройки»: обычные значения видны, секрет — только «задан».
	rec = do("GET", "/api/hub/plugins/dns/settings", nil, session)
	expectCode(rec, 200, "settings")
	if b := rec.Body.String(); !strings.Contains(b, `"value":"ns1.example.com"`) || strings.Contains(b, token) || strings.Contains(b, secret()) ||
		!strings.Contains(b, `"env":"DNS_API_TOKEN","title":`) || !strings.Contains(b, `"set":true`) {
		t.Fatalf("settings: %s", b)
	}
	rec = do("POST", "/api/hub/plugins/dns/settings", map[string]any{"dry_run": true, "settings": map[string]string{"DNS_DOMAIN": "ns9.example.com"}}, session)
	if b := rec.Body.String(); rec.Code != 200 || !strings.Contains(b, `"old":"ns1.example.com","new":"ns9.example.com"`) || strings.Count(b, `"env"`) != 1 {
		t.Fatalf("settings dry run: %d %s", rec.Code, b)
	}
	expectCode(do("POST", "/api/hub/plugins/dns/settings", map[string]any{"settings": map[string]string{"DNS_DOMAIN": "ns1.example.com"}}, session), 400, "nothing changed")
	expectCode(do("POST", "/api/hub/plugins/dns/settings", map[string]any{"settings": map[string]string{"NKT_PLUGIN_SECRET": "x"}}, session), 400, "hub key")
	plugSecret := secret()
	const token2 = "technitium-api-token-second-987"
	rec = do("POST", "/api/hub/plugins/dns/settings", map[string]any{"settings": map[string]string{"DNS_DOMAIN": "ns9.example.com", "DNS_API_TOKEN": token2}}, session)
	expectCode(rec, 200, "settings apply")
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	job = waitJobDone(t, db, started.JobID)
	if job.Status != "succeeded" {
		lines, _ := db.JobLog(t.Context(), job.ID, 0, 100)
		t.Fatalf("settings job %s: %s %+v", job.Status, job.Error, lines)
	}
	if strings.Contains(string(job.Params), token2) {
		t.Fatalf("secret in job params: %s", job.Params)
	}
	raw, _ = os.ReadFile(envPath)
	if !strings.Contains(string(raw), "DNS_DOMAIN=ns9.example.com") || !strings.Contains(string(raw), "DNS_API_TOKEN="+token2) || secret() != plugSecret {
		t.Fatalf("env after settings:\n%s", raw)
	}
	if hist := srv.pluginSettingsHistory(t.Context(), "dns"); len(hist) != 2 || hist[0].Values["DNS_DOMAIN"] != "ns9.example.com" ||
		hist[1].Values["DNS_DOMAIN"] != "ns1.example.com" || hist[0].Values["DNS_API_TOKEN"] != "" {
		t.Fatalf("history: %+v", hist)
	}
	// Плагин не поднялся — прежние настройки вернулись.
	rec = do("POST", "/api/hub/plugins/dns/settings", map[string]any{"settings": map[string]string{"DNS_DOMAIN": "bad.example.com"}}, session)
	expectCode(rec, 200, "bad settings")
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if job := waitJobDone(t, db, started.JobID); job.Status != "failed" {
		t.Fatalf("bad settings: %s", job.Status)
	}
	raw, _ = os.ReadFile(envPath)
	if !strings.Contains(string(raw), "DNS_DOMAIN=ns9.example.com") {
		t.Fatalf("not rolled back:\n%s", raw)
	}
	if _, err := os.Stat(envPath + ".prev"); !os.IsNotExist(err) {
		t.Fatal(".env.prev left")
	}
	if len(srv.pluginSettingsHistory(t.Context(), "dns")) != 2 {
		t.Fatal("failed settings in history")
	}
	// Новая версия плагина с обязательной настройкой — «нужна настройка».
	pluginsMu.Lock()
	list = srv.plugins(t.Context())
	for i := range list {
		if list[i].Name == "dns" {
			list[i].SettingKeys = []string{"DNS_DOMAIN"}
		}
	}
	_ = srv.savePlugins(t.Context(), list)
	pluginsMu.Unlock()
	if rec := do("GET", "/api/hub/plugins", nil, session); !strings.Contains(rec.Body.String(), `"needs_settings":true`) {
		t.Fatalf("no needs_settings: %s", rec.Body)
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
