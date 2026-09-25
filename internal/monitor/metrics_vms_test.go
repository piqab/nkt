package monitor

import "testing"

func TestLibvirtDomStats(t *testing.T) {
	st := libvirtDomStats("Domain: 'web-vm'\n  cpu.time=5000000000\n  balloon.rss=1048576\n  net.count=2\n  net.0.rx.bytes=10\n  net.1.rx.bytes=5\n\nDomain: 'db'\n  cpu.time=1\n")
	if st["web-vm"]["cpu.time"] != 5e9 || st["web-vm"]["balloon.rss"] != 1048576 || st["web-vm"]["net.1.rx.bytes"] != 5 || st["db"]["cpu.time"] != 1 {
		t.Fatalf("%v", st)
	}
}
