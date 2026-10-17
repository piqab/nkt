package fly

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// testNet — маршрутизатор на 127.0.0.1: порт маршрута → настоящий порт,
// адрес цели в туннеле → настоящий адрес бэкенда.
type testNet struct {
	mu       sync.Mutex
	public   map[int]string
	backends map[string]string
}

func newRuntime(t *testing.T, n *testNet) *Runtime {
	rt := NewRuntime()
	rt.Listen = func(port int) (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			n.mu.Lock()
			n.public[port] = ln.Addr().String()
			n.mu.Unlock()
		}
		return ln, err
	}
	rt.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		n.mu.Lock()
		real, ok := n.backends[addr]
		n.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("no route to %s", addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, real)
	}
	t.Cleanup(rt.Close)
	return rt
}

// proxyBackend — бэкенд, который читает заголовок PROXY v2 и отвечает
// своим именем и адресом клиента из заголовка.
func proxyBackend(t *testing.T, name string, useTLS bool) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	cert := selfSigned(t)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hdr := make([]byte, 16)
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				body := make([]byte, binary.BigEndian.Uint16(hdr[14:16]))
				io.ReadFull(c, body)
				src := net.IP(body[0:4]).String()
				var conn net.Conn = c
				if useTLS {
					tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
					if tc.Handshake() != nil {
						return
					}
					conn = tc
				}
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				msg := name + " " + src + " " + req.Host
				fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(msg), msg)
			}(c)
		}
	}()
	return ln.Addr().String()
}

// plainBackend — HTTP-бэкенд без PROXY: отвечает своим именем.
func plainBackend(t *testing.T, name string) (string, func()) {
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, name) })}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	stop := func() { srv.Close() }
	t.Cleanup(stop)
	return ln.Addr().String(), stop
}

func selfSigned(t *testing.T) tls.Certificate {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "app.example.com"}, DNSNames: []string{"app.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}

func get(t *testing.T, addr, host string, useTLS bool) string {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return net.Dial("tcp", addr) },
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: host}, DisableKeepAlives: true}
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	cl := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := cl.Get(scheme + "://" + host + "/")
	if err != nil {
		return "ERR " + err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Sprint(resp.StatusCode)
	}
	return string(b)
}

func cfgFor(routes ...Route) GatewayConfig {
	return GatewayConfig{Iface: "nkt-fly", Address: "10.77.1.1/24", ListenPort: 51820,
		Peers:  []Peer{{Name: "h1", PublicKey: keyA, Address: "10.77.1.2"}, {Name: "h2", PublicKey: keyB, Address: "10.77.1.3"}},
		Routes: routes}
}

func TestRouterSNIAndProxy(t *testing.T) {
	n := &testNet{public: map[int]string{}, backends: map[string]string{
		"10.77.1.2:40001": proxyBackend(t, "app", true),
		"10.77.1.3:40001": proxyBackend(t, "wild", true),
	}}
	rt := newRuntime(t, n)
	err := rt.Apply(cfgFor(
		Route{ID: "app", Kind: KindHTTPS, Names: []string{"app.example.com"}, Port: 443, ProxyProtocol: true, Algorithm: AlgoLeastConn, Check: Check{Kind: CheckNone},
			Targets: []Target{{ID: "t", Host: "10.77.1.2", Port: 40001}}},
		Route{ID: "wild", Kind: KindHTTPS, Names: []string{"*.example.com"}, Port: 443, ProxyProtocol: true, Algorithm: AlgoRoundRobin, Check: Check{Kind: CheckNone},
			Targets: []Target{{ID: "t", Host: "10.77.1.3", Port: 40001}}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, n.public[443], "app.example.com", true); got != "app 127.0.0.1 app.example.com" {
		t.Fatalf("точное имя: %q", got)
	}
	if got := get(t, n.public[443], "git.example.com", true); got != "wild 127.0.0.1 git.example.com" {
		t.Fatalf("шаблон: %q", got)
	}
	if got := get(t, n.public[443], "other.org", true); !strings.HasPrefix(got, "ERR") {
		t.Fatalf("чужое имя прошло: %q", got)
	}
	st := rt.Snapshot()
	if st.Routes[0].Total != 1 || st.Routes[1].Total != 1 || st.Routes[0].BytesOut == 0 {
		t.Fatalf("%+v", st)
	}
}

func TestRouterHTTPBalanceAndHealth(t *testing.T) {
	a, stopA := plainBackend(t, "A")
	b, _ := plainBackend(t, "B")
	c, _ := plainBackend(t, "C")
	n := &testNet{public: map[int]string{}, backends: map[string]string{"10.77.1.2:40003": a, "10.77.1.3:40003": b, "10.77.1.2:40004": c}}
	rt := newRuntime(t, n)
	changed := make(chan struct{}, 8)
	rt.OnHealthChange = func() { changed <- struct{}{} }
	err := rt.Apply(cfgFor(Route{ID: "web", Kind: KindHTTP, Names: []string{"web.example.com"}, Port: 80, Algorithm: AlgoRoundRobin,
		Check:   Check{Kind: CheckHTTP, Path: "/", IntervalS: 1},
		Targets: []Target{{ID: "a", Host: "10.77.1.2", Port: 40003}, {ID: "b", Host: "10.77.1.3", Port: 40003}, {ID: "c", Host: "10.77.1.2", Port: 40004, Drain: true}}}))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		seen[get(t, n.public[80], "web.example.com", false)]++
	}
	if seen["A"] != 3 || seen["B"] != 3 {
		t.Fatalf("по кругу, без цели в «сливе»: %v", seen)
	}
	if got := get(t, n.public[80], "nope.example.com", false); got != "421" {
		t.Fatalf("чужое имя: %q", got)
	}
	// A упала: после трёх неудачных проверок — вне пула.
	stopA()
	deadline := time.After(10 * time.Second)
	for rt.Alive("web", "a") {
		select {
		case <-changed:
		case <-deadline:
			t.Fatal("упавшая цель осталась живой")
		}
	}
	for i := 0; i < 4; i++ {
		if got := get(t, n.public[80], "web.example.com", false); got != "B" {
			t.Fatalf("после падения A: %q", got)
		}
	}
	for _, ts := range rt.Snapshot().Routes[0].Targets {
		if ts.ID == "a" && (ts.Alive || ts.LastError == "") {
			t.Fatalf("%+v", ts)
		}
	}
}

func TestSourceHashSticky(t *testing.T) {
	rt := NewRuntime()
	r := Route{ID: "x", Kind: KindTCP, Port: 22, Algorithm: AlgoSourceHash, Check: Check{Kind: CheckNone},
		Targets: []Target{{ID: "a", Host: "10.77.1.2", Port: 1}, {ID: "b", Host: "10.77.1.3", Port: 1}}}
	if err := rt.Apply(cfgFor(r)); err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	first := rt.pick(r, mustAddr("198.51.100.7"))[0].ID
	for i := 0; i < 10; i++ {
		if rt.pick(r, mustAddr("198.51.100.7"))[0].ID != first {
			t.Fatal("один клиент — разные цели")
		}
	}
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }
