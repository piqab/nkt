package hub

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Прокси отвечает чужим сертификатом (www вместо hb) — видно, чей он.
func TestHTTPSCheckWrongCert(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.StartTLS()
	defer srv.Close()
	// Сертификат httptest выдан на example.com и 127.0.0.1.
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	httpsCheckRoots = pool
	httpsCheckDial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	defer func() { httpsCheckRoots, httpsCheckDial = nil, nil }()

	chk := httpsCheck(context.Background(), "hb.example.org", "")
	if chk.OK || !chk.WrongCert || len(chk.CertNames) == 0 || chk.CertNames[0] != "example.com" {
		t.Fatalf("wrong cert not detected: %+v", chk)
	}
	chk = httpsCheck(context.Background(), "example.com", "")
	if !chk.OK || chk.WrongCert {
		t.Fatalf("right cert: %+v", chk)
	}
}

// Что считать ответом сайта: без названного пути — как проверялось всегда
// (любой код кроме 5xx), с названным — что путь открылся. 401 и 403 — тоже
// успех: интерфейс жив и просит вход, ровно то, что даёт /ui под
// аутентификацией.
func TestHTTPSStatusOK(t *testing.T) {
	for _, c := range []struct {
		path   string
		status int
		want   bool
	}{
		{"", 200, true}, {"", 301, true}, {"", 404, true}, {"", 451, true}, {"", 500, false}, {"", 502, false},
		{"/ui", 200, true}, {"/ui", 204, true}, {"/ui", 302, true},
		{"/ui", 401, true}, {"/ui", 403, true},
		{"/ui", 400, false}, {"/ui", 404, false}, {"/ui", 410, false}, {"/ui", 500, false}, {"/ui", 503, false},
	} {
		if got := httpsStatusOK(c.path, c.status); got != c.want {
			t.Errorf("path %q status %d: %v, want %v", c.path, c.status, got, c.want)
		}
	}
	// Имя сайта в журнале: путь виден, поэтому «отвечает» и «401 на /ui»
	// различимы.
	if httpsTarget("shop.example.org", "") != "shop.example.org" {
		t.Fatal("root target names the domain only")
	}
	if httpsTarget("shop.example.org", "/ui") != "shop.example.org/ui" {
		t.Fatal(`with a path: "shop.example.org/ui"`)
	}
}

// Названный путь доезжает до запроса: сервер отвечает кодом из пути, и
// проверка сдаёт именно его путь, а не корень.
func TestHTTPSCheckUsesPath(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/s/")); err == nil {
			w.WriteHeader(code)
			return
		}
		// Корень у стека с веб-мордой часто 404 — без названного пути это
		// не провал выкладки.
		w.WriteHeader(http.StatusNotFound)
	}))
	srv.StartTLS()
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	httpsCheckRoots = pool
	httpsCheckDial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	defer func() { httpsCheckRoots, httpsCheckDial = nil, nil }()
	ctx := context.Background()

	if chk := httpsCheck(ctx, "example.com", ""); !chk.OK || chk.Status != 404 || chk.Path != "" {
		t.Fatalf("root: %+v", chk)
	}
	for _, c := range []struct {
		path   string
		status int
		ok     bool
	}{
		{"/s/200", 200, true}, {"/s/302", 302, true}, {"/s/401", 401, true}, {"/s/403", 403, true},
		{"/s/404", 404, false}, {"/s/500", 500, false},
	} {
		chk := httpsCheck(ctx, "example.com", c.path)
		if chk.OK != c.ok || chk.Status != c.status || chk.Path != c.path {
			t.Fatalf("path %q: %+v, want ok=%v status=%d", c.path, chk, c.ok, c.status)
		}
		if !chk.OK && chk.Error != strconv.Itoa(c.status)+" "+http.StatusText(c.status) {
			t.Fatalf("failed check keeps the status as the error: %+v", chk)
		}
		if chk.CertNames[0] != "example.com" {
			t.Fatalf("cert of %q lost: %+v", c.path, chk)
		}
	}
}
