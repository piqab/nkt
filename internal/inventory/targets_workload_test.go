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

// Хост, где кроме кластера ничего нет, тоже получает цели доступности.
func TestDeriveK8sTargets(t *testing.T) {
	snap := &model.Snapshot{K8s: &model.K8sState{
		Ingresses: []model.K8sIngress{{Namespace: "default", Name: "web", Hosts: []string{"shop.example.com", "*"}}},
		Services: []model.K8sService{
			{Namespace: "shop", Name: "api", Type: "NodePort", Ports: []model.K8sServicePort{{Port: 8080, NodePort: 30080, Protocol: "TCP"}}},
			{Namespace: "shop", Name: "db", Type: "ClusterIP", Ports: []model.K8sServicePort{{Port: 5432}}},
		},
		Nodes: []model.K8sNode{{Name: "w-1", IP: "192.168.122.11"}, {Name: "w-2"}},
	}}
	got := map[string]string{}
	for _, tg := range DeriveTargets(snap) {
		got[tg.Key] = tg.Kind + " " + tg.Address() + " " + tg.HostHeader
	}
	want := map[string]string{
		"k8s-ing:default/web:shop.example.com": "http 127.0.0.1:80 shop.example.com",
		"k8s-svc:shop/api:30080":               "tcp 127.0.0.1:30080 ",
		"k8s-node:w-1":                         "icmp 192.168.122.11:0 ",
	}
	if len(got) != len(want) {
		t.Fatalf("цели: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
}
