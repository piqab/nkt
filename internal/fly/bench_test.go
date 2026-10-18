package fly

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// Замеры маршрутизатора (маршруты по имени) на loopback этой машины:
// go test ./internal/fly -run x -bench Router -benchtime 3s
// Шифрование WireGuard и сеть VPS сюда не входят — это верхняя граница
// самого маршрутизатора.

// streamBackend — HTTP-бэкенд, отдающий size байт.
func streamBackend(b *testing.B, size int) string {
	payload := []byte(strings.Repeat("x", 64<<10))
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(size))
		for left := size; left > 0; left -= len(payload) {
			n := len(payload)
			if left < n {
				n = left
			}
			_, _ = w.Write(payload[:n])
		}
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	go srv.Serve(ln)
	b.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func benchRuntime(b *testing.B, r Route, backend string) (*Runtime, string) {
	pub := map[int]string{}
	var mu sync.Mutex
	rt := NewRuntime()
	rt.Listen = func(port int) (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			mu.Lock()
			pub[port] = ln.Addr().String()
			mu.Unlock()
		}
		return ln, err
	}
	rt.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, backend)
	}
	cert := selfSigned(b)
	rt.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }
	r.Targets = []Target{{ID: "t", Host: "10.77.1.2", Port: 40001}}
	cfg := GatewayConfig{Iface: "nkt-fly", Address: "10.77.1.1/24", ListenPort: 51820,
		Peers: []Peer{{Name: "h", PublicKey: keyA, Address: "10.77.1.2"}}, Routes: []Route{r}}
	if err := rt.Apply(cfg); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(rt.Close)
	return rt, pub[r.Port]
}

// BenchmarkRouterThroughputHTTP — поток через маршрут HTTP по имени
// (маршрутизатор только копирует байты, как и у HTTPS без расшифровки).
func BenchmarkRouterThroughputHTTP(b *testing.B) {
	const size = 64 << 20
	_, addr := benchRuntime(b, Route{ID: "app", Kind: KindHTTP, Names: []string{"app.example.com"}, Port: 80, Algorithm: AlgoRoundRobin, Check: Check{Kind: CheckNone}}, streamBackend(b, size))
	cl := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return net.Dial("tcp", addr) }}}
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := cl.Get("http://app.example.com/")
		if err != nil {
			b.Fatal(err)
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if n != size {
			b.Fatalf("got %d", n)
		}
	}
}

// BenchmarkRouterThroughputGatewayTLS — поток с сертификатом на шлюзе
// (TLS расшифровывает шлюз, цели — HTTP).
func BenchmarkRouterThroughputGatewayTLS(b *testing.B) {
	const size = 64 << 20
	_, addr := benchRuntime(b, Route{ID: "app", Kind: KindHTTPS, Names: []string{"app.example.com"}, Port: 443, TLS: true, Algorithm: AlgoLeastConn, Check: Check{Kind: CheckNone}}, streamBackend(b, size))
	cl := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return net.Dial("tcp", addr) },
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "app.example.com"}}}
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := cl.Get("https://app.example.com/")
		if err != nil {
			b.Fatal(err)
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if n != size {
			b.Fatalf("got %d", n)
		}
	}
}

// BenchmarkRouterConnsHTTP — новых соединений в секунду (каждый запрос —
// своё соединение), параллельно.
func BenchmarkRouterConnsHTTP(b *testing.B) {
	_, addr := benchRuntime(b, Route{ID: "app", Kind: KindHTTP, Names: []string{"app.example.com"}, Port: 80, Algorithm: AlgoRoundRobin, Check: Check{Kind: CheckNone}}, streamBackend(b, 128))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		cl := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return net.Dial("tcp", addr) }}}
		for pb.Next() {
			resp, err := cl.Get("http://app.example.com/")
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}

// BenchmarkRouterConnsGatewayTLS — новых соединений в секунду с
// рукопожатием TLS на шлюзе (ECDSA P-256), параллельно.
func BenchmarkRouterConnsGatewayTLS(b *testing.B) {
	_, addr := benchRuntime(b, Route{ID: "app", Kind: KindHTTPS, Names: []string{"app.example.com"}, Port: 443, TLS: true, Algorithm: AlgoLeastConn, Check: Check{Kind: CheckNone}}, streamBackend(b, 128))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		cl := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true,
			DialContext:     func(ctx context.Context, _, _ string) (net.Conn, error) { return net.Dial("tcp", addr) },
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "app.example.com"}}}
		for pb.Next() {
			resp, err := cl.Get("https://app.example.com/")
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}
