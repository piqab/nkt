package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/model"
)

func TestCheckLiveCertificatesMatch(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	_, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("разбор адреса сервера: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("порт не число: %v", err)
	}

	sum := sha256.Sum256(srv.Certificate().Raw)
	fingerprint := hex.EncodeToString(sum[:])

	endpoints := []model.Endpoint{{
		Address: "127.0.0.1", Port: port,
		Extra: map[string]string{"ssl_certificate": "/fake/cert.pem"},
	}}

	certs := []model.Certificate{{Path: "/fake/cert.pem", Fingerprint: fingerprint}}
	checkLiveCertificates(context.Background(), certs, endpoints, 3*time.Second)

	if !certs[0].Serving.Checked {
		t.Fatal("проверка должна была выполниться")
	}
	if certs[0].Serving.Error != "" {
		t.Fatalf("неожиданная ошибка: %s", certs[0].Serving.Error)
	}
	if !certs[0].Serving.Match {
		t.Error("отпечатки совпадают, но Match=false")
	}
	if certs[0].Serving.ServedSerial != srv.Certificate().SerialNumber.String() {
		t.Errorf("серийный номер = %s, ожидался %s", certs[0].Serving.ServedSerial,
			srv.Certificate().SerialNumber.String())
	}
}

// This is the exact scenario the rule exists for: certbot renews the file, the
// service keeps serving the old certificate until reloaded.
func TestCheckLiveCertificatesMismatch(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	_, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	endpoints := []model.Endpoint{{
		Address: "0.0.0.0", Port: port, // 0.0.0.0 must resolve to a dialable loopback
		Extra: map[string]string{"ssl_certificate": "/fake/cert.pem"},
	}}
	certs := []model.Certificate{{Path: "/fake/cert.pem", Fingerprint: strings.Repeat("0", 64)}}
	checkLiveCertificates(context.Background(), certs, endpoints, 3*time.Second)

	if !certs[0].Serving.Checked || certs[0].Serving.Error != "" {
		t.Fatalf("ожидался успешный дозвон, получено %+v", certs[0].Serving)
	}
	if certs[0].Serving.Match {
		t.Error("отпечатки разные, Match не должен быть true")
	}
}

func TestCheckLiveCertificatesUnreachable(t *testing.T) {
	endpoints := []model.Endpoint{{
		Address: "127.0.0.1", Port: 1, // reserved, nothing listens there
		Extra: map[string]string{"ssl_certificate": "/fake/cert.pem"},
	}}
	certs := []model.Certificate{{Path: "/fake/cert.pem", Fingerprint: "whatever"}}
	checkLiveCertificates(context.Background(), certs, endpoints, 500*time.Millisecond)

	if !certs[0].Serving.Checked || certs[0].Serving.Error == "" {
		t.Errorf("ожидалась зафиксированная ошибка подключения, получено %+v", certs[0].Serving)
	}
}

// A certificate whose file could not even be parsed has no fingerprint to
// compare against, and no endpoint tells us where to dial in the first place
// for endpoints that reference a different path — the check must not run.
func TestCheckLiveCertificatesSkipsBrokenOrUnreferenced(t *testing.T) {
	certs := []model.Certificate{
		{Path: "/broken.pem", Error: "файл недоступен"},
		{Path: "/unreferenced.pem"},
	}
	checkLiveCertificates(context.Background(), certs, nil, time.Second)

	for _, c := range certs {
		if c.Serving.Checked {
			t.Errorf("%s: проверка не должна была запускаться", c.Path)
		}
	}
}
