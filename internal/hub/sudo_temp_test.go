package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// Сценарии выдачи и возврата полного sudo — на временном каталоге вместо
// /etc/sudoers.d и с заглушкой visudo: прежнее правило, его отсутствие и
// недоведённый прошлый запуск возвращаются ровно такими, какими были.
func TestTempSudoScripts(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o755)
	if err := os.WriteFile(filepath.Join(bin, "visudo"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "nkt-hub")
	local := func(script string) string { return strings.ReplaceAll(script, sudoersDropIn, f) }
	run := func(script string) {
		t.Helper()
		cmd := exec.Command("sh", "-c", local(script))
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	read := func() string { b, _ := os.ReadFile(f); return string(b) }
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	// Узкое правило было — вернулось оно.
	narrow := "antex ALL=(root) NOPASSWD: /usr/local/bin/nkt hub-sudo\n"
	_ = os.WriteFile(f, []byte(narrow), 0o440)
	run(grantTempSudoScript("antex"))
	if got := read(); got != "antex ALL=(ALL) NOPASSWD: ALL\n" {
		t.Fatalf("full rule: %q", got)
	}
	// Повторная выдача (прошлый запуск не вернул) не затирает запомненное.
	run(grantTempSudoScript("antex"))
	run(restoreSudoScript)
	if got := read(); got != narrow {
		t.Fatalf("restored: %q", got)
	}
	if exists(f+".nkt-bak") || exists(f+".nkt-none") {
		t.Fatal("leftovers after restore")
	}

	// Правила не было — после возврата его снова нет.
	_ = os.Remove(f)
	run(grantTempSudoScript("antex"))
	if !exists(f) {
		t.Fatal("no full rule")
	}
	run(restoreSudoScript)
	if exists(f) || exists(f+".nkt-none") {
		t.Fatal("rule or marker left")
	}
	// Возвращать нечего — ничего и не делается.
	run(restoreSudoScript)
}

func TestFullSudoCommand(t *testing.T) {
	cmd := fullSudoCommand("antex")
	if !strings.Contains(cmd, `"antex ALL=(ALL) NOPASSWD: ALL"`) || !strings.Contains(cmd, "mktemp") || strings.Contains(cmd, "/tmp/") {
		t.Fatalf("command: %s", cmd)
	}
	if out, err := exec.Command("sh", "-n", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("syntax: %v %s", err, out)
	}
	if fullSudoCommand("a'b") != "" || fullSudoCommand("x;rm") != "" {
		t.Fatal("bad user accepted")
	}
}

func TestOneTimeSecrets(t *testing.T) {
	tok := oneTime.Put([]byte("s3cret"))
	v, ok := oneTime.Take(tok)
	if !ok || string(v) != "s3cret" {
		t.Fatalf("take: %q %v", v, ok)
	}
	if _, ok := oneTime.Take(tok); ok {
		t.Fatal("second take succeeded")
	}
}

// Узкий sudo без пароля в запросе — 409 с командой; пароль не попадает ни
// в ответ, ни в параметры задания.
func TestEdgeInstallNeedsFullSudo(t *testing.T) {
	m, db := newTestManager(t)
	s := &Server{hub: m, db: db}
	ctx := context.Background()
	// Порт 1 на loopback — живая проверка сразу получит отказ и оставит
	// записанное состояние.
	h, err := db.CreateHost(ctx, "fly-vps", "127.0.0.1", 1, "antex", store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetHostSudoStatus(ctx, h, store.SudoStatusNarrow); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"host_id": h, "domain": "", "roles": []string{EdgeRoleFly}})
	rec := httptest.NewRecorder()
	s.handleEdgeInstall(rec, httptest.NewRequest(http.MethodPost, "/hub/edges/install", bytes.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	var res struct {
		SudoNeeded  bool   `json:"sudo_needed"`
		SudoUser    string `json:"sudo_user"`
		SudoCommand string `json:"sudo_command"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.SudoNeeded || res.SudoUser != "antex" || !strings.Contains(res.SudoCommand, "antex ALL=(ALL) NOPASSWD: ALL") {
		t.Fatalf("response: %s", rec.Body)
	}
}
