package clamav

import (
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	hit, _, _ := ParseLine("/var/www/html/shell.php: Php.Trojan.Webshell-1 FOUND")
	if hit == nil || hit.Path != "/var/www/html/shell.php" || hit.Signature != "Php.Trojan.Webshell-1" {
		t.Errorf("hit: %+v", hit)
	}
	if hit, c, n := ParseLine("Infected files: 3"); hit != nil || c != "Infected files" || n != 3 {
		t.Errorf("counter: %v %q %d", hit, c, n)
	}
	if hit, c, _ := ParseLine("/etc/passwd: OK"); hit != nil || c != "" {
		t.Errorf("OK-строка: %v %q", hit, c)
	}
}

func TestImageScanScript(t *testing.T) {
	s := ImageScanScript("docker", "nginx:alpine")
	for _, want := range []string{"docker create 'nginx:alpine'", "docker export", "clamscan -r --infected", "rm -rf"} {
		if !strings.Contains(s, want) {
			t.Errorf("нет %q в:\n%s", want, s)
		}
	}
	if !strings.Contains(ImageScanScript("podman", "a'b"), `'a'\''b'`) {
		t.Error("кавычка в имени образа не экранирована")
	}
}

func TestValidPath(t *testing.T) {
	for p, want := range map[string]bool{"/srv": true, "/": false, "srv": false, "/a/../b": false, "/x\ny": false} {
		if ValidPath(p) != want {
			t.Errorf("%q → %v", p, !want)
		}
	}
}
