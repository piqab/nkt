package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/plugin"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Хост плагина целиком: «Проверить» и добавление (отпечаток запоминается,
// пароль — только в хабе и не наружу), список хостов, разделы, действия и
// команды с правами, сценарий «run» (проверка, сухой прогон, выполнение),
// опрос (доступность, находки, «Мониторинг»), версии конфигурации,
// агентские пути отказывают, экспорт и импорт с перешифровкой учёток.
func TestPluginHosts(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	srv.jobs.Register(KindScriptRun, srv.ScriptRunner())
	h := srv.Handler()
	ctx := context.Background()
	const secret = "plugin-secret-0123456789"
	const devPass = "device-password-777"
	var mu sync.Mutex
	var calls []plugin.Request
	cfgText := "/ip address add address=192.168.88.1/24\n"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !plugin.Verify(secret, r, body, time.Now()) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == plugin.PathManifest {
			_ = json.NewEncoder(w).Encode(plugin.Manifest{Protocol: 1, Name: "mt", Title: "MT", Version: "1", Provides: []string{"hosts"},
				HostTypes: []plugin.HostType{{ID: "routeros", Title: "RouterOS",
					Fields: []plugin.Field{{Name: "address", Title: "Address", Required: true}, {Name: "user", Title: "User"},
						{Name: "password", Title: "Password", Type: "password", Required: true}, {Name: "fingerprint", Title: "FP"}},
					Sections: []string{"overview", "metrics", "findings", "config", "actions", "exec"},
					Actions:  []plugin.Action{{Title: "Reboot", Op: "reboot"}}, Exec: "routeros"}}})
			return
		}
		var req plugin.Request
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		calls = append(calls, req)
		mu.Unlock()
		conn, _ := req.Args["conn"].(map[string]any)
		resp := plugin.Response{OK: true}
		if conn["password"] != devPass {
			resp = plugin.Response{OK: false, Error: "wrong login or password"}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		set := func(v any) { resp.Result, _ = json.Marshal(v) }
		switch req.Op {
		case plugin.OpHostCheck:
			set(plugin.HostInfo{Name: "Router-Office", OS: "RouterOS", Version: "7.15", Fingerprint: "ab12"})
		case plugin.OpHostSnapshot:
			set(plugin.HostSnapshot{Info: plugin.HostInfo{Name: "Router-Office"}, Neighbors: []plugin.HostNeighbor{{Name: "sw1", Address: "192.168.88.2"}}})
		case plugin.OpHostMetrics:
			cpu, used, total := 12.5, int64(100), int64(400)
			set(plugin.HostMetrics{CPUPct: &cpu, MemUsed: &used, MemTotal: &total, UptimeS: 100, Net: map[string]plugin.HostNetCounters{"ether1": {RX: 1000, TX: 2000}}})
		case plugin.OpHostFindings:
			set(map[string]any{"findings": []plugin.HostFinding{{ID: "telnet", Severity: "critical", Title: "telnet on"}, {ID: "upd", Severity: "warning", Title: "update"}}})
		case plugin.OpHostConfig:
			mu.Lock()
			set(map[string]string{"text": cfgText})
			mu.Unlock()
		case plugin.OpHostAction:
			resp.Text = fmt.Sprintf("did %v dry=%v", req.Args["action"], req.DryRun)
		case plugin.OpHostExec:
			if req.Args["check"] == true {
				resp.Text = "checked"
				if strings.Contains(fmt.Sprint(req.Args["command"]), "BAD") {
					resp = plugin.Response{OK: false, Error: "syntax error"}
				}
			} else {
				resp.Text = fmt.Sprintf("ran %v dry=%v", req.Args["command"], req.DryRun)
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer fake.Close()
	u, _ := url.Parse(fake.URL)
	port, _ := strconv.Atoi(u.Port())
	enc, _ := secretbox.Encrypt(srv.hub.key, []byte(secret))
	_ = srv.savePlugins(ctx, []pluginConf{{Name: "mt", Host: localHostName, Port: port, SecretEnc: enc}})
	forgetManifests("mt")

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
	expect := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: want %d, got %d %s", what, code, rec.Code, rec.Body)
		}
	}
	rec := do("POST", "/api/auth/login", `{"username":"admin","password":"admin-password-1234"}`, nil)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	session := func(r *http.Request) { r.AddCookie(cookie) }

	rec = do("GET", "/api/hub/plugin-hosts/types", nil, session)
	if !strings.Contains(rec.Body.String(), `"id":"routeros"`) {
		t.Fatalf("types: %s", rec.Body)
	}
	add := map[string]any{"plugin": "mt", "type": "routeros", "group": "office", "conn": map[string]any{"address": "192.168.88.1", "user": "nkt", "password": devPass}}
	bad := map[string]any{"plugin": "mt", "type": "routeros", "conn": map[string]any{"address": "192.168.88.1", "password": "x"}}
	expect(do("POST", "/api/hub/plugin-hosts/check", bad, session), 400, "wrong password")
	rec = do("POST", "/api/hub/plugin-hosts/check", add, session)
	expect(rec, 200, "check")
	if !strings.Contains(rec.Body.String(), `"fingerprint":"ab12"`) {
		t.Fatalf("check: %s", rec.Body)
	}
	rec = do("POST", "/api/hub/plugin-hosts", add, session)
	expect(rec, 200, "add")
	var added struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	if added.Name != "router-office" {
		t.Fatalf("name from the device: %s", rec.Body)
	}
	expect(do("POST", "/api/hub/plugin-hosts", add, session), 409, "same name")
	hostPath := "/api/hub/plugin-hosts/" + strconv.FormatInt(added.ID, 10)
	hst, _ := db.HostByID(ctx, added.ID)
	raw, _ := secretbox.Decrypt(srv.hub.key, hst.ConnEnc)
	if !hst.IsPlugin() || !strings.Contains(string(raw), `"fingerprint":"ab12"`) || !strings.Contains(string(raw), devPass) {
		t.Fatalf("stored: %+v %s", hst, raw)
	}
	// Агентские пути и списки хост не видят.
	if list, _ := db.ListHosts(ctx); len(list) != 0 {
		t.Fatalf("ListHosts sees a plugin host: %+v", list)
	}
	if _, _, _, err := srv.hub.dialerFor(ctx, added.ID); err == nil || !strings.Contains(err.Error(), "router-office") {
		t.Fatalf("dialer: %v", err)
	}
	expect(do("GET", "/api/hosts/"+strconv.FormatInt(added.ID, 10)+"/overview", nil, session), 404, "agent proxy")

	// Карточка: пароль — только «задан».
	rec = do("GET", hostPath, nil, session)
	expect(rec, 200, "host")
	if b := rec.Body.String(); strings.Contains(b, devPass) || !strings.Contains(b, `"password":true`) || !strings.Contains(b, `"user":"nkt"`) {
		t.Fatalf("host card: %s", b)
	}
	// Опрос: доступен, находки, метрики в «Мониторинг».
	srv.pollPluginHostNow(ctx, added.ID, true)
	srv.pollPluginHostNow(ctx, added.ID, false)
	rec = do("GET", "/api/hub/hosts", nil, session)
	if b := rec.Body.String(); !strings.Contains(b, `"kind":"plugin"`) || !strings.Contains(b, `"reachable":true`) || !strings.Contains(b, `"critical":1`) {
		t.Fatalf("host list: %s", b)
	}
	if rows, _ := db.MonSeries(ctx, store.MonSeriesQuery{HostID: added.ID, Source: "host", Metric: "cpu_pct", From: time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02T15"), To: time.Now().UTC().Add(2 * time.Hour).Format("2006-01-02T15")}); len(rows) == 0 {
		t.Fatal("no monitoring rows")
	}
	// Разделы.
	if b := do("GET", hostPath+"/snapshot", nil, session).Body.String(); !strings.Contains(b, `"sw1"`) {
		t.Fatalf("snapshot: %s", b)
	}
	if b := do("GET", hostPath+"/findings", nil, session).Body.String(); !strings.Contains(b, `"telnet on"`) {
		t.Fatalf("findings: %s", b)
	}
	// Конфигурация: версия при опросе; изменилась на устройстве — новая.
	mu.Lock()
	cfgText = "/ip address add address=192.168.88.1/24\n/ip dns set servers=1.1.1.1\n"
	mu.Unlock()
	rec = do("GET", hostPath+"/config?fresh=1", nil, session)
	var cfg struct {
		Versions []pluginHostConfigVersion `json:"versions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cfg)
	if len(cfg.Versions) != 2 || !strings.Contains(cfg.Versions[0].Text, "dns set") {
		t.Fatalf("config versions: %s", rec.Body)
	}
	_ = do("GET", hostPath+"/config?fresh=1", nil, session)
	if len(srv.pluginHostConfigs(ctx, added.ID)) != 2 {
		t.Fatal("same config made a new version")
	}
	// Действия и команды: администратор — да; токен без «плагин mt» — нет.
	if b := do("POST", hostPath+"/action", map[string]any{"action": "reboot", "dry_run": true}, session).Body.String(); !strings.Contains(b, "did reboot dry=true") {
		t.Fatalf("action: %s", b)
	}
	expect(do("POST", hostPath+"/action", map[string]any{"action": "format"}, session), 400, "unknown action")
	if b := do("POST", hostPath+"/exec", map[string]any{"command": "/ip dns cache flush"}, session).Body.String(); !strings.Contains(b, "ran /ip dns cache flush dry=false") {
		t.Fatalf("exec: %s", b)
	}
	rec = do("POST", "/api/hub/tokens", `{"name":"ro","role":"read"}`, session)
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token) }
	expect(do("GET", hostPath+"/findings", nil, bearer), 200, "token reads")
	expect(do("POST", hostPath+"/exec", map[string]any{"command": "/system reboot"}, bearer), 403, "read token exec")

	// Сценарий: «run» на хосте плагина; «Проверить» спрашивает плагин.
	check := func(content string) string {
		rec := do("POST", "/api/hub/scripts/check", map[string]string{"content": content}, session)
		return rec.Body.String()
	}
	if b := check("on router-office run \"/ip dns cache flush\"\n"); !strings.Contains(b, `"issues":[]`) {
		t.Fatalf("check ok script: %s", b)
	}
	if b := check("on router-office run \"BAD\"\n"); !strings.Contains(b, "syntax error") {
		t.Fatalf("check bad command: %s", b)
	}
	if b := check("on router-office service nginx restart\n"); !strings.Contains(b, "only “run”") && !strings.Contains(b, "только «run»") {
		t.Fatalf("service on a plugin host: %s", b)
	}
	rec = do("POST", "/api/hub/scripts", map[string]string{"name": "flush", "content": "on router-office run\n/ip dns cache flush\n:put done\nend\n"}, session)
	var sc struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sc)
	for _, dry := range []bool{true, false} {
		rec = do("POST", "/api/hub/scripts/"+strconv.FormatInt(sc.ID, 10)+"/run", map[string]any{"dry_run": dry}, session)
		if rec.Code != 200 {
			t.Fatalf("run: %d %s (create: %d)", rec.Code, rec.Body, sc.ID)
		}
		var started struct {
			JobID int64 `json:"job_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &started)
		job := waitJobDone(t, db, started.JobID)
		lines, _ := db.JobLog(ctx, job.ID, 0, 100)
		logText := fmt.Sprint(lines)
		if job.Status != "succeeded" || !strings.Contains(logText, fmt.Sprintf("dry=%v", dry)) {
			t.Fatalf("script run dry=%v: %s %s %s", dry, job.Status, job.Error, logText)
		}
	}
	// Пароль устройства нигде не светится: ни в журнале действий, ни в
	// списке хостов.
	if b := do("GET", "/api/hub/hosts", nil, session).Body.String(); strings.Contains(b, devPass) {
		t.Fatal("password in the host list")
	}
	entries, _ := db.ListAudit(ctx, store.AuditFilter{Limit: 200})
	for _, e := range entries {
		if b, _ := json.Marshal(e); strings.Contains(string(b), devPass) {
			t.Fatalf("password in the audit: %s", b)
		}
	}
	// Смена учёток: пустой пароль — прежний.
	expect(do("PUT", hostPath+"/conn", map[string]any{"conn": map[string]any{"address": "192.168.88.1", "user": "nkt2"}}, session), 200, "conn edit")
	hst, _ = db.HostByID(ctx, added.ID)
	raw, _ = secretbox.Decrypt(srv.hub.key, hst.ConnEnc)
	if !strings.Contains(string(raw), `"user":"nkt2"`) || !strings.Contains(string(raw), devPass) {
		t.Fatalf("conn after edit: %s", raw)
	}
	// Экспорт с ключом и импорт в другой хаб: учётки перешифрованы.
	export, err := srv.hub.ExportHub(ctx, true, false)
	if err != nil {
		t.Fatal(err)
	}
	m2, db2 := newTestManager(t)
	m2.cfg.DataDir = t.TempDir()
	rawExp, _ := json.Marshal(export)
	decoded, _ := store.DecodeHubExport(rawExp)
	rep := m2.ImportHosts(ctx, decoded, nil)
	if len(rep.Errors) != 0 {
		t.Fatalf("import: %v", rep.Errors)
	}
	ph, _ := db2.ListPluginHosts(ctx)
	if len(ph) != 1 || ph[0].Plugin != "mt" || ph[0].PluginType != "routeros" || ph[0].Group != "office" {
		t.Fatalf("imported: %+v", ph)
	}
	if raw, err := secretbox.Decrypt(m2.key, ph[0].ConnEnc); err != nil || !strings.Contains(string(raw), devPass) {
		t.Fatalf("re-encrypted conn: %v %s", err, raw)
	}
	// Удаление: из хаба (на устройстве — ничего).
	expect(do("DELETE", hostPath, nil, session), 200, "delete")
	if all, _ := db.ListAllHosts(ctx); len(all) != 0 {
		t.Fatalf("left: %+v", all)
	}
}
