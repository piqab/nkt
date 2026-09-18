package control

import (
	"strings"
	"testing"
)

const (
	testPriv = "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	testPub  = "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="
)

func TestWGMeshConfig(t *testing.T) {
	m := WGMesh{Name: "nktwg3", PrivateKey: testPriv, Address: "10.200.3.1/24", ListenPort: 51820, VMSubnet: "10.103.1.0/24",
		Peers: []WGPeer{{Name: "hv2", PublicKey: testPub, Endpoint: "hv2.example:51820", AllowedIPs: []string{"10.200.3.2/32", "10.103.2.0/24"}}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg := m.Config()
	for _, want := range []string{
		"PrivateKey = " + testPriv,
		"Address = 10.200.3.1/24",
		"ListenPort = 51820",
		"iptables -I INPUT -p udp --dport 51820 -j ACCEPT",
		"iptables -I FORWARD -i %i -j ACCEPT",
		"iptables -t nat -I POSTROUTING -s 10.103.1.0/24 -d 10.200.3.0/24 -j RETURN",
		"iptables -t nat -I POSTROUTING -s 10.103.1.0/24 -d 10.103.2.0/24 -j RETURN",
		"PostDown = iptables -D INPUT -p udp --dport 51820 -j ACCEPT 2>/dev/null || true",
		"PublicKey = " + testPub,
		"Endpoint = hv2.example:51820",
		"AllowedIPs = 10.200.3.2/32, 10.103.2.0/24",
		"PersistentKeepalive = 25",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	// Адрес узла (/32) в маскарад-исключения не попадает — только подсети.
	if strings.Contains(cfg, "-d 10.200.3.2/32 -j RETURN") {
		t.Errorf("/32 peer address must not get a nat rule:\n%s", cfg)
	}
}

func TestWGMeshValidate(t *testing.T) {
	good := WGMesh{Name: "nktwg1", PrivateKey: testPriv, Address: "10.200.1.1/24", ListenPort: 51820, VMSubnet: "10.101.1.0/24"}
	bad := []func(m *WGMesh){
		func(m *WGMesh) { m.Name = "this-name-is-way-too-long" },
		func(m *WGMesh) { m.Name = "wg0; rm -rf /" },
		func(m *WGMesh) { m.PrivateKey = "nope" },
		func(m *WGMesh) { m.Address = "10.200.1.1" },
		func(m *WGMesh) { m.ListenPort = 0 },
		func(m *WGMesh) {
			m.Peers = []WGPeer{{PublicKey: testPub, Endpoint: "host", AllowedIPs: []string{"10.0.0.0/8"}}}
		},
		func(m *WGMesh) { m.Peers = []WGPeer{{PublicKey: testPub, Endpoint: "h:1", AllowedIPs: nil}} },
		func(m *WGMesh) {
			m.Peers = []WGPeer{{PublicKey: testPub, Endpoint: "h`x`:1", AllowedIPs: []string{"10.0.0.0/8"}}}
		},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}
	for i, f := range bad {
		m := good
		f(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}
