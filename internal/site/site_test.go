package site

import (
	"strings"
	"testing"
)

func TestNormalizeDomains(t *testing.T) {
	got, err := NormalizeDomains([]string{"App.Example.com, www.app.example.com", "app.example.com."})
	if err != nil || len(got) != 2 || got[0] != "app.example.com" {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"", "localhost", "a..b.com", "-x.com", "x.com;rm", "ex ample.com"} {
		if _, err := NormalizeDomains([]string{bad}); err == nil && bad != "ex ample.com" {
			t.Fatalf("%q accepted", bad)
		}
	}
	if _, err := ValidUpstream("127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"127.0.0.1", "host:80", "127.0.0.1:0", "127.0.0.1:99999", "$(x):80"} {
		if _, err := ValidUpstream(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestNginxAndCaddy(t *testing.T) {
	c := Config{Domains: []string{"app.example.com", "www.app.example.com"}, Upstream: "127.0.0.1:18000", Lineage: "app.example.com"}
	n := Nginx(c)
	for _, want := range []string{"server_name app.example.com www.app.example.com;", "proxy_pass http://127.0.0.1:18000;",
		"ssl_certificate /etc/letsencrypt/live/app.example.com/fullchain.pem;", "return 301 https://", "map $http_upgrade $nkt_conn_app_example_com"} {
		if !strings.Contains(n, want) {
			t.Fatalf("nginx lacks %q:\n%s", want, n)
		}
	}
	cd := Caddy(c)
	if !strings.Contains(cd, "app.example.com, www.app.example.com {") || !strings.Contains(cd, "reverse_proxy 127.0.0.1:18000") {
		t.Fatal(cd)
	}
	main, changed := EnsureCaddyImport(":80 {\n}\n", "/etc/caddy")
	if !changed || !strings.Contains(main, "import /etc/caddy/nkt/*.caddy") {
		t.Fatal(main)
	}
	if _, again := EnsureCaddyImport(main, "/etc/caddy"); again {
		t.Fatal("import added twice")
	}
}

func TestHAProxyBlock(t *testing.T) {
	main := "global\n    daemon\n\ndefaults\n    mode http\n"
	block := HAProxyBlock([]Config{{Domains: []string{"b.example.com"}, Upstream: "127.0.0.1:18001"}, {Domains: []string{"a.example.com"}, Upstream: "127.0.0.1:18000"}})
	withBlock := ReplaceHAProxyBlock(main, block)
	if !strings.Contains(withBlock, "backend nkt_a_example_com") || !strings.Contains(withBlock, "server app 127.0.0.1:18001") ||
		strings.Index(withBlock, "nkt_a_example_com") > strings.Index(withBlock, "nkt_b_example_com") {
		t.Fatal(withBlock)
	}
	if _, busy := HAProxyForeignBind(withBlock); busy {
		t.Fatal("own block counted as foreign")
	}
	// Замена блока, а не второй блок.
	again := ReplaceHAProxyBlock(withBlock, HAProxyBlock([]Config{{Domains: []string{"a.example.com"}, Upstream: "127.0.0.1:18000"}}))
	if strings.Count(again, HAProxyBegin) != 1 || strings.Contains(again, "b_example_com") {
		t.Fatal(again)
	}
	if cleared := ReplaceHAProxyBlock(again, ""); strings.Contains(cleared, "nkt") || !strings.HasPrefix(cleared, "global") {
		t.Fatal(cleared)
	}
	if port, busy := HAProxyForeignBind(main + "\nfrontend web\n    bind *:443 ssl crt /x.pem\n"); !busy || port != "443" {
		t.Fatal("foreign bind not found")
	}
}

func TestOverride(t *testing.T) {
	out, err := Override("", "web", 18000, 80)
	if err != nil || !strings.Contains(out, "127.0.0.1:18000:80") {
		t.Fatal(out, err)
	}
	out2, err := Override(out, "web", 18005, 80)
	if err != nil || strings.Contains(out2, "18000") || !strings.Contains(out2, "127.0.0.1:18005:80") {
		t.Fatal(out2, err)
	}
	out3, _ := Override(out2, "api", 18006, 3000)
	if !strings.Contains(out3, "18005:80") || !strings.Contains(out3, "18006:3000") {
		t.Fatal(out3)
	}
	if _, err := Override("services: [", "web", 1, 1); err == nil {
		t.Fatal("broken yaml accepted")
	}
}
