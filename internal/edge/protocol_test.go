package edge

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func startEdge(t *testing.T, token string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	cert, _, err := TunnelCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, _ := os.ReadFile(filepath.Join(dir, "tunnel.crt"))
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				if Accept(c, token) != nil {
					c.Close()
				}
			}(c)
		}
	}()
	return l.Addr().String(), string(pemBytes)
}

func TestHandshake(t *testing.T) {
	token := "t0123456789abcdef0123456789abcdef"
	addr, certPEM := startEdge(t, token)
	fp, err := CertInfo(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	conn, seen, err := Dial(addr, token, certPEM, 5*time.Second)
	if err != nil || seen != fp {
		t.Fatalf("свой сертификат: %v %s", err, seen)
	}
	conn.Close()
	if _, _, err := Dial(addr, token+"x", certPEM, 5*time.Second); !errors.Is(err, ErrRejected) {
		t.Errorf("чужой токен: %v", err)
	}
	_, otherPEM := startEdge(t, token)
	if _, _, err := Dial(addr, token, otherPEM, 5*time.Second); err == nil {
		t.Error("чужой сертификат принят")
	}
	if _, _, err := Dial(addr, token, "", 5*time.Second); err == nil {
		t.Error("без сертификата принят")
	}
}

func TestTunnelCertStable(t *testing.T) {
	dir := t.TempDir()
	_, a, err := TunnelCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, b, _ := TunnelCert(dir)
	if a != b {
		t.Error("отпечаток меняется между запусками")
	}
	// Сертификат прежнего вида (не PEM с именем nkt-edge) пересоздаётся.
	_ = os.WriteFile(filepath.Join(dir, "tunnel.crt"), []byte("old"), 0o644)
	_, c, err := TunnelCert(dir)
	if err != nil || c == a {
		t.Errorf("пересоздание: %v", err)
	}
}
