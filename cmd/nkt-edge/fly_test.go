package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Команда хаба /fly/... доходит до nkt-fly с токеном edge; без роли fly
// такого пути нет.
func TestHubHandlerFly(t *testing.T) {
	var gotToken, gotPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken, gotPath = r.Header.Get(flyHeader), r.URL.Path
		io.WriteString(w, `{"public_key":"k"}`)
	}))
	defer fake.Close()
	t.Setenv("FLY_API_ADDR", strings.TrimPrefix(fake.URL, "http://"))
	s := &server{token: strings.Repeat("t", 32), roles: map[string]bool{"fly": true}}
	h := s.hubHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/fly/key", nil))
	if rec.Code != 200 || gotToken != s.token || gotPath != "/fly/key" || !strings.Contains(rec.Body.String(), "public_key") {
		t.Fatalf("code %d token %q path %q body %s", rec.Code, gotToken, gotPath, rec.Body)
	}
	// Без роли fly — ни пути, ни обработчика вовсе (только probe).
	s2 := &server{token: s.token, roles: map[string]bool{"probe": true}}
	rec = httptest.NewRecorder()
	s2.hubHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/fly/key", nil))
	if rec.Code != 404 {
		t.Fatalf("без роли fly: %d", rec.Code)
	}
	if (&server{roles: map[string]bool{"hooks": true}}).hubHandler() != nil {
		t.Fatal("без probe и fly хабу нечего слушать")
	}
}
