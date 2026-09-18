package hub

import (
	"testing"

	"github.com/piqab/nkt/internal/store"
)

func TestBuildWGPlan(t *testing.T) {
	hosts := []store.Host{{ID: 7, Name: "hv1", Addr: "10.0.0.7"}, {ID: 9, Name: "hv2", Addr: "hv2.lan"}}
	p, err := buildWGPlan(3, hosts, func(h store.Host) string { return h.Addr })
	if err != nil {
		t.Fatal(err)
	}
	if p.Iface != "nktwg3" || p.Port != 51820 || len(p.Hosts) != 2 {
		t.Fatalf("plan: %+v", p)
	}
	h1, h2 := p.Hosts[0], p.Hosts[1]
	if h1.Address != "10.200.3.1/24" || h1.IP != "10.200.3.1" || h1.VMSubnet != "10.103.1.0/24" || h1.Endpoint != "10.0.0.7:51820" {
		t.Errorf("host1: %+v", h1)
	}
	if h2.Address != "10.200.3.2/24" || h2.VMSubnet != "10.103.2.0/24" || h2.Endpoint != "hv2.lan:51820" {
		t.Errorf("host2: %+v", h2)
	}
	if h1.PrivateKey == h2.PrivateKey || h1.PublicKey == h2.PublicKey || h1.PrivateKey == "" {
		t.Errorf("keys must be distinct and set")
	}
	m := p.mesh(h1)
	if err := m.Validate(); err != nil {
		t.Fatalf("mesh: %v", err)
	}
	if m.Name != "nktwg3" || m.PrivateKey != h1.PrivateKey || len(m.Peers) != 1 || m.Peers[0].PublicKey != h2.PublicKey {
		t.Errorf("mesh: %+v", m)
	}
	if got := m.Peers[0].AllowedIPs; len(got) != 2 || got[0] != "10.200.3.2/32" || got[1] != "10.103.2.0/24" {
		t.Errorf("allowed: %v", got)
	}
	if p.host(9) == nil || p.host(1) != nil {
		t.Errorf("host lookup")
	}
}

func TestValidatePlacementsWireGuard(t *testing.T) {
	spec := ClusterSpec{Name: "lab", Flavor: "k3s", NetworkMode: NetworkWireGuard, ImageID: "ubuntu-24.04", Placements: []Placement{
		{HostID: 1, Role: RoleControlPlane, Kind: KindVM, Count: 1, Bridge: "br0", Network: "default"},
		{HostID: 2, Role: RoleWorker, Kind: KindHost},
	}}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Placements[0].Bridge != "" || spec.Placements[0].Network != "" || spec.HostID != 1 {
		t.Errorf("wireguard placements must drop bridge/network: %+v", spec.Placements[0])
	}
}
