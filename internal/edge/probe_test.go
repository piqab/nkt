package edge

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestRunProbe(t *testing.T) {
	probeAllowPrivate = true
	defer func() { probeAllowPrivate = false }()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	open := l.Addr().(*net.TCPAddr).Port
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()
	res := RunProbe(context.Background(), ProbeRequest{Checks: []ProbeCheck{
		{Type: "tcp", Host: "127.0.0.1", Port: open},
		{Type: "tcp", Host: "127.0.0.1", Port: closedPort},
		{Type: "dns", Host: "localhost"},
		{Type: "tcp", Host: "bad host; rm -rf /", Port: 22},
		{Type: "exec", Host: "x"},
	}})
	l.Close()
	r := res.Results
	if r[0].State != "open" || r[1].State != "refused" {
		t.Fatalf("tcp: %+v %+v", r[0], r[1])
	}
	if len(r[2].IPs) == 0 {
		t.Fatalf("dns: %+v", r[2])
	}
	if r[3].Error != "invalid check" || r[4].Error != "invalid check" {
		t.Fatalf("invalid: %+v %+v", r[3], r[4])
	}
	for _, c := range []ProbeCheck{{Type: "tcp", Host: "a", Port: 0}, {Type: "dns", Host: "a", Port: 5}, {Type: "https", Host: "a/b"},
		{Type: "https", Host: "a", Path: "ui"}, {Type: "https", Host: "a", Path: "//evil.example.com/x"},
		{Type: "https", Host: "a", Path: "/a b"}, {Type: "https", Host: "a", Path: "?x=1"}, {Type: "https", Host: "a", Path: "#top"},
		{Type: "https", Host: "a", Path: "/" + strings.Repeat("x", 255)}} {
		if ValidProbe(c) {
			t.Errorf("accepted %+v", c)
		}
	}
	for _, c := range []ProbeCheck{{Type: "https", Host: "a", Path: "/ui"}, {Type: "https", Host: "a", Path: "/ui/login"},
		{Type: "https", Host: "a", Path: "/a@b/c.d"}, {Type: "https", Host: "a", Path: "/%D0%BA/DASH_1-2~3"}} {
		if !ValidProbe(c) {
			t.Errorf("rejected %+v", c)
		}
	}
}

// Путь https-проверки доезжает до сайта: edge запрашивает именно его, а не
// корень — у сервиса с веб-мордой не в корне корень часто 404, и проверка по
// нему ничего не доказывает. Пусто — прежний корень «/». Ответ возвращает
// путь обратно: по эху хаб отличает edge прежней версии, который поля не
// знает и отвечает пусто (значит проверен корень, и судить надо по нему).
func TestProbeHTTPSCheckPath(t *testing.T) {
	asked := make(chan string, 8)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked <- r.URL.Path
		if r.URL.Path == "/ui" {
			// Интерфейс жив и просит вход — хаб считает это открытым путём.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	srv.StartTLS()
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	probeRoots = pool
	probeAllowPrivate = true
	defer func() { probeRoots, probeAllowPrivate = nil, false }()
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	res := RunProbe(context.Background(), ProbeRequest{Checks: []ProbeCheck{
		{Type: "https", Host: "127.0.0.1", Port: port, Path: "/ui"},
		{Type: "https", Host: "127.0.0.1", Port: port},
		{Type: "https", Host: "127.0.0.1", Port: port, Path: "ui"},
		{Type: "https", Host: "127.0.0.1", Port: port, Path: "//evil.example.com/x"},
	}})
	close(asked)
	seen := map[string]bool{}
	for p := range asked {
		seen[p] = true
	}
	if len(seen) != 2 || !seen["/ui"] || !seen["/"] {
		t.Fatalf("requests reached the site: %+v", seen)
	}
	r := res.Results
	if r[0].Status != http.StatusUnauthorized || r[0].Path != "/ui" || r[0].Error != "" {
		t.Fatalf("path /ui: %+v", r[0])
	}
	if r[1].Status != http.StatusOK || r[1].Path != "" || r[1].Error != "" {
		t.Fatalf("root: %+v", r[1])
	}
	if r[2].Error != "invalid check" || r[3].Error != "invalid check" {
		t.Fatalf("bad paths accepted: %+v %+v", r[2], r[3])
	}
}

// В сеть самого VPS проверки не ходят: loopback, частные адреса,
// link-local (метаданные облака) — отказ до соединения.
func TestProbeBlocksPrivate(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	res := RunProbe(context.Background(), ProbeRequest{Checks: []ProbeCheck{
		{Type: "tcp", Host: "127.0.0.1", Port: port},
		{Type: "tcp", Host: "localhost", Port: port},
		{Type: "https", Host: "169.254.169.254", Port: 80},
	}})
	for i, r := range res.Results {
		if !strings.Contains(r.State+r.Error, "not a public address") {
			t.Errorf("check %d reached a private address: %+v", i, r)
		}
	}
	for addr, want := range map[string]bool{"8.8.8.8": true, "10.0.0.1": false, "192.168.1.1": false, "100.64.0.5": false,
		"169.254.169.254": false, "::1": false, "fd00::1": false, "2606:4700::1111": true, "::ffff:10.0.0.1": false} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}
