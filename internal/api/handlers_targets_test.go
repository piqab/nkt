package api

import "testing"

func TestManualTarget(t *testing.T) {
	ok := []targetCreateRequest{
		{Kind: "icmp", Host: "10.0.0.5", Port: 99},
		{Kind: "tcp", Host: "db.local", Port: 5432},
		{Kind: "https", Host: "example.com", Port: 443},
		{Kind: "http", Host: "[fd00::1]", Port: 80, Path: "/health"},
	}
	for _, r := range ok {
		tg, good := manualTarget(r)
		if !good || tg.Source != "manual" {
			t.Errorf("отклонено %+v", r)
		}
		if r.Kind == "icmp" && tg.Port != 0 {
			t.Errorf("icmp с портом")
		}
		if r.Kind == "https" && tg.Path != "/" {
			t.Errorf("путь по умолчанию: %q", tg.Path)
		}
	}
	bad := []targetCreateRequest{
		{Kind: "udp", Host: "x", Port: 1},
		{Kind: "tcp", Host: "x", Port: 0},
		{Kind: "tcp", Host: "-bad", Port: 22},
		{Kind: "tcp", Host: "a b", Port: 22},
		{Kind: "http", Host: "x", Port: 80, Path: "nope"},
	}
	for _, r := range bad {
		if _, good := manualTarget(r); good {
			t.Errorf("принято %+v", r)
		}
	}
}
