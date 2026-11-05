package hub

import (
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// Общий адрес control plane: kubeadm принимает его в любой топологии и
// требует при трёх control plane; у k3s его быть не может; порт
// дописывается сразу, и именно адрес кластера идёт в kubeconfig.
func TestClusterEndpointValidate(t *testing.T) {
	cp3 := func(flavor string) ClusterSpec {
		return ClusterSpec{Name: "lab", Flavor: flavor, Topology: TopologyCP3, Workers: 1, ImageID: "ubuntu-24.04"}
	}
	spec := cp3("kubeadm")
	spec.Endpoint = " K8S.Example.com "
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Endpoint != "k8s.example.com:6443" {
		t.Errorf("порт не дописан: %q", spec.Endpoint)
	}
	// k3s три control plane живут — общего адреса у него нет и не нужно.
	k3 := cp3("k3s")
	if err := k3.Validate(); err != nil {
		t.Errorf("k3s cp3: %v", err)
	}
	// Один control plane живёт и без endpoint'а, и с ним.
	one := ClusterSpec{Name: "lab", Flavor: "kubeadm", Topology: TopologyCP1, Workers: 1, ImageID: "ubuntu-24.04"}
	if err := one.Validate(); err != nil {
		t.Errorf("kubeadm cp1 без endpoint: %v", err)
	}
	one.Endpoint = "10.0.0.5"
	if err := one.Validate(); err != nil {
		t.Errorf("kubeadm cp1 с endpoint: %v", err)
	}
	// Три control plane kubeadm без общего адреса — отказ, а не кластер,
	// висящий на первом узле.
	noEnd := cp3("kubeadm")
	err := noEnd.Validate()
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("cp3 без endpoint: %v", err)
	}
	// У k3s поле означает другую систему — молча проигнорировать его
	// нельзя.
	bad := cp3("k3s")
	bad.Endpoint = "k8s.example.com"
	if err := bad.Validate(); err == nil {
		t.Error("endpoint у k3s прошёл")
	}
	for _, form := range []string{"k8s_example", "k8s.example.com:0", ":6443", "k8s example"} {
		spec := cp3("kubeadm")
		spec.Endpoint = form
		if err := spec.Validate(); err == nil {
			t.Errorf("прошла форма: %q", form)
		}
	}
}

// Те же правила при размещении по хостам и в строке сценария.
func TestClusterEndpointPlacementAndScript(t *testing.T) {
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", ImageID: "ubuntu-24.04", NetworkMode: NetworkNAT, Placements: []Placement{
		{HostID: 1, Role: RoleControlPlane, Kind: KindVM, Count: 3},
		{HostID: 1, Role: RoleWorker, Kind: KindVM, Count: 1},
	}}
	if err := spec.Validate(); err == nil {
		t.Error("три control plane без endpoint прошли")
	}
	spec.Endpoint = "k8s.example.com"
	if err := spec.Validate(); err != nil {
		t.Fatalf("размещение с endpoint: %v", err)
	}
	if spec.Endpoint != "k8s.example.com:6443" {
		t.Errorf("порт не дописан: %q", spec.Endpoint)
	}

	sc, issues := script.Parse("on hv1 k8s create lab flavor kubeadm nodes 3+1 endpoint ep.example", nil)
	if len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	fromArgs, err := clusterSpecFromArgs(store.Host{ID: 7}, sc.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if fromArgs.Topology != TopologyCP3 || fromArgs.Endpoint != "ep.example" {
		t.Fatalf("args: %+v", fromArgs)
	}
	if err := fromArgs.Validate(); err != nil {
		t.Fatal(err)
	}
	if fromArgs.Endpoint != "ep.example:6443" {
		t.Errorf("endpoint из сценария: %q", fromArgs.Endpoint)
	}
	// Сценарий без endpoint при трёх control plane — тот же отказ.
	sc, issues = script.Parse("on hv1 k8s create lab flavor kubeadm nodes 3+1", nil)
	if len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	noEnd, err := clusterSpecFromArgs(store.Host{ID: 7}, sc.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := noEnd.Validate(); err == nil {
		t.Error("сценарий cp3 без endpoint прошёл")
	}
}

// Адрес API: общий endpoint важнее проброса — он и есть адрес кластера;
// без него — как был.
func TestClusterAPIAddrEndpoint(t *testing.T) {
	spec := ClusterSpec{Name: "lab", Flavor: "kubeadm", Topology: TopologyCP1, Workers: 1, ImageID: "ubuntu-24.04",
		Expose: true}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := spec.apiAddr("203.0.113.7", "10.0.0.5"); got != "203.0.113.7:6443" {
		t.Errorf("без endpoint: %q", got)
	}
	spec.Endpoint = "ep.example:7443"
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := spec.apiAddr("203.0.113.7", "10.0.0.5"); got != "ep.example:7443" {
		t.Errorf("с endpoint: %q", got)
	}
}
