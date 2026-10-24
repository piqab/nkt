package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testManifest() Manifest {
	return Manifest{Protocol: 1, Name: "dns", Title: "DNS", Version: "1.0",
		Ops: []Op{
			{Name: "zones", ReadOnly: true, MCP: true},
			{Name: "zone_add", Input: map[string]any{"type": "object"}},
		},
		Pages:    []Page{{ID: "zones", Title: "Zones", Op: "zones"}},
		Commands: []Command{{Word: "dns", Usage: "dns set NAME TYPE VALUE"}},
		Provides: []string{"dns"},
	}
}

func TestValidate(t *testing.T) {
	m := testManifest()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	for what, mod := range map[string]func(*Manifest){
		"protocol":       func(m *Manifest) { m.Protocol = 2 },
		"name":           func(m *Manifest) { m.Name = "DNS!" },
		"op name":        func(m *Manifest) { m.Ops[0].Name = "Zones" },
		"dup op":         func(m *Manifest) { m.Ops[1].Name = "zones" },
		"page op":        func(m *Manifest) { m.Pages[0].Op = "nope" },
		"page writes":    func(m *Manifest) { m.Pages[0].Op = "zone_add" },
		"command word":   func(m *Manifest) { m.Commands[0].Word = "DNS set" },
		"provides":       func(m *Manifest) { m.Provides = []string{"mail"} },
		"repeated words": func(m *Manifest) { m.Commands = append(m.Commands, m.Commands[0]) },
	} {
		m := testManifest()
		mod(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
}

// Клиент подписывает, плагин проверяет; чужой секрет и старая отметка —
// отказ.
func TestClientSignedCalls(t *testing.T) {
	const secret = "s3cret"
	var gotLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !Verify(secret, r, body, time.Now()) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotLang = r.Header.Get(HeaderLang)
		switch r.URL.Path {
		case PathManifest:
			_ = json.NewEncoder(w).Encode(testManifest())
		case PathCall:
			var req Request
			_ = json.Unmarshal(body, &req)
			if req.Op == "fail" {
				_ = json.NewEncoder(w).Encode(Response{Error: "zone exists"})
				return
			}
			_ = json.NewEncoder(w).Encode(Response{OK: true, Text: "op " + req.Op + " " + req.Args["zone"].(string)})
		}
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	dial := func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "tcp", addr) }

	c := Client{Dial: dial, Secret: secret, Lang: "ru"}
	m, err := c.Manifest(context.Background())
	if err != nil || m.Name != "dns" || gotLang != "ru" {
		t.Fatalf("manifest: %+v %v %q", m, err, gotLang)
	}
	resp, err := c.Call(context.Background(), Request{Op: "zone_add", Args: map[string]any{"zone": "example.com"}})
	if err != nil || resp.Text != "op zone_add example.com" {
		t.Fatalf("call: %+v %v", resp, err)
	}
	_, err = c.Call(context.Background(), Request{Op: "fail", Args: map[string]any{"zone": "x"}})
	var ce *CallError
	if !errors.As(err, &ce) || ce.Msg != "zone exists" {
		t.Fatalf("plugin error: %v", err)
	}
	bad := Client{Dial: dial, Secret: "other"}
	if _, err := bad.Manifest(context.Background()); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("wrong secret: %v", err)
	}

	// Старая отметка.
	req := httptest.NewRequest("POST", PathCall, nil)
	old := time.Now().Add(-10 * time.Minute)
	ts := "1"
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, Sign(secret, ts, PathCall, nil))
	if Verify(secret, req, nil, old) {
		t.Fatal("stale timestamp accepted")
	}
}
