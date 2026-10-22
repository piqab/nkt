package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/svcdesc"
)

const testDesc = `name: myapp
title: My app
sources:
  - id: cfg
    file: /etc/myapp/app.yml
extract:
  endpoints:
    - each: "$.listen[*]"
      fields:
        port: "@.port"
`

// Описания сервисов на хабе: библиотека с историей, проверка, вычисление
// пути над образцом, доставка на машину хаба, журнал разбора на хосте,
// экспорт и импорт.
func TestSvcDescHub(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	srv.jobs.Register(KindSvcDescDeliver, NewSvcDescDeliverRunner(srv))
	h := srv.Handler()
	ctx := context.Background()
	do := func(method, path, body string, cookie *http.Cookie) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out == nil {
			out = map[string]any{"raw": rec.Body.String()}
		}
		return rec.Code, out
	}
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"admin-password-1234"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	body := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	// Проверка: ошибка — текстом, хорошее — сводкой.
	if code, out := do("POST", "/api/hub/svcdescs/check", body(map[string]string{"content": "name: nginx\nsources: []\n"}), cookie); code != 200 || out["error"] == "" {
		t.Fatalf("check bad: %d %v", code, out)
	}
	code, out := do("POST", "/api/hub/svcdescs/check", body(map[string]string{"content": testDesc}), cookie)
	if code != 200 || out["name"] != "myapp" || out["endpoints"].(float64) != 1 {
		t.Fatalf("check: %d %v", code, out)
	}
	// Создать, второй с тем же именем — конфликт, правка — история.
	code, out = do("POST", "/api/hub/svcdescs", body(map[string]string{"content": testDesc}), cookie)
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	id := int64(out["id"].(float64))
	if code, _ := do("POST", "/api/hub/svcdescs", body(map[string]string{"content": testDesc}), cookie); code != http.StatusConflict {
		t.Fatalf("duplicate: %d", code)
	}
	if code, out := do("POST", "/api/hub/svcdescs", body(map[string]string{"content": "name: x\nsources: [\n"}), cookie); code != 400 {
		t.Fatalf("broken saved: %d %v", code, out)
	}
	if code, _ := do("PUT", "/api/hub/svcdescs/"+strconv.FormatInt(id, 10), body(map[string]string{"content": strings.Replace(testDesc, "My app", "My app 2", 1), "note": "title"}), cookie); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if versions, _ := db.SvcDescVersions(ctx, id, 10); len(versions) != 2 || versions[0].Note != "title" {
		t.Fatalf("versions: %+v", versions)
	}
	_, out = do("GET", "/api/hub/svcdescs", "", cookie)
	if own := out["own"].([]any); len(own) != 1 {
		t.Fatalf("list own: %v", out)
	}
	if builtin := out["builtin"].([]any); len(builtin) != 16 {
		t.Fatalf("list builtin: %d", len(builtin))
	}

	// Конструктор: each и поле над деревом.
	_, out = do("POST", "/api/hub/svcdescs/eval", body(map[string]any{
		"tree": map[string]any{"listen": []any{map[string]any{"addr": ":80"}, map[string]any{"addr": "127.0.0.1:8443"}}},
		"each": "$.listen[*]", "expr": "@.addr | port", "when": "@.addr != ':80'"}), cookie)
	items := out["items"].([]any)
	if len(items) != 2 || items[1].(map[string]any)["values"].([]any)[0].(float64) != 8443 || items[0].(map[string]any)["when"] != false {
		t.Fatalf("eval: %v", out)
	}

	// Доставка на машину хаба: файл уровня «профиль».
	code, out = do("POST", "/api/hub/svcdescs/deliver", body(map[string]any{"host_ids": []int64{-1}}), cookie)
	if code != 200 {
		t.Fatalf("deliver: %d %v", code, out)
	}
	jobID := int64(out["job_id"].(float64))
	deadline := time.Now().Add(20 * time.Second)
	var j store.Job
	for time.Now().Before(deadline) {
		if j, _ = db.JobByID(ctx, jobID); j.Status == store.JobSucceeded || j.Status == store.JobFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if j.Status != store.JobSucceeded {
		t.Fatalf("deliver job: %s\n%s", j.Status, jobLogText(t, ctx, db, jobID))
	}
	data, err := os.ReadFile(filepath.Join(svcdesc.ProfileDir(srv.cfg.DataDir), "myapp.yaml"))
	if err != nil || !strings.Contains(string(data), "My app 2") {
		t.Fatalf("delivered file: %v %q", err, data)
	}
	// Список описаний на хосте видит его как «профиль».
	_, out = do("GET", "/api/hosts/local/svcdesc", "", cookie)
	if names := out["profile"].([]any); len(names) != 1 || names[0] != "myapp" {
		t.Fatalf("host list: %v", out)
	}
	// Журнал разбора на хосте: файла нет — «не найден».
	_, out = do("POST", "/api/hosts/local/svcdesc/test", body(map[string]string{"content": testDesc}), cookie)
	if log := out["log"].([]any); len(log) == 0 || !strings.Contains(log[len(log)-1].(string), "myapp") {
		t.Fatalf("test: %v", out)
	}
	// Образец: закрытый путь — отказ.
	if code, _ := do("POST", "/api/hosts/local/svcdesc/sample", body(map[string]any{"source": map[string]string{"file": "/etc/shadow", "format": "text"}}), cookie); code != 400 {
		t.Fatalf("sample shadow: %d", code)
	}
	if code, out := do("POST", "/api/hosts/local/svcdesc/sample", body(map[string]any{"source": map[string]string{"file": "/etc/hostname", "format": "text"}}), cookie); code != 200 || len(out["docs"].([]any)) != 1 {
		t.Fatalf("sample: %d %v", code, out)
	}

	// Экспорт хаба — с историей; импорт на чистый хаб.
	exp, err := db.ExportHosts(ctx)
	if err != nil || len(exp.SvcDescs) != 1 || len(exp.SvcDescs[0].Versions) != 2 {
		t.Fatalf("export: %v %+v", err, exp.SvcDescs)
	}
	db2, err := store.Open(filepath.Join(t.TempDir(), "hub2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	rep := db2.ImportHosts(ctx, exp, store.ImportResolutions{})
	if rep.Count(store.SectionSvcDescs).Added != 1 {
		t.Fatalf("import: %+v", rep)
	}
	if v, _ := db2.SvcDescByName(ctx, "myapp"); !strings.Contains(v.Content, "My app 2") {
		t.Fatalf("imported: %+v", v)
	}
}
