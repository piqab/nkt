package topology

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/model"
)

// Машины и инстансы LXD на карте: сети, проброшенный порт LXD и бэкенд
// nginx, который смотрит на адрес машины.
func TestWorkloadLinks(t *testing.T) {
	snap := &model.Snapshot{
		LXD: []model.LXDInstance{{
			Name: "c1", Status: "running", Type: "container", IPv4: []string{"10.44.12.5"}, Networks: []string{"lxdbr0"},
			Ports: []model.LXDPort{{Device: "http", Listen: "tcp:0.0.0.0:8088", Connect: "tcp:127.0.0.1:80"}},
		}},
		VMs: []model.VirtualMachine{{Name: "web", State: "running", Networks: []model.VMNetIface{{Source: "default", IP: "192.168.122.45"}}}},
		Upstreams: []model.Upstream{{ID: "u1", Name: "app", Service: "nginx", Servers: []model.UpstreamServer{
			{Host: "192.168.122.45", Port: 8080}, {Host: "10.44.12.5", Port: 3000},
		}}},
	}
	g := Build(context.Background(), snap)
	has := func(from, to, kind string) bool {
		for _, e := range g.Edges {
			if e.From == from && e.To == to && e.Kind == kind {
				return true
			}
		}
		return false
	}
	for _, c := range [][3]string{
		{"lxd:c1", "net:lxd:lxdbr0", "attached"},
		{"vm:web", "net:libvirt:default", "attached"},
		{"ep:lxd:c1:http", "lxd:c1", "publishes"},
		{"internet", "ep:lxd:c1:http", "ingress"},
		{"be:192.168.122.45:8080", "vm:web", "served-by"},
		{"be:10.44.12.5:3000", "lxd:c1", "served-by"},
	} {
		if !has(c[0], c[1], c[2]) {
			var list []string
			for _, e := range g.Edges {
				list = append(list, e.From+" → "+e.To+" "+e.Kind)
			}
			t.Errorf("нет связи %v; есть: %v", c, list)
		}
	}
}
