package fly

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Маршрут HTTPS с сертификатом на шлюзе: шлюз сам завершает TLS и отдаёт
// цели обычный HTTP — с адресом клиента в X-Forwarded-For и X-Real-IP.
// Соединение клиента обслуживает свой http.Server (одно соединение —
// один сервер), поэтому keep-alive, HTTP/1.1 и WebSocket работают как у
// обычного обратного прокси.

// tlsName — у имени есть маршрут с сертификатом на шлюзе.
func (rt *Runtime) tlsName(name string) bool {
	_, ok := rt.tlsNameOf(name)
	return ok
}

// tlsNameOf — имя из конфигурации шлюза, совпавшее с name.
func (rt *Runtime) tlsNameOf(name string) (string, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for _, r := range rt.cfg.Routes {
		if !r.TLS {
			continue
		}
		for _, n := range r.Names {
			if n == name {
				return n, true
			}
		}
	}
	return "", false
}

// acmeRequest — в начале соединения запрос проверки Let's Encrypt.
func acmeRequest(br *bufio.Reader) bool {
	b, _ := br.Peek(br.Buffered())
	return bytes.HasPrefix(b, []byte("GET /.well-known/acme-challenge/")) || bytes.HasPrefix(b, []byte("GET "+CheckPath80+" "))
}

// bufConn — соединение клиента, у которого начало уже прочитано в br;
// закрытие сообщает oneConnListener, что можно заканчивать.
type bufConn struct {
	net.Conn
	r      *bufio.Reader
	in     *atomic.Int64
	out    *atomic.Int64
	mu     sync.Mutex
	closed chan struct{}
}

func (c *bufConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if c.in != nil {
		c.in.Add(int64(n))
	}
	return n, err
}

func (c *bufConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if c.out != nil {
		c.out.Add(int64(n))
	}
	return n, err
}

func (c *bufConn) Close() error {
	c.mu.Lock()
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	c.mu.Unlock()
	return c.Conn.Close()
}

// oneConnListener отдаёт http.Server одно соединение, а следующий Accept
// ждёт его закрытия — тогда Serve возвращается.
type oneConnListener struct {
	conn   net.Conn
	done   <-chan struct{}
	mu     sync.Mutex
	served bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.served {
		l.served = true
		l.mu.Unlock()
		return l.conn, nil
	}
	l.mu.Unlock()
	<-l.done
	return nil, io.EOF
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

var quietLog = log.New(io.Discard, "", 0)

// acmeALPN — протокол проверки Let's Encrypt по порту 443 (tls-alpn-01).
const acmeALPN = "acme-tls/1"

// serveOne — обслужить одно соединение http.Server'ом.
func serveOne(conn net.Conn, done <-chan struct{}, h http.Handler) {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, ErrorLog: quietLog}
	_ = srv.Serve(&oneConnListener{conn: conn, done: done})
}

// serveRedirect — порт 80 имени с сертификатом на шлюзе: проверки Let's
// Encrypt и перенаправление на https.
func (rt *Runtime) serveRedirect(c net.Conn, br *bufio.Reader) {
	bc := &bufConn{Conn: c, r: br, closed: make(chan struct{})}
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == CheckPath80 {
			_, _ = io.WriteString(w, CheckReply80)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		// Имя для адреса — из конфигурации шлюза, а не из запроса:
		// перенаправить можно только на своё имя.
		name, ok := rt.tlsNameOf(host)
		if !ok {
			http.Error(w, "421 Misdirected Request", http.StatusMisdirectedRequest)
			return
		}
		u := url.URL{Scheme: "https", Host: name, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
		http.Redirect(w, r, u.String(), http.StatusPermanentRedirect)
	})
	var h http.Handler = redirect
	if rt.ACME != nil {
		h = rt.ACME(redirect)
	}
	serveOne(bc, bc.closed, h)
}

// serveTLS — расшифровать соединение и проксировать запросы к целям
// маршрута по HTTP. order — цели по алгоритму: первая, что примет
// соединение, обслуживает и следующие запросы этого клиента.
func (rt *Runtime) serveTLS(c net.Conn, br *bufio.Reader, r Route, rc *routeCounters, client netip.AddrPort, order []Target) {
	if rt.GetCertificate == nil {
		rc.failed.Add(1)
		return
	}
	var in, out atomic.Int64
	bc := &bufConn{Conn: c, r: br, in: &in, out: &out, closed: make(chan struct{})}
	// acme-tls/1 — проверка Let's Encrypt по порту 443 (tls-alpn-01):
	// сертификат проверки отдаёт GetCertificate, а соединение после
	// рукопожатия просто закрывается.
	tc := tls.Server(bc, &tls.Config{GetCertificate: rt.GetCertificate, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1", acmeALPN}})
	_ = c.SetDeadline(time.Now().Add(peekTimeout))
	if err := tc.Handshake(); err != nil {
		rc.failed.Add(1)
		_ = c.Close()
		return
	}
	_ = c.SetDeadline(time.Time{})
	if tc.ConnectionState().NegotiatedProtocol == acmeALPN {
		_ = tc.Close()
		return
	}
	if !rc.admit(r, client.Addr()) {
		_ = tc.Close()
		return
	}

	ip := client.Addr().Unmap().String()
	var (
		usedMu sync.Mutex
		used   *targetState
		target string
	)
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		usedMu.Lock()
		t := target
		usedMu.Unlock()
		if t != "" {
			return rt.Dial(ctx, "tcp", t)
		}
		var last error = errors.New("fly: no live targets")
		for i, tg := range order {
			if i == 3 {
				break
			}
			addr := net.JoinHostPort(tg.Host, strconv.Itoa(tg.Port))
			dctx, cancel := context.WithTimeout(ctx, dialTimeout)
			up, err := rt.Dial(dctx, "tcp", addr)
			cancel()
			if err != nil {
				last = err
				continue
			}
			rt.mu.RLock()
			ts := rt.health[key(r.ID, tg.ID)]
			rt.mu.RUnlock()
			usedMu.Lock()
			if target == "" && ts != nil {
				target, used = addr, ts
				ts.active.Add(1)
				ts.total.Add(1)
			}
			usedMu.Unlock()
			return up, nil
		}
		return nil, last
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: "fly-target"})
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Real-IP", ip)
		},
		Transport: &http.Transport{DialContext: dial, MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Minute,
			ResponseHeaderTimeout: 5 * time.Minute},
		ErrorLog: quietLog,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			rc.failed.Add(1)
			http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		},
		FlushInterval: -1,
	}

	rc.active.Add(1)
	rc.mu.Lock()
	rc.clients[ip]++
	rc.mu.Unlock()
	serveOne(tc, bc.closed, rp)
	rc.active.Add(-1)
	rc.bytesIn.Add(in.Load())
	rc.bytesOut.Add(out.Load())
	rc.mu.Lock()
	if rc.clients[ip]--; rc.clients[ip] <= 0 {
		delete(rc.clients, ip)
	}
	rc.mu.Unlock()
	usedMu.Lock()
	if used != nil {
		used.active.Add(-1)
	}
	usedMu.Unlock()
	if tr, ok := rp.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}
