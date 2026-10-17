package fly

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConntrack(t *testing.T) {
	out := `tcp      6 431997 ESTABLISHED src=198.51.100.7 dst=203.0.113.1 sport=51514 dport=2222 packets=12 bytes=1800 src=10.77.1.2 dst=198.51.100.7 sport=40002 dport=51514 packets=10 bytes=2400 [ASSURED] mark=1179389955 use=1
udp      17 29 src=198.51.100.9 dst=203.0.113.1 sport=40001 dport=25565 packets=3 bytes=300 src=10.77.1.3 dst=198.51.100.9 sport=40010 dport=40001 packets=3 bytes=600 mark=1179389956 use=1
tcp      6 120 TIME_WAIT src=198.51.100.7 dst=203.0.113.1 sport=51515 dport=2222 packets=5 bytes=300 src=10.77.1.2 dst=198.51.100.7 sport=40002 dport=51515 packets=4 bytes=200 [ASSURED] mark=1179389955 use=1
conntrack v1.4.8 (conntrack-tools): 3 flow entries have been shown.
`
	s := ParseConntrack(out)
	if s.Conns != 3 || s.Clients["198.51.100.7"] != 2 || s.Clients["198.51.100.9"] != 1 || s.Bytes != 1800+2400+300+600+300+200 {
		t.Fatalf("%+v", s)
	}
}

func TestParseWGDump(t *testing.T) {
	out := "privkey\tpubkey\t51820\toff\n" +
		keyA + "\t(none)\t198.51.100.20:43211\t10.77.1.2/32\t1760000000\t1024\t2048\t25\n" +
		keyB + "\t(none)\t(none)\t10.77.1.3/32\t0\t0\t0\t25\n"
	p := ParseWGDump(out)
	if len(p) != 2 || p[0].Endpoint != "198.51.100.20:43211" || p[0].Handshake != 1760000000 || p[1].Endpoint != "" || p[0].Address != "10.77.1.2" {
		t.Fatalf("%+v", p)
	}
	if !TunnelUp(1760000000, 1760000100) || TunnelUp(1760000000, 1760000300) || TunnelUp(0, 1) {
		t.Fatal("TunnelUp")
	}
}

func TestKeys(t *testing.T) {
	p := t.TempDir() + "/k/wg.key"
	pub, err := LoadOrCreateKey(p)
	if err != nil || !ValidKey(pub) {
		t.Fatal(pub, err)
	}
	again, _ := LoadOrCreateKey(p)
	if again != pub {
		t.Fatal("ключ пересоздан")
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
}

func TestFilePath(t *testing.T) {
	dir := t.TempDir()
	if p, err := FilePath(dir, "nkt-fly1", ".key"); err != nil || p != filepath.Join(dir, "nkt-fly1.key") {
		t.Fatalf("good name: %q %v", p, err)
	}
	if _, err := FilePath(dir, "wg", ".key"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../etc/passwd", "nkt-fly1/../../x", "", "eth0", "nkt-fly1\x00"} {
		if _, err := FilePath(dir, bad, ".key"); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
	if _, err := FilePath(dir, "nkt-fly1", ".conf"); err == nil {
		t.Error("ext .conf accepted")
	}
	if RouteMark(-1) != GatewayMarkBase || RouteMark(1<<20) != GatewayMarkBase || RouteMark(3) != GatewayMarkBase+3 {
		t.Error("RouteMark range")
	}
	if HostMark(2) != HostMarkBase+2 {
		t.Error("HostMark")
	}
}
