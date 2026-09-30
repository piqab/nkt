package hub

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
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

	chk := httpsCheck(context.Background(), "hb.example.org")
	if chk.OK || !chk.WrongCert || len(chk.CertNames) == 0 || chk.CertNames[0] != "example.com" {
		t.Fatalf("wrong cert not detected: %+v", chk)
	}
	chk = httpsCheck(context.Background(), "example.com")
	if !chk.OK || chk.WrongCert {
		t.Fatalf("right cert: %+v", chk)
	}
}
