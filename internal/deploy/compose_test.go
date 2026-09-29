package deploy

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildOnlyServices(t *testing.T) {
	// docker-compose.yml из github.com/postmanlabs/httpbin.
	httpbin := "version: '2'\nservices:\n    httpbin:\n      build: '.'\n      ports:\n        - '80:80'\n"
	if got := BuildOnlyServices(httpbin); !reflect.DeepEqual(got, []string{"httpbin"}) {
		t.Fatal(got)
	}
	both := "services:\n  web:\n    build: .\n    image: ghcr.io/org/web:1\n  db:\n    image: postgres:16\n"
	if got := BuildOnlyServices(both); got != nil {
		t.Fatal(got)
	}
	if got := BuildOnlyServices("services: [broken"); got != nil {
		t.Fatal(got)
	}
}

func TestSiteSpec(t *testing.T) {
	base := "repo: https://codeberg.org/me/app.git\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: app\n  hosts: [web1]\n"
	s, err := ParseSpec(base + "  site: App.Example.com\n")
	if err != nil || s.Compose.Site.Managed() || s.Compose.Site.CheckDomain() != "app.example.com" {
		t.Fatalf("string form: %+v %v", s.Compose.Site, err)
	}
	block := base + "  site:\n    domains: [App.example.com, www.app.example.com]\n    service: web\n    port: 8080\n"
	s, err = ParseSpec(block)
	if err != nil || !s.Compose.Site.Managed() || s.Compose.Site.Domains[0] != "app.example.com" || !s.Compose.Site.OpenFirewall() {
		t.Fatalf("block form: %+v %v", s.Compose.Site, err)
	}
	for _, bad := range []string{
		strings.Replace(block, "hosts: [web1]", "hosts: [web1, web2]", 1),
		strings.Replace(block, "hosts: [web1]", "group: prod", 1),
		strings.Replace(block, "port: 8080", "port: 0", 1),
		strings.Replace(block, "service: web", "service: 'a b'", 1),
		block + "    proxy: apache\n",
		block + "    unknown: 1\n",
	} {
		if _, err := ParseSpec(bad); err == nil {
			t.Fatalf("accepted:\n%s", bad)
		}
	}
}
