package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestPortForwardProxy(t *testing.T) {
	var gotPath, gotCookie string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCookie = r.URL.Path+"?"+r.URL.RawQuery, r.Header.Get("Cookie")
		http.SetCookie(w, &http.Cookie{Name: "app", Value: "x"})
		_, _ = io.WriteString(w, "hello")
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	local, _ := strconv.Atoi(u.Port())

	s := &Server{}
	s.pf.sessions = map[string]*pfSession{"tok": {Token: "tok", cmd: &exec.Cmd{}, LastUsed: time.Now(), proxy: pfProxy(local, 80)}}
	r := chi.NewRouter()
	r.HandleFunc("/api/k8s/pf/{token}", s.handleK8sPortForwardProxy)
	r.HandleFunc("/api/k8s/pf/{token}/*", s.handleK8sPortForwardProxy)

	req := httptest.NewRequest("GET", "/api/k8s/pf/tok/static/app.js?v=1", nil)
	req.AddCookie(&http.Cookie{Name: "nkt_session", Value: "secret"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "hello" || gotPath != "/static/app.js?v=1" || gotCookie != "" {
		t.Fatalf("code %d body %q path %q cookie %q", rec.Code, rec.Body.String(), gotPath, gotCookie)
	}
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Set-Cookie") != "" || len(rec.Header().Values("Referrer-Policy")) != 1 || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("заголовки: %v", rec.Header())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/k8s/pf/tok", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/api/k8s/pf/tok/" {
		t.Errorf("редирект: %d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/k8s/pf/other/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("чужой токен: %d", rec.Code)
	}
}

func TestPortForwardOwnOrigin(t *testing.T) {
	var gotCookie, gotPrefix string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie, gotPrefix = r.Header.Get("Cookie"), r.Header.Get("X-Forwarded-Prefix")
		w.Header().Add("Set-Cookie", "sid=1; Path=/; HttpOnly")
		w.Header().Add("Set-Cookie", "nkt_session=evil; Path=/")
		_, _ = io.WriteString(w, "ok")
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	local, _ := strconv.Atoi(u.Port())
	s := &Server{}
	s.pf.sessions = map[string]*pfSession{"tok": {Token: "tok", cmd: &exec.Cmd{}, LastUsed: time.Now(), proxy: pfProxy(local, 80)}}
	h := s.ForwardHandler()
	req := httptest.NewRequest("GET", "/f/tok/app/", nil)
	req.Header.Set("Cookie", "nkt_session=admin; sid=1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || gotCookie != "sid=1" || gotPrefix != "/f/tok" {
		t.Fatalf("code %d cookie %q prefix %q", rec.Code, gotCookie, gotPrefix)
	}
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Error("песочница на своём origin")
	}
	sc := rec.Header().Values("Set-Cookie")
	if len(sc) != 1 || sc[0] != "sid=1; HttpOnly; Path=/f/tok/" {
		t.Errorf("Set-Cookie: %v", sc)
	}
}
