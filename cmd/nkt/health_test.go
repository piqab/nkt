package main

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/tlscert"
)

// nkt health с TLS доверяет только своему сертификату: свой — проходит,
// чужой — нет (проверка не отключена).
func TestHealthTLS(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "tls", "cert.pem"), filepath.Join(dir, "tls", "key.pem")
	if err := tlscert.EnsureSelfSigned(cert, key, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	oc, ok := filepath.Join(other, "tls", "cert.pem"), filepath.Join(other, "tls", "key.pem")
	if err := tlscert.EnsureSelfSigned(oc, ok, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	srv.StartTLS()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("NKT_ADDR", "0.0.0.0:"+port)
	t.Setenv("NKT_TLS_ENABLED", "true")
	t.Setenv("NKT_DATA_DIR", dir)
	if err := runHealth(); err != nil {
		t.Fatalf("own certificate: %v", err)
	}
	t.Setenv("NKT_DATA_DIR", other)
	if err := runHealth(); err == nil {
		t.Fatal("a foreign certificate was trusted")
	}
}
