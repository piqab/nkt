package main

import (
	"net"
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
