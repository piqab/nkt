package edge

import (
	"context"
	"net"
	"testing"
)

func TestRunProbe(t *testing.T) {
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
