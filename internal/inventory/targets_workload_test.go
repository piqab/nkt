package inventory

import (
	"testing"

	"github.com/piqab/nkt/internal/model"
)

func TestDeriveWorkloadTargets(t *testing.T) {
	snap := &model.Snapshot{
		Podman: []model.PodmanContainer{
			{Name: "grafana", State: "running", Ports: []model.PortMapping{{HostPort: 3000, ContainerPort: 3000, Protocol: "tcp"}}},
			{Name: "old", State: "exited", Ports: []model.PortMapping{{HostPort: 3001, ContainerPort: 3000}}},
		},
		LXD: []model.LXDInstance{
			{Name: "c1", Status: "Running", IPv4: []string{"10.44.12.5"}, Ports: []model.LXDPort{{Device: "http", Listen: "tcp:0.0.0.0:8088", Connect: "tcp:127.0.0.1:80"}, {Device: "dns", Listen: "udp:0.0.0.0:53"}}},
			{Name: "off", Status: "Stopped", IPv4: []string{"10.44.12.6"}},
		},
		VMs: []model.VirtualMachine{
			{Name: "web", State: "running", Networks: []model.VMNetIface{{MAC: "m", IP: "192.168.122.45"}}},
			{Name: "noip", State: "running", Networks: []model.VMNetIface{{MAC: "m"}}},
		},
	}
	got := map[string]string{}
	for _, tg := range DeriveTargets(snap) {
		got[tg.Key] = tg.Kind + " " + tg.Address()
	}
	want := map[string]string{
		"podman:grafana:127.0.0.1:3000":   "tcp 127.0.0.1:3000",
		"lxd-port:c1:http:127.0.0.1:8088": "tcp 127.0.0.1:8088",
		"lxd-ip:c1":                       "icmp 10.44.12.5:0",
		"vm-ip:web":                       "icmp 192.168.122.45:0",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
}
