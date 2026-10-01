package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Роль api: до хаба доходят только подписанные запросы токенов к API;
// хаб не подключён — 503 (значит, фильтры пройдены).
func TestHandleAPIFilters(t *testing.T) {
	s := &server{roles: map[string]bool{"api": true}, limiter: newLimiter(60), apiLimiter: newLimiter(120)}
	signed := func(r *http.Request) {
		r.Header.Set("X-NKT-API-Key", "abcdefghijklmnop")
		r.Header.Set("X-NKT-API-Signature", "00")
	}
	cases := []struct {
		name, path string
		set        func(*http.Request)
		want       int
	}{
		{"signed", "/api/hub/hosts", signed, http.StatusServiceUnavailable},
		{"unsigned", "/api/hub/hosts", nil, http.StatusUnauthorized},
		{"bearer", "/api/hub/hosts", func(r *http.Request) { signed(r); r.Header.Set("Authorization", "Bearer x") }, http.StatusUnauthorized},
		{"login", "/api/auth/login", signed, http.StatusNotFound},
		{"hooks via api", "/api/hub/hooks/x", signed, http.StatusNotFound},
		{"websocket", "/api/hosts/1/terminal/ws", func(r *http.Request) { signed(r); r.Header.Set("Upgrade", "websocket") }, http.StatusNotFound},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.path, nil)
		if c.set != nil {
			c.set(req)
		}
		rec := httptest.NewRecorder()
		s.handleAPI(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: want %d, got %d", c.name, c.want, rec.Code)
		}
	}
}

// Роль callbacks: только POST /callbacks/<платформа>/<вид>.
func TestHandleCallbackFilters(t *testing.T) {
	s := &server{roles: map[string]bool{"callbacks": true}, limiter: newLimiter(60), apiLimiter: newLimiter(120)}
	for path, want := range map[string]int{
		"/callbacks/slack/commands":    http.StatusServiceUnavailable, // фильтры пройдены, хаба нет
		"/callbacks/slack/../../api/x": http.StatusNotFound,
		"/callbacks/slack":             http.StatusNotFound,
		"/callbacks/Slack/commands":    http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		s.handleCallback(rec, httptest.NewRequest("POST", path, nil))
		if rec.Code != want {
			t.Errorf("%s: want %d, got %d", path, want, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.handleCallback(rec, httptest.NewRequest("GET", "/callbacks/slack/commands", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET: %d", rec.Code)
	}
}
