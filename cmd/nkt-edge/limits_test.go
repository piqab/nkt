package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	l := newLimiter(3)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.allow("1.1.1.1", now) {
			t.Fatal("рано отказал")
		}
	}
	if l.allow("1.1.1.1", now) {
		t.Error("пропустил сверх предела")
	}
	if !l.allow("2.2.2.2", now) {
		t.Error("другой адрес не пропущен")
	}
	if !l.allow("1.1.1.1", now.Add(61*time.Second)) {
		t.Error("через минуту не пропустил")
	}
}

func TestGithubNets(t *testing.T) {
	_, n, _ := net.ParseCIDR("192.30.252.0/22")
	g := &githubNets{nets: []*net.IPNet{n}}
	if !g.contains(net.ParseIP("192.30.252.10")) || g.contains(net.ParseIP("8.8.8.8")) || g.contains(nil) {
		t.Error("адреса GitHub")
	}
}

// За прокси адрес отправителя берётся из заголовков прокси, но только
// если запрос пришёл с loopback; без режима прокси заголовкам не верим.
func TestClientIPBehindProxy(t *testing.T) {
	req := func(remote string, h map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/hooks/x", nil)
		r.RemoteAddr = remote
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	direct := &server{}
	if got := direct.clientIP(req("203.0.113.5:4000", map[string]string{"X-Real-IP": "140.82.112.1"})); got != "203.0.113.5" {
		t.Errorf("без прокси: %s", got)
	}
	proxied := &server{behindProxy: true}
	for _, c := range []struct {
		remote string
		h      map[string]string
		want   string
	}{
		{"127.0.0.1:5000", map[string]string{"X-Real-IP": "140.82.112.1"}, "140.82.112.1"},
		{"127.0.0.1:5000", map[string]string{"X-Forwarded-For": "1.2.3.4, 140.82.112.2"}, "140.82.112.2"},
		{"[::1]:5000", map[string]string{"X-Real-IP": "garbage"}, "::1"},
		{"198.51.100.7:5000", map[string]string{"X-Real-IP": "140.82.112.1"}, "198.51.100.7"},
	} {
		if got := proxied.clientIP(req(c.remote, c.h)); got != c.want {
			t.Errorf("%s %v: %s, want %s", c.remote, c.h, got, c.want)
		}
	}
}
