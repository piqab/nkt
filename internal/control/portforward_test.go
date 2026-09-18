package control

import (
	"strings"
	"testing"
)

func TestPortForwardScript(t *testing.T) {
	s := Script([]PortForward{{Name: "k8s-cp-1", IP: "192.168.122.10", Ports: []int{6443, 80}}})
	for _, want := range []string{
		"iptables -t nat -N NKT-PF 2>/dev/null || iptables -t nat -F NKT-PF",
		"iptables -t nat -I PREROUTING 1 -j NKT-PF",
		"iptables -I FORWARD 1 -j NKT-PF",
		"--dport 6443 -j DNAT --to-destination 192.168.122.10:6443",
		"-d 192.168.122.10 --dport 80 -j ACCEPT",
		"ip_forward=1",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("нет %q", want)
		}
	}
	if !strings.HasPrefix(Script(nil), "#!/bin/sh") {
		t.Error("пустой список — всё равно скрипт, который чистит цепочки")
	}
}
