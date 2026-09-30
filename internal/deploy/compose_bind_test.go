package deploy

import (
	"strings"
	"testing"
)

func TestBindPorts(t *testing.T) {
	src := `services:
  web:
    image: nginx
    ports:
      - "8080:80"
      - 443
      - "9000-9001:9000-9001/udp"
      - "10.0.0.5:2222:22"
      - "[::1]:5000:5000"
      - "${HTTP_PORT:-81}:80"
      - target: 3000
        published: 3000
      - target: 4000
        host_ip: 0.0.0.0
  db:
    image: postgres
`
	out, changes, err := BindPorts(src, "127.0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"127.0.0.1:8080:80"`, `"127.0.0.1::443"`, `"127.0.0.1:9000-9001:9000-9001/udp"`,
		`"10.0.0.5:2222:22"`, `"[::1]:5000:5000"`, "${HTTP_PORT:-81}:80", "host_ip: 127.0.0.1", "host_ip: 0.0.0.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in\n%s", want, out)
		}
	}
	var bound, kept, skipped int
	for _, c := range changes {
		switch {
		case c.Skipped:
			skipped++
		case c.Kept:
			kept++
		default:
			bound++
		}
	}
	if bound != 4 || kept != 3 || skipped != 1 {
		t.Fatalf("bound %d kept %d skipped %d: %+v", bound, kept, skipped, changes)
	}
	// force: и указанные адреса; IPv6-адрес bind — в скобках.
	out, _, _ = BindPorts(src, "::1", true)
	if !strings.Contains(out, `"[::1]:2222:22"`) || !strings.Contains(out, "host_ip: ::1") || strings.Contains(out, "10.0.0.5") {
		t.Fatalf("force:\n%s", out)
	}
	// На всех адресах без force — без изменений.
	if out, _, _ := BindPorts(src, "0.0.0.0", false); out != src {
		t.Fatal("0.0.0.0 changed the file")
	}
	base := "repo: https://codeberg.org/me/app.git\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: app\n  hosts: [web1]\n"
	for _, ok := range []string{"", "  bind: 0.0.0.0\n", "  bind: 10.0.0.5\n  bind_force: true\n", "  bind: '::1'\n"} {
		s, err := ParseSpec(base + ok)
		if err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
		if ok == "" && s.Compose.BindAddr() != "127.0.0.1" {
			t.Fatal(s.Compose.BindAddr())
		}
	}
	for _, bad := range []string{"  bind: localhost\n", "  bind: '1.2.3.4; rm'\n", "  bind: 'fe80::1%eth0'\n"} {
		if _, err := ParseSpec(base + bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSitePortHints(t *testing.T) {
	base := "repo: https://codeberg.org/me/app.git\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: app\n  hosts: [web1]\n  site:\n    domains: [a.example.com]\n    service: httpbin\n"
	// Без port и с port: auto — порт из образа (0 до выкладки).
	for _, extra := range []string{"", "    port: auto\n"} {
		s, err := ParseSpec(base + extra)
		if err != nil || s.Compose.Site.Port != 0 {
			t.Fatalf("auto %q: %+v %v", extra, s.Compose.Site, err)
		}
	}
	// site.port — порт хоста из ports: понятная ошибка.
	if _, err := ParseSpec(base + "    port: 8080\n  ports:\n    httpbin: [\"127.0.0.1:8080:80\"]\n"); err == nil || !strings.Contains(err.Error(), "порт на хосте") {
		t.Fatalf("host port as site.port: %v", err)
	}
	s, err := ParseSpec(base + "    port: 81\n  ports:\n    httpbin: [\"127.0.0.1:8080:80\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Compose.SitePortsMismatch(); len(got) != 1 || got[0] != 80 {
		t.Fatalf("mismatch: %v", got)
	}
	s, _ = ParseSpec(base + "    port: 80\n  ports:\n    httpbin: [\"127.0.0.1:8080:80\"]\n")
	if got := s.Compose.SitePortsMismatch(); got != nil {
		t.Fatalf("false mismatch: %v", got)
	}
}
