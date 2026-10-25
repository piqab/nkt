package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

// Плагин службой: файл, настройки 0600 (с 127.0.0.1 и портом), юнит с
// DynamicUser; обновление без env — настройки прежние; без python3 —
// 409; удаление — файлы прочь.
func TestPluginNativeInstall(t *testing.T) {
	root := t.TempDir()
	python, docker := true, true
	write := func() {
		cmds := []map[string]any{{"match": []string{"systemctl", "daemon-reload"}, "stdout": ""},
			{"match": []string{"rm", "-rf", "--", "/opt/nkt-plugins/dns"}, "stdout": ""}}
		if docker {
			cmds = append(cmds, map[string]any{"match": []string{"getent", "group", "docker"}, "stdout": "docker:x:999:\n"})
		}
		if python {
			cmds = append(cmds, map[string]any{"match": []string{"sh", "-c", "command -v python3"}, "stdout": "/usr/bin/python3\n"})
		}
		raw, _ := json.Marshal(map[string]any{"commands": cmds})
		_ = os.MkdirAll(filepath.Join(root, ".commands"), 0o755)
		_ = os.WriteFile(filepath.Join(root, ".commands", "index.json"), raw, 0o644)
	}
	write()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{}
	s := &Server{cfg: cfg, db: db, scanner: inventory.New(cfg, collect.NewFixtures(root), nil)}
	call := func(h http.HandlerFunc, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/api/plugins/native", strings.NewReader(string(raw))))
		return rec
	}
	req := map[string]any{"name": "dns", "run": "python3", "file": "nkt-plugin.py", "content": "print('hi')\n", "port": 9180,
		"env": map[string]string{"DNS_API_TOKEN": "tok-123", "NKT_PLUGIN_SECRET": "s3cret-s3cret-s3cret"}}
	if rec := call(s.handlePluginNativeInstall, req); rec.Code != 200 {
		t.Fatalf("install: %d %s", rec.Code, rec.Body)
	}
	env := filepath.Join(root, "etc/nkt-plugins/dns.env")
	st, err := os.Stat(env)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("env file: %v %v", st, err)
	}
	raw, _ := os.ReadFile(env)
	for _, want := range []string{"DNS_API_TOKEN=tok-123", "NKT_PLUGIN_LISTEN=127.0.0.1", "NKT_PLUGIN_PORT=9180"} {
		if !strings.Contains(string(raw), want+"\n") {
			t.Fatalf("env misses %s:\n%s", want, raw)
		}
	}
	unit, _ := os.ReadFile(filepath.Join(root, "etc/systemd/system/nkt-plugin-dns.service"))
	if !strings.Contains(string(unit), "DynamicUser=yes") || !strings.Contains(string(unit), "/opt/nkt-plugins/dns/nkt-plugin.py") {
		t.Fatalf("unit:\n%s", unit)
	}
	// Обновление без env — настройки как были.
	delete(req, "env")
	req["content"] = "print('v2')\n"
	if rec := call(s.handlePluginNativeInstall, req); rec.Code != 200 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if again, _ := os.ReadFile(env); string(again) != string(raw) {
		t.Fatal("env changed on update")
	}
	// Плохие значения настроек.
	if rec := call(s.handlePluginNativeInstall, map[string]any{"name": "dns", "run": "python3", "file": "nkt-plugin.py", "content": "x", "port": 9180,
		"env": map[string]string{"X": "a$(id)"}}); rec.Code != 400 {
		t.Fatalf("bad env accepted: %d", rec.Code)
	}
	if rec := call(s.handlePluginNativeInstall, map[string]any{"name": "../x", "run": "python3", "file": "a.py", "content": "x", "port": 9180}); rec.Code != 400 {
		t.Fatalf("bad name accepted: %d", rec.Code)
	}
	// Доступ к Docker: служба в группе docker.
	req["docker"] = true
	if rec := call(s.handlePluginNativeInstall, req); rec.Code != 200 {
		t.Fatalf("docker install: %d %s", rec.Code, rec.Body)
	}
	unit, _ = os.ReadFile(filepath.Join(root, "etc/systemd/system/nkt-plugin-dns.service"))
	if !strings.Contains(string(unit), "SupplementaryGroups=docker") {
		t.Fatalf("no docker group:\n%s", unit)
	}
	delete(req, "docker")
	// Удаление.
	if rec := call(s.handlePluginNativeRemove, map[string]string{"name": "dns"}); rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Fatal("env left")
	}
	// Нет группы docker — 409; без python3 — 409.
	docker = false
	write()
	s = &Server{cfg: cfg, db: db, scanner: inventory.New(cfg, collect.NewFixtures(root), nil)}
	req["docker"] = true
	if rec := call(s.handlePluginNativeInstall, req); rec.Code != http.StatusConflict {
		t.Fatalf("no docker: %d %s", rec.Code, rec.Body)
	}
	delete(req, "docker")
	python = false
	write()
	s = &Server{cfg: cfg, db: db, scanner: inventory.New(cfg, collect.NewFixtures(root), nil)}
	req["env"] = map[string]string{"A": "b"}
	if rec := call(s.handlePluginNativeInstall, req); rec.Code != http.StatusConflict {
		t.Fatalf("no python: %d %s", rec.Code, rec.Body)
	}
}

// «Настройки»: read отдаёт только названные несекретные значения (секреты —
// «задан», ключи хаба NKT_ — никогда); apply хранит прежний файл, rollback
// его возвращает, commit удаляет.
func TestPluginNativeSettings(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, "etc/nkt-plugins/dns.env")
	_ = os.MkdirAll(filepath.Dir(envPath), 0o755)
	_ = os.WriteFile(envPath, []byte("# nkt\nDNS_API_TOKEN=tok-1\nDNS_DOMAIN=ns1.example.com\nNKT_PLUGIN_SECRET=s3cret\nNKT_PLUGIN_PORT=9180\n"), 0o600)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{}
	s := &Server{cfg: cfg, db: db, scanner: inventory.New(cfg, collect.NewFixtures(root), nil)}
	call := func(body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		s.handlePluginNativeSettings(rec, httptest.NewRequest(http.MethodPost, "/api/plugins/native/settings", strings.NewReader(string(raw))))
		return rec
	}
	rec := call(map[string]any{"name": "dns", "op": "read", "keys": []string{"DNS_DOMAIN", "NKT_PLUGIN_SECRET"}, "secrets": []string{"DNS_API_TOKEN"}})
	if b := rec.Body.String(); rec.Code != 200 || !strings.Contains(b, `"DNS_DOMAIN":"ns1.example.com"`) || !strings.Contains(b, `"DNS_API_TOKEN":true`) ||
		strings.Contains(b, "tok-1") || strings.Contains(b, "s3cret") {
		t.Fatalf("read: %d %s", rec.Code, b)
	}
	for _, bad := range []map[string]string{{"NKT_PLUGIN_PORT": "1"}, {"DNS_DOMAIN": "a$b"}, {"bad-key": "x"}} {
		if rec := call(map[string]any{"name": "dns", "op": "apply", "set": bad}); rec.Code != 400 {
			t.Fatalf("apply %v: %d", bad, rec.Code)
		}
	}
	if rec := call(map[string]any{"name": "dns", "op": "apply", "set": map[string]string{"DNS_DOMAIN": "ns2.example.com", "DNS_API_TOKEN": ""}}); rec.Code != 200 {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	raw, _ := os.ReadFile(envPath)
	if !strings.Contains(string(raw), "DNS_DOMAIN=ns2.example.com") || strings.Contains(string(raw), "DNS_API_TOKEN") || !strings.Contains(string(raw), "NKT_PLUGIN_SECRET=s3cret") {
		t.Fatalf("after apply:\n%s", raw)
	}
	if st, err := os.Stat(envPath + ".prev"); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("prev: %v %v", st, err)
	}
	if rec := call(map[string]any{"name": "dns", "op": "rollback"}); rec.Code != 200 {
		t.Fatalf("rollback: %d %s", rec.Code, rec.Body)
	}
	raw, _ = os.ReadFile(envPath)
	if !strings.Contains(string(raw), "DNS_DOMAIN=ns1.example.com") || !strings.Contains(string(raw), "DNS_API_TOKEN=tok-1") {
		t.Fatalf("after rollback:\n%s", raw)
	}
	if _, err := os.Stat(envPath + ".prev"); !os.IsNotExist(err) {
		t.Fatal("prev left after rollback")
	}
	if rec := call(map[string]any{"name": "dns", "op": "rollback"}); rec.Code != 400 {
		t.Fatalf("second rollback: %d", rec.Code)
	}
	_ = call(map[string]any{"name": "dns", "op": "apply", "set": map[string]string{"DNS_DOMAIN": "ns3.example.com"}})
	if rec := call(map[string]any{"name": "dns", "op": "commit"}); rec.Code != 200 {
		t.Fatalf("commit: %d", rec.Code)
	}
	if _, err := os.Stat(envPath + ".prev"); !os.IsNotExist(err) {
		t.Fatal("prev left after commit")
	}
}
