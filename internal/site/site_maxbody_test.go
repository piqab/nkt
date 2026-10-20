package site

import (
	"strings"
	"testing"
)

func TestNginxMaxBody(t *testing.T) {
	base := Config{Domains: []string{"s3.example.com"}, Upstream: "127.0.0.1:13900", Lineage: "s3.example.com"}
	if c := Nginx(base); !strings.Contains(c, "client_max_body_size 64m;") || strings.Contains(c, "proxy_request_buffering") {
		t.Fatalf("default:\n%s", c)
	}
	base.MaxBody = "0"
	if c := Nginx(base); !strings.Contains(c, "client_max_body_size 0;") || !strings.Contains(c, "proxy_request_buffering off;") {
		t.Fatalf("unlimited:\n%s", c)
	}
	base.MaxBody = "2g"
	if c := Nginx(base); !strings.Contains(c, "client_max_body_size 2g;") {
		t.Fatalf("2g:\n%s", c)
	}
	for v, ok := range map[string]bool{"": true, "0": true, "512m": true, "10": true, "1x": false, "-1": false, "0m": false, "1;evil": false} {
		if ValidMaxBody(v) != ok {
			t.Errorf("%q: %v", v, !ok)
		}
	}
}
