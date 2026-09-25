package api

import (
	"strings"
	"testing"
)

func TestOriginScript(t *testing.T) {
	s := originScript(cmdOrigin{env: map[string]string{"DEBIAN_FRONTEND": "noninteractive", "TERM": "xterm"}, argv: []string{"bash", "-c", "apt-get update && apt-get install -y btop"}})
	if !strings.HasPrefix(s, "export DEBIAN_FRONTEND='noninteractive'\nexport TERM='xterm'\n") || !strings.Contains(s, "apt-get -o APT::Status-Fd=1 install -y btop") {
		t.Fatalf("%s", s)
	}
	u := originScript(cmdOrigin{argv: []string{"bash", "-c", "apt-get update && apt-get dist-upgrade"}})
	if !strings.Contains(u, "dist-upgrade -y -o Dpkg::Options::=--force-confdef") || !strings.Contains(u, "DEBIAN_FRONTEND=noninteractive") {
		t.Fatalf("%s", u)
	}
	if a := originScript(cmdOrigin{argv: []string{"apt-get", "install", "-y", "ufw"}}); !strings.Contains(a, "'apt-get' '-o' 'APT::Status-Fd=1' 'install' '-y' 'ufw'") {
		t.Fatalf("%s", a)
	}
	if k, args := updateJobTitle("system.install_btop", "apt-get install btop"); k != "cmdjob.titleInstall" || len(args) != 1 {
		t.Fatalf("%s %v", k, args)
	}
}

// Собранная, но не запущенная команда отдаёт свои argv и окружение.
func TestTakeOrigin(t *testing.T) {
	cmd := unrestrictedCommand(map[string]string{"A": "1"}, "echo", "hi")
	o, ok := takeOrigin(cmd)
	if !ok || o.argv[0] != "echo" || o.env["A"] != "1" {
		t.Fatalf("%+v %v", o, ok)
	}
	if _, ok := takeOrigin(cmd); ok {
		t.Fatal("повторно")
	}
}
