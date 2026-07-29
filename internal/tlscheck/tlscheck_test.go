package tlscheck

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestFetchMatchesServerCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	sum := sha256.Sum256(srv.Certificate().Raw)
	want := hex.EncodeToString(sum[:])

	got, err := Fetch(context.Background(), srv.Listener.Addr().String(), "", 3*time.Second)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Fingerprint != want {
		t.Errorf("отпечаток = %s, ожидался %s", got.Fingerprint, want)
	}
	if got.Serial != srv.Certificate().SerialNumber.String() {
		t.Errorf("серийный номер = %s, ожидался %s", got.Serial, srv.Certificate().SerialNumber.String())
	}
	if !got.NotAfter.Equal(srv.Certificate().NotAfter.UTC()) {
		t.Errorf("NotAfter = %v, ожидался %v", got.NotAfter, srv.Certificate().NotAfter.UTC())
	}
}

// SNI must actually reach the server: this is what lets the caller pick the
// right certificate when several are multiplexed on one socket.
func TestFetchSendsServerName(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	var mu sync.Mutex
	var gotSNI string
	base := srv.TLS
	srv.TLS = &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			mu.Lock()
			gotSNI = hello.ServerName
			mu.Unlock()
			return nil, nil // nil keeps httptest's own certificate handling
		},
	}
	if base != nil {
		srv.TLS.Certificates = base.Certificates
	}
	srv.StartTLS()
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Listener.Addr().String(), "custom.example.com", 3*time.Second); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotSNI != "custom.example.com" {
		t.Errorf("сервер увидел SNI %q, ожидалось custom.example.com", gotSNI)
	}
}

func TestFetchReportsDialError(t *testing.T) {
	// Port 1 is reserved and nothing listens there.
	if _, err := Fetch(context.Background(), "127.0.0.1:1", "", 300*time.Millisecond); err == nil {
		t.Error("ожидалась ошибка подключения к закрытому порту")
	}
}
