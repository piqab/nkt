package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

type f2bCall func(method, url string, body any, remote string) (int, map[string]any)

// f2bServer — сервер на копии фикстур: запись в /etc/fail2ban идёт в
// копию, а не в репозиторий.
func f2bServer(t *testing.T) (*Server, string, f2bCall) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "fixtures", "host"))); err != nil {
		t.Fatal(err)
	}
	c := collect.NewFixtures(root)
	cfg := &config.Config{Mode: config.ModeFixtures, DataDir: t.TempDir(), Fail2banRoot: "/etc/fail2ban", CommandTimeout: 5 * time.Second}
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, c, db)
	svc := control.NewServiceManager(cfg, c, db)
	s := &Server{cfg: cfg, db: db, scanner: scanner, services: svc, configs: control.NewConfigManager(cfg, c, db, scanner, svc)}
	s.configs.AttachDocs(f2bTemplatePrefix, &f2bTemplateDocs{s})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), store.User{Username: "admin", Role: "admin"})))
		})
	})
	r.Get("/fail2ban", s.handleF2BStatus)
	r.Get("/fail2ban/log", s.handleF2BLog)
	r.Get("/fail2ban/templates", s.handleF2BTemplates)
	r.Post("/fail2ban/ban", s.handleF2BBan)
	r.Post("/fail2ban/unban", s.handleF2BUnban)
	r.Post("/fail2ban/setup", s.handleF2BSetup)
	r.Put("/fail2ban/hub-addr", s.handleF2BHubAddr)
	r.Put("/fail2ban/templates", s.handleF2BTemplateSave)
	r.Delete("/fail2ban/templates/{name}", s.handleF2BTemplateDelete)
	r.Post("/fail2ban/templates/apply", s.handleF2BTemplateApply)
	r.Post("/fail2ban/regex-test", s.handleF2BRegexTest)
	r.Get("/configs/versions", s.handleConfigVersions)
	r.Post("/configs/versions/{id}/rollback", s.handleConfigRollback)
	call := func(method, url string, body any, remote string) (int, map[string]any) {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, url, &buf)
		if remote != "" {
			req.RemoteAddr = remote
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	return s, root, call
}

func TestF2BStatusAndBans(t *testing.T) {
	_, _, call := f2bServer(t)
	code, out := call("GET", "/fail2ban", nil, "203.0.113.5:4000")
	st, _ := out["state"].(map[string]any)
	if code != 200 || st["running"] != true || st["banned_now"].(float64) != 5 || out["client_ip"] != "203.0.113.5" || out["manual_ready"] != true {
		t.Fatalf("status: %d %v", code, out)
	}
	// Свой адрес и loopback не банятся.
	if code, out := call("POST", "/fail2ban/ban", map[string]any{"ips": []string{"203.0.113.5"}}, "203.0.113.5:4000"); code != 400 || !strings.Contains(out["error"].(string), "203.0.113.5") {
		t.Fatalf("self ban: %d %v", code, out)
	}
	if code, _ := call("POST", "/fail2ban/ban", map[string]any{"ips": []string{"127.0.0.1"}}, ""); code != 400 {
		t.Fatalf("loopback ban: %d", code)
	}
	// Мусор вместо адреса и чужой джейл — отказ до всякой команды.
	if code, _ := call("POST", "/fail2ban/ban", map[string]any{"ips": []string{"1.2.3.4; reboot"}}, ""); code != 400 {
		t.Fatalf("bad ip: %d", code)
	}
	if code, _ := call("POST", "/fail2ban/ban", map[string]any{"jail": "nope", "ips": []string{"198.51.100.9"}}, ""); code != 400 {
		t.Fatalf("unknown jail: %d", code)
	}
	if code, out := call("POST", "/fail2ban/ban", map[string]any{"ips": []string{"198.51.100.9"}, "ban_time": 3600}, ""); code != 200 || out["jail"] != "nkt-manual" {
		t.Fatalf("manual ban: %d %v", code, out)
	}
	if code, out := call("POST", "/fail2ban/unban", map[string]any{"jail": "sshd", "ips": []string{"203.0.113.45"}}, ""); code != 200 {
		t.Fatalf("unban: %d %v", code, out)
	}
	if code, _ := call("POST", "/fail2ban/unban", map[string]any{"all": true}, ""); code != 200 {
		t.Fatalf("unban all: %d", code)
	}
	code, out = call("GET", "/fail2ban/log?days=7&q=203.0.113.45&action=Ban", nil, "")
	if code != 200 || out["total"].(float64) == 0 {
		t.Fatalf("log: %d %v", code, out)
	}
}

// Адрес хаба: файл защиты с прежним ignoreip и адресом хаба; бан хаба —
// отказ.
func TestF2BHubAddr(t *testing.T) {
	_, root, call := f2bServer(t)
	if code, out := call("PUT", "/fail2ban/hub-addr", map[string]any{"addr": "198.51.100.7"}, ""); code != 200 || out["changed"] != true {
		t.Fatalf("hub addr: %d %v", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(root, "etc", "fail2ban", "jail.d", "zz-nkt-hub.local"))
	if err != nil || !strings.Contains(string(raw), "ignoreip = 127.0.0.1/8 ::1 10.0.0.0/8 198.51.100.7") {
		t.Fatalf("hub file: %v %s", err, raw)
	}
	// Второй раз то же — без записи.
	if code, out := call("PUT", "/fail2ban/hub-addr", map[string]any{"addr": "198.51.100.7"}, ""); code != 200 || out["changed"] != false {
		t.Fatalf("hub addr again: %d %v", code, out)
	}
	if code, out := call("POST", "/fail2ban/ban", map[string]any{"ips": []string{"198.51.100.7"}}, ""); code != 400 || !strings.Contains(out["error"].(string), "198.51.100.7") {
		t.Fatalf("hub ban: %d %v", code, out)
	}
	if code, _ := call("PUT", "/fail2ban/hub-addr", map[string]any{"addr": "not-an-ip"}, ""); code != 400 {
		t.Fatalf("bad hub addr: %d", code)
	}
}

// Свои шаблоны: сохранение с историей, применение с фильтром (dry-run и
// запись), удаление версией и откат.
func TestF2BTemplates(t *testing.T) {
	_, root, call := f2bServer(t)
	code, out := call("GET", "/fail2ban/templates", nil, "")
	if code != 200 || len(out["builtin"].([]any)) < 5 {
		t.Fatalf("templates: %d %v", code, out)
	}
	tpl := map[string]any{"name": "myapp", "description": "вход в myapp", "jail": "[myapp]\nenabled = true\nlogpath = /var/log/nginx/error.log\n", "filter": "[Definition]\nfailregex = ^login failed from <HOST>$\n"}
	if code, out := call("PUT", "/fail2ban/templates", tpl, ""); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	if code, _ := call("PUT", "/fail2ban/templates", map[string]any{"name": "Bad Name", "jail": "[x]\n"}, ""); code != 400 {
		t.Fatalf("bad name accepted: %d", code)
	}
	tpl["dry_run"] = true
	code, out = call("POST", "/fail2ban/templates/apply", tpl, "")
	files, _ := out["files"].([]any)
	if code != 200 || len(files) != 2 {
		t.Fatalf("dry run: %d %v", code, out)
	}
	jail := files[1].(map[string]any)
	if !strings.Contains(jail["after"].(string), "filter = nkt-myapp") || jail["exists"] != false {
		t.Fatalf("jail file: %v", jail)
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "fail2ban", "jail.d", "nkt-myapp.local")); err == nil {
		t.Fatal("dry run wrote a file")
	}
	tpl["dry_run"] = false
	if code, out := call("POST", "/fail2ban/templates/apply", tpl, ""); code != 200 {
		t.Fatalf("apply: %d %v", code, out)
	}
	for _, rel := range []string{"jail.d/nkt-myapp.local", "filter.d/nkt-myapp.conf"} {
		if _, err := os.Stat(filepath.Join(root, "etc", "fail2ban", rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	tpl["description"] = "правка"
	if code, _ := call("PUT", "/fail2ban/templates", tpl, ""); code != 200 {
		t.Fatal("second save")
	}
	if code, _ := call("DELETE", "/fail2ban/templates/myapp", nil, ""); code != 200 {
		t.Fatal("delete")
	}
	code, out = call("GET", "/configs/versions?path="+f2bTemplatePrefix+"myapp", nil, "")
	versions, _ := out["versions"].([]any)
	if code != 200 || len(versions) < 3 {
		t.Fatalf("versions: %d %v", code, out)
	}
	// Откат к версии «правка» возвращает удалённый шаблон.
	var id int64
	for _, v := range versions {
		m := v.(map[string]any)
		if strings.Contains(m["note"].(string), "удал") || strings.Contains(m["note"].(string), "deleted") {
			continue
		}
		id = int64(m["id"].(float64))
		break
	}
	if code, out := call("POST", "/configs/versions/"+strconv.FormatInt(id, 10)+"/rollback", map[string]any{}, ""); code != 200 {
		t.Fatalf("rollback: %d %v", code, out)
	}
	_, out = call("GET", "/fail2ban/templates", nil, "")
	custom, _ := out["custom"].([]any)
	if len(custom) != 1 || custom[0].(map[string]any)["description"] != "правка" {
		t.Fatalf("after rollback: %v", custom)
	}
	if code, out := call("POST", "/fail2ban/regex-test", map[string]any{"filter": "^x <HOST>$", "log": "/var/log/auth.log"}, ""); code != 200 || out["matched"].(float64) != 37 {
		t.Fatalf("regex test: %d %v", code, out)
	}
	if code, _ := call("POST", "/fail2ban/regex-test", map[string]any{"filter": "x", "log": "/etc/shadow"}, ""); code != 400 {
		t.Fatalf("regex test outside /var/log: %d", code)
	}
}
