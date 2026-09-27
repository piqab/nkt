package edge

import (
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

func startEdge(t *testing.T, token string) (string, string) {
	t.Helper()
	cert, fp, err := TunnelCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	return l.Addr().String(), fp
}

func TestHandshake(t *testing.T) {
	token := "t0123456789abcdef0123456789abcdef"
	addr, fp := startEdge(t, token)
	conn, seen, err := Dial(addr, token, fp, 5*time.Second)
	if err != nil || seen != fp {
		t.Fatalf("свой токен и отпечаток: %v %s", err, seen)
	}
	conn.Close()
	if _, _, err := Dial(addr, token+"x", fp, 5*time.Second); !errors.Is(err, ErrRejected) {
		t.Errorf("чужой токен: %v", err)
	}
	if _, _, err := Dial(addr, token, "00"+fp[2:], 5*time.Second); !errors.Is(err, ErrFingerprint) {
		t.Errorf("подменённый сертификат: %v", err)
	}
	if _, seen, err := Dial(addr, token, "", 5*time.Second); err != nil || seen != fp {
		t.Errorf("без закреплённого отпечатка: %v", err)
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
}
