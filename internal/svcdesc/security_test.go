package svcdesc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// Команда без as_root у службы-root идёт от nobody через setpriv.
func TestCommandArgv(t *testing.T) {
	s := Source{Command: []string{"consul", "members"}}
	got := strings.Join(commandArgv(s, "local", 0), " ")
	if got != "setpriv --reuid=65534 --regid=65534 --clear-groups --no-new-privs -- consul members" {
		t.Errorf("root: %s", got)
	}
	if got := strings.Join(commandArgv(s, "local", 1000), " "); got != "consul members" {
		t.Errorf("non-root: %s", got)
	}
	if got := strings.Join(commandArgv(s, "fixtures", 0), " "); got != "consul members" {
		t.Errorf("fixtures: %s", got)
	}
	s.AsRoot = true
	if got := strings.Join(commandArgv(s, "local", 0), " "); got != "consul members" {
		t.Errorf("as_root: %s", got)
	}
}

// Файлы секретов и HTTP наружу: закрытые пути и чужие адреса — ошибка
// описания.
func TestSecretFilesAndHTTP(t *testing.T) {
	for name, src := range map[string]string{
		"shadow password": "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, http: 'http://127.0.0.1/', basic_auth: {user: u, password_file: /etc/shadow}}]\n",
		"root bearer":     "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, http: 'http://127.0.0.1/', bearer_file: /root/token}]\n",
		"nkt env bearer":  "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, http: 'http://127.0.0.1/', bearer_file: /etc/netknownsthat/nkt.env}]\n",
		"external http":   "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, http: 'https://example.com/collect'}]\n",
		"public ip":       "name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, http: 'http://8.8.8.8/'}]\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := Parse([]byte("name: a\ndetect: {any: [{port: 1}]}\nsources: [{id: a, http: 'http://127.0.0.1:15672/api', basic_auth: {user: u, password_file: /etc/rabbitmq/p}}]\n")); err != nil {
		t.Errorf("local http with a password file: %v", err)
	}
}

// Ссылка на закрытый путь не читается, как и сам путь.
func TestSymlinkToClosedPath(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc", "myapp"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "etc", "shadow"), []byte("root:$6$x:1:0:99999:7:::\n"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "etc", "myapp", "ok.conf"), []byte("port 80\n"), 0o644)
	if err := os.Symlink("../shadow", filepath.Join(root, "etc", "myapp", "link.conf")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(root, "etc", "myapp", "out.conf")); err != nil {
		t.Skip(err)
	}
	d, err := Parse([]byte("name: myapp\nsources:\n  - id: c\n    file: /etc/myapp/*.conf\n    format: text\n"))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(collect.NewFixtures(root), "", t.TempDir())
	var warnings []string
	docs := e.read(context.Background(), d, d.Sources[0], func(k string, a ...any) { warnings = append(warnings, k) })
	if len(docs) != 1 || docs[0].File != "/etc/myapp/ok.conf" {
		t.Fatalf("docs: %+v", docs)
	}
	if len(warnings) != 2 || warnings[0] != "svcdesc.w.sensitive" {
		t.Fatalf("warnings: %v", warnings)
	}
}
