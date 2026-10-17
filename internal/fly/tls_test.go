package fly

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Сертификат на шлюзе: TLS завершает шлюз, цели уходит обычный HTTP с
// адресом клиента в заголовках; порт 80 отдаёт проверки Let's Encrypt и
// перенаправляет остальное на https.
func TestRouterGatewayTLS(t *testing.T) {
	echo := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s|%s|%v", r.Host, r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-Proto"), r.TLS != nil)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go echo.Serve(ln)
	t.Cleanup(func() { echo.Close() })

	n := &testNet{public: map[int]string{}, backends: map[string]string{"10.77.1.2:40001": ln.Addr().String()}}
	rt := newRuntime(t, n)
	cert := selfSigned(t)
	asked := ""
	rt.GetCertificate = func(h *tls.ClientHelloInfo) (*tls.Certificate, error) { asked = h.ServerName; return &cert, nil }
	rt.ACME = func(fallback http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
				io.WriteString(w, "token-"+r.Host)
				return
			}
			fallback.ServeHTTP(w, r)
		})
	}
	err = rt.Apply(cfgFor(Route{ID: "app", Kind: KindHTTPS, Names: []string{"app.example.com"}, Port: 443, TLS: true, Algorithm: AlgoLeastConn,
		Check: Check{Kind: CheckNone}, Targets: []Target{{ID: "t", Host: "10.77.1.2", Port: 40001}}}))
	if err != nil {
		t.Fatal(err)
	}
	got := get(t, n.public[443], "app.example.com", true)
	if got != "app.example.com|127.0.0.1|127.0.0.1|https|false" || asked != "app.example.com" {
		t.Fatalf("через шлюз: %q (сертификат для %q)", got, asked)
	}

	// Порт 80: проверка Let's Encrypt — шлюзу, остальное — на https.
	cl := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return net.Dial("tcp", n.public[80]) }}}
	resp, err := cl.Get("http://app.example.com/.well-known/acme-challenge/abc")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "token-app.example.com" {
		t.Fatalf("acme: %q", b)
	}
	resp, err = cl.Get("http://app.example.com/x?y=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "https://app.example.com/x?y=1" {
		t.Fatalf("redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	st := rt.Snapshot()
	if st.Routes[0].Total == 0 || st.Routes[0].BytesOut == 0 {
		t.Fatalf("stats: %+v", st.Routes[0])
	}
}

func TestGatewayTLSValidate(t *testing.T) {
	base := Route{ID: "app", Kind: KindHTTPS, Names: []string{"app.example.com"}, Port: 443, TLS: true, Algorithm: AlgoLeastConn,
		Check: Check{Kind: CheckNone}, Targets: []Target{{ID: "t", Host: "10.77.1.2", Port: 40001}}}
	if err := cfgFor(base).Validate(); err != nil {
		t.Fatal(err)
	}
	wild := base
	wild.Names = []string{"*.example.com"}
	pp := base
	pp.ProxyProtocol = true
	httpTLS := base
	httpTLS.Kind, httpTLS.Port = KindHTTP, 80
	port80 := Route{ID: "raw80", Kind: KindTCP, Port: 80, Algorithm: AlgoRoundRobin, Check: Check{Kind: CheckNone},
		Targets: []Target{{ID: "t", Host: "10.77.1.3", Port: 40002}}}
	for name, c := range map[string]GatewayConfig{"wildcard": cfgFor(wild), "proxy": cfgFor(pp), "http": cfgFor(httpTLS), "port80": cfgFor(base, port80)} {
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Проверка Let's Encrypt по порту 443 (tls-alpn-01): рукопожатие с
// acme-tls/1 проходит, а до цели соединение не доходит.
func TestRouterGatewayTLSALPN(t *testing.T) {
	hits := 0
	echo := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ })}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go echo.Serve(ln)
	t.Cleanup(func() { echo.Close() })
	n := &testNet{public: map[int]string{}, backends: map[string]string{"10.77.1.2:40001": ln.Addr().String()}}
	rt := newRuntime(t, n)
	cert := selfSigned(t)
	rt.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }
	if err := rt.Apply(cfgFor(Route{ID: "app", Kind: KindHTTPS, Names: []string{"app.example.com"}, Port: 443, TLS: true, Algorithm: AlgoLeastConn,
		Check: Check{Kind: CheckNone}, Targets: []Target{{ID: "t", Host: "10.77.1.2", Port: 40001}}})); err != nil {
		t.Fatal(err)
	}
	c, err := tls.Dial("tcp", n.public[443], &tls.Config{InsecureSkipVerify: true, ServerName: "app.example.com", NextProtos: []string{"acme-tls/1"}})
	if err != nil {
		t.Fatal(err)
	}
	if p := c.ConnectionState().NegotiatedProtocol; p != "acme-tls/1" {
		t.Fatalf("alpn: %q", p)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection stayed open")
	}
	c.Close()
	if hits != 0 {
		t.Fatal("challenge reached the target")
	}
}
