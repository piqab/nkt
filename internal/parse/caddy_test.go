package parse

import (
	"context"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/model"
)

func TestParseCaddyAddress(t *testing.T) {
	cases := []struct {
		name      string
		addr      string
		wantOK    bool
		wantAddr  string
		wantPort  int
		wantTLS   bool
		wantNames []string
	}{
		{"bare hostname gets automatic HTTPS on 443", "example.com", true, "0.0.0.0", 443, true, []string{"example.com"}},
		{"explicit http scheme forces plaintext 80", "http://example.com", true, "0.0.0.0", 80, false, []string{"example.com"}},
		{"explicit https scheme with explicit port", "https://example.com:8443", true, "0.0.0.0", 8443, true, []string{"example.com"}},
		{"bare port, no host, is plaintext", ":8080", true, "0.0.0.0", 8080, false, nil},
		{"hostname on port 80 explicitly is plaintext", "example.com:80", true, "0.0.0.0", 80, false, []string{"example.com"}},
		{"hostname on a non-80 port still gets HTTPS", "example.com:8443", true, "0.0.0.0", 8443, true, []string{"example.com"}},
		{"a literal IP is the bind address, not a name", "10.0.0.5:8080", true, "10.0.0.5", 8080, false, nil},
		{"a trailing path matcher is dropped", "example.com/api/*", true, "0.0.0.0", 443, true, []string{"example.com"}},
		{"empty string is rejected", "", false, "", 0, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, port, tls, names, ok := parseCaddyAddress(tc.addr)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if addr != tc.wantAddr || port != tc.wantPort || tls != tc.wantTLS {
				t.Errorf("got (%q, %d, %v), want (%q, %d, %v)", addr, port, tls, tc.wantAddr, tc.wantPort, tc.wantTLS)
			}
			if len(names) != len(tc.wantNames) {
				t.Errorf("names = %v, want %v", names, tc.wantNames)
			}
			for i := range names {
				if names[i] != tc.wantNames[i] {
					t.Errorf("names = %v, want %v", names, tc.wantNames)
				}
			}
		})
	}
}

// TestCaddyParsesFixture exercises the real Caddyfile end to end: a single-
// backend site (a plain address Route, no synthesised Upstream), a
// multi-backend site (a real Upstream with both servers), a redirect, a
// static site, and both TLS-off shapes (bare port, explicit http://).
func TestCaddyParsesFixture(t *testing.T) {
	res := Caddy(context.Background(), fixtureCollector(t), "/etc/caddy/Caddyfile")
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}
	if !res.Status.Available {
		t.Fatal("Status.Available = false")
	}

	byLabel := map[string]model.Endpoint{}
	for _, e := range res.Endpoints {
		byLabel[e.Label] = e
	}

	acme, ok := byLabel["acme.example.com"]
	if !ok {
		t.Fatal("acme.example.com endpoint не найден")
	}
	if !acme.TLS || acme.Port != 443 {
		t.Errorf("acme.example.com: tls=%v port=%d, want tls=true port=443", acme.TLS, acme.Port)
	}
	if len(acme.Routes) != 1 || acme.Routes[0].TargetKind != "address" || acme.Routes[0].Target != "127.0.0.1:9001" {
		t.Errorf("acme.example.com routes = %+v, want one address route to 127.0.0.1:9001", acme.Routes)
	}

	lb, ok := byLabel["lb.example.com"]
	if !ok {
		t.Fatal("lb.example.com endpoint не найден")
	}
	if len(lb.Routes) != 1 || lb.Routes[0].TargetKind != "upstream" {
		t.Fatalf("lb.example.com routes = %+v, want one upstream route", lb.Routes)
	}
	var lbUpstream *model.Upstream
	for i := range res.Upstreams {
		if res.Upstreams[i].Name == lb.Routes[0].Target {
			lbUpstream = &res.Upstreams[i]
		}
	}
	if lbUpstream == nil || len(lbUpstream.Servers) != 2 {
		t.Fatalf("lb.example.com upstream = %+v, want 2 servers", lbUpstream)
	}

	old, ok := byLabel["old.example.com"]
	if !ok {
		t.Fatal("old.example.com endpoint не найден")
	}
	if len(old.Routes) != 1 || old.Routes[0].TargetKind != "redirect" {
		t.Errorf("old.example.com routes = %+v, want one redirect route", old.Routes)
	}

	static, ok := byLabel["static.example.com"]
	if !ok {
		t.Fatal("static.example.com endpoint не найден")
	}
	sawStatic := false
	for _, r := range static.Routes {
		if r.TargetKind == "static" {
			sawStatic = true
		}
	}
	if !sawStatic {
		t.Errorf("static.example.com routes = %+v, want at least one static route", static.Routes)
	}

	barePort, ok := byLabel[":8081"]
	if !ok {
		t.Fatal(":8081 endpoint не найден")
	}
	if barePort.TLS || barePort.Port != 8081 {
		t.Errorf(":8081: tls=%v port=%d, want tls=false port=8081", barePort.TLS, barePort.Port)
	}

	plain, ok := byLabel["http://plain.example.com"]
	if !ok {
		t.Fatal("http://plain.example.com endpoint не найден")
	}
	if plain.TLS || plain.Port != 80 {
		t.Errorf("plain.example.com: tls=%v port=%d, want tls=false port=80", plain.TLS, plain.Port)
	}

	if len(res.Files) != 1 || res.Files[0].Path != "/etc/caddy/Caddyfile" {
		t.Errorf("Files = %+v, want exactly the Caddyfile itself", res.Files)
	}
}

func TestCaddyBlocksFixture(t *testing.T) {
	blocks, err := Blocks(fixtureCollector(t), "/etc/caddy/Caddyfile", model.ServiceCaddy)
	if err != nil {
		t.Fatalf("Blocks: %v", err)
	}
	// 6 site blocks — the leading bare "{ admin off }" global options block
	// must NOT show up as a site (no address to name it after).
	if len(blocks) != 6 {
		t.Fatalf("got %d blocks, want 6: %+v", len(blocks), blocks)
	}
	for _, b := range blocks {
		if b.Kind != BlockSite {
			t.Errorf("block %q kind = %q, want %q", b.Name, b.Kind, BlockSite)
		}
		if !strings.HasSuffix(b.Raw, "}") {
			t.Errorf("block %q Raw does not end with its closing brace: %q", b.Name, b.Raw)
		}
	}
}
