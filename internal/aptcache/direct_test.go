package aptcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Поддельный apt-get: через кэш — 502, мимо кэша — успех. Сценарий с
// nkt_apt должен повторить напрямую и завершиться успехом; ошибка не про
// кэш — не повторяться.
func TestWrapScriptRetriesDirect(t *testing.T) {
	dir := t.TempDir()
	fake := `#!/bin/sh
case "$*" in
  *Proxy=DIRECT*) echo "direct ok $*"; exit 0;;
  *broken*) echo "E: Unable to locate package broken"; exit 100;;
  *) echo "Err:1 http://mirror.example/debian trixie/main x 502 Bad Gateway [IP: 127.0.0.1 3142]"; exit 100;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "apt-get"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(script string) (string, error) {
		cmd := exec.Command("sh", "-c", WrapScript(script))
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("DEBIAN_FRONTEND=noninteractive apt-get install -y wireguard-tools && echo done")
	if err != nil || !strings.Contains(out, "retrying directly") || !strings.Contains(out, "direct ok") || !strings.Contains(out, "done") {
		t.Fatalf("retry: %v\n%s", err, out)
	}
	out, err = run("apt-get install -y broken")
	if err == nil || strings.Contains(out, "retrying") {
		t.Fatalf("non-proxy failure retried: %v\n%s", err, out)
	}
	if WrapScript("echo hi") != "echo hi" || !ProxyFailed("Err:1 ... 502 Bad Gateway [IP: 127.0.0.1 3142]") || ProxyFailed("E: Unable to locate package x") {
		t.Fatal("helpers")
	}
	if got := Direct("sudo -n env X=1 apt-get install -y a"); !strings.Contains(got, "apt-get "+DirectOpts+" install") {
		t.Fatal(got)
	}
}
