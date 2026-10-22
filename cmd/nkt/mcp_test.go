package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// nkt mcp: строка stdin → POST /api/mcp с токеном, ответ — строкой;
// уведомление — без ответа; ошибка HTTP — ошибкой JSON-RPC.
func TestRunMCPBridge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mcp" || r.Header.Get("Authorization") != "Bearer nkt_test" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(string(body), "notifications/"):
			w.WriteHeader(http.StatusAccepted)
		case strings.Contains(string(body), `"fail"`):
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
		}
	}))
	defer srv.Close()
	t.Setenv("NKT_HUB_URL", srv.URL+"/")
	t.Setenv("NKT_API_TOKEN", "nkt_test")
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}
{"jsonrpc":"2.0","method":"notifications/initialized"}

{"jsonrpc":"2.0","id":2,"method":"fail"}
`)
	var out strings.Builder
	if err := runMCP(in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || lines[0] != `{"jsonrpc":"2.0","id":1,"result":{}}` || !strings.Contains(lines[1], `"id":2`) || !strings.Contains(lines[1], "HTTP 500") {
		t.Fatalf("out: %q", out.String())
	}
	t.Setenv("NKT_API_TOKEN", "")
	if err := runMCP(strings.NewReader(""), io.Discard); err == nil {
		t.Fatal("no token: want error")
	}
}
