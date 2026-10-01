package edge

import (
	"context"
	"net"
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
	for _, c := range []ProbeCheck{{Type: "tcp", Host: "a", Port: 0}, {Type: "dns", Host: "a", Port: 5}, {Type: "https", Host: "a/b"}} {
		if ValidProbe(c) {
			t.Errorf("accepted %+v", c)
		}
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
