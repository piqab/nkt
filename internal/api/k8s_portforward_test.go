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
	var gotPath, gotCookie, gotHost string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCookie, gotHost = r.URL.Path+"?"+r.URL.RawQuery, r.Header.Get("Cookie"), r.Host
		http.SetCookie(w, &http.Cookie{Name: "app", Value: "x"})
		_, _ = io.WriteString(w, "hello")
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	local, _ := strconv.Atoi(u.Port())

	s := &Server{}
	s.pf.sessions = map[string]*pfSession{"0123456789abcdef0123456789abcdef": {Token: "0123456789abcdef0123456789abcdef", cmd: &exec.Cmd{}, LastUsed: time.Now(), proxy: pfProxy(local, 80, "hub.example:8446")}}
	r := chi.NewRouter()
	r.HandleFunc("/api/k8s/pf/{token}", s.handleK8sPortForwardProxy)
	r.HandleFunc("/api/k8s/pf/{token}/*", s.handleK8sPortForwardProxy)

	req := httptest.NewRequest("GET", "/api/k8s/pf/0123456789abcdef0123456789abcdef/static/app.js?v=1", nil)
	req.AddCookie(&http.Cookie{Name: "nkt_session", Value: "secret"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "hello" || gotPath != "/static/app.js?v=1" || gotCookie != "" {
		t.Fatalf("code %d body %q path %q cookie %q", rec.Code, rec.Body.String(), gotPath, gotCookie)
	}
	// Песочница: Host остаётся синтетическим, даже когда внешний адрес поднят.
	if gotHost != "127.0.0.1:80" {
		t.Errorf("host в песочнице: %q", gotHost)
	}
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Set-Cookie") != "" || len(rec.Header().Values("Referrer-Policy")) != 1 || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("заголовки: %v", rec.Header())
	}
	// Адрес пробросов: песочницы нет, приложение видит внешний адрес в Host.
	rec2 := httptest.NewRecorder()
	s.ForwardHandler().ServeHTTP(rec2, httptest.NewRequest("GET", "/f/0123456789abcdef0123456789abcdef/static/app.js", nil))
	if rec2.Code != 200 || rec2.Body.String() != "hello" || gotHost != "hub.example:8446" {
		t.Fatalf("адрес пробросов: code %d body %q host %q", rec2.Code, rec2.Body.String(), gotHost)
	}
	if rec2.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("на адресе пробросов песочницы быть не должно: %v", rec2.Header())
	}
	// Хаб маршрутизировал проброс и передал свой внешний адрес: он побеждает
	// конфигурацию хоста — приложение видит адрес страницы браузера.
	req3 := httptest.NewRequest("GET", "/f/0123456789abcdef0123456789abcdef/app/", nil)
	req3.Header.Set("X-NKT-Forward-Host", "hub.example:9446")
	rec3 := httptest.NewRecorder()
	s.ForwardHandler().ServeHTTP(rec3, req3)
	if rec3.Code != 200 || gotHost != "hub.example:9446" {
		t.Fatalf("заголовок хаба: code %d host %q", rec3.Code, gotHost)
	}
	// В песочнице заголовок не действует: Host синтетический.
	req4 := httptest.NewRequest("GET", "/api/k8s/pf/0123456789abcdef0123456789abcdef/app/", nil)
	req4.Header.Set("X-NKT-Forward-Host", "hub.example:9446")
	rec4 := httptest.NewRecorder()
	r.ServeHTTP(rec4, req4)
	if rec4.Code != 200 || gotHost != "127.0.0.1:80" {
		t.Fatalf("заголовок в песочнице: code %d host %q", rec4.Code, gotHost)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/k8s/pf/0123456789abcdef0123456789abcdef", nil))
	if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
		t.Errorf("без косой: %d %v", rec.Code, rec.Header())
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
	s.pf.sessions = map[string]*pfSession{"0123456789abcdef0123456789abcdef": {Token: "0123456789abcdef0123456789abcdef", cmd: &exec.Cmd{}, LastUsed: time.Now(), proxy: pfProxy(local, 80, "")}}
	h := s.ForwardHandler()
	req := httptest.NewRequest("GET", "/f/0123456789abcdef0123456789abcdef/app/", nil)
	req.Header.Set("Cookie", "nkt_session=admin; sid=1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || gotCookie != "sid=1" || gotPrefix != "/f/0123456789abcdef0123456789abcdef" {
		t.Fatalf("code %d cookie %q prefix %q", rec.Code, gotCookie, gotPrefix)
	}
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Error("песочница на своём origin")
	}
	sc := rec.Header().Values("Set-Cookie")
	if len(sc) != 1 || sc[0] != "sid=1; HttpOnly; Path=/f/0123456789abcdef0123456789abcdef/" {
		t.Errorf("Set-Cookie: %v", sc)
	}
}
