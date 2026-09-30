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
		strings.Replace(block, "port: 8080", "port: 70000", 1),
		strings.Replace(block, "service: web", "service: 'a b'", 1),
		block + "    proxy: apache\n",
		block + "    unknown: 1\n",
	} {
		if _, err := ParseSpec(bad); err == nil {
			t.Fatalf("accepted:\n%s", bad)
		}
	}
}

func TestOverrideImages(t *testing.T) {
	// docker-compose.yml из github.com/postmanlabs/httpbin.
	src := "version: '2'\nservices:\n    httpbin:\n      build: '.'\n      ports:\n        - '80:80'\n"
	out, err := OverrideImages(src, map[string]string{"httpbin": "kennethreitz/httpbin"})
	if err != nil || strings.Contains(out, "build") || !strings.Contains(out, "image: kennethreitz/httpbin") || !strings.Contains(out, "80:80") {
		t.Fatalf("%v\n%s", err, out)
	}
	if names := BuildOnlyServices(out); names != nil {
		t.Fatal(names)
	}
	if _, err := OverrideImages(src, map[string]string{"web": "nginx"}); err == nil {
		t.Fatal("unknown service accepted")
	}
	base := "repo: https://github.com/postmanlabs/httpbin.git\nref: master\naction: compose\ncompose:\n  file: docker-compose.yml\n  project: httpbin\n  hosts: [cn4]\n"
	if _, err := ParseSpec(base + "  images:\n    httpbin: kennethreitz/httpbin\n"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"  images:\n    httpbin: 'x; rm -rf /'\n", "  images:\n    'a b': nginx\n"} {
		if _, err := ParseSpec(base + bad); err == nil {
			t.Fatalf("accepted: %s", bad)
		}
	}
	if _, err := ParseSpec(base + "  images:\n    httpbin: ghcr.io/me/httpbin:{{nkt.tag}}\n"); err != nil {
		t.Fatal(err)
	}
}

func TestOverridePorts(t *testing.T) {
	src := "version: '2'\nservices:\n    httpbin:\n      build: '.'\n      ports:\n        - '80:80'\n    db:\n      image: postgres\n"
	out, err := OverrideServices(src, map[string]string{"httpbin": "kennethreitz/httpbin"}, map[string][]string{"httpbin": {}, "db": {"127.0.0.1:5432:5432"}}, nil)
	if err != nil || strings.Contains(out, "80:80") || strings.Contains(out, "build") || !strings.Contains(out, `"127.0.0.1:5432:5432"`) {
		t.Fatalf("%v\n%s", err, out)
	}
	base := "repo: https://github.com/postmanlabs/httpbin.git\nref: master\naction: compose\ncompose:\n  file: docker-compose.yml\n  project: httpbin\n  hosts: [cn4]\n"
	if _, err := ParseSpec(base + "  ports:\n    httpbin: []\n"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"  ports:\n    httpbin: ['80:80; rm']\n", "  ports:\n    httpbin: ['abc']\n"} {
		if _, err := ParseSpec(base + bad); err == nil {
			t.Fatalf("accepted: %s", bad)
		}
	}
	if _, err := OverrideServices(src, nil, map[string][]string{"nope": {}}, nil); err == nil {
		t.Fatal("unknown service accepted")
	}
}
