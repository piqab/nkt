package hub

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// The cluster-scoped agent API resolves to the cluster's first control
// plane: load-balancer machines are skipped, an all-LB or empty cluster
// is an error rather than a proxy onto a host without k8s.
func TestClusterAgentHost(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	cl, err := db.CreateCluster(ctx, store.Cluster{Name: "lab", HostID: 1, Flavor: "kubeadm", Topology: TopologyCP3})
	if err != nil {
		t.Fatal(err)
	}
	newHost := func(name, role string) int64 {
		id, err := db.CreateHost(ctx, name, "10.101.1.7", 22, "nkt", store.HostAuthKey, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SetHostCluster(ctx, id, cl, role); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Created LB-first on purpose: the resolver must pick the control
	// plane regardless of insertion order.
	lb := newHost("lab-lb-1", RoleLoadBalancer)
	cp := newHost("lab-cp-1", RoleControlPlane)
	_ = newHost("lab-w-1", RoleWorker)

	h, err := srv.clusterAgentHost(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}
	if h.ID != cp {
		t.Errorf("agent host = %d (%s), want control plane %d", h.ID, h.Name, cp)
	}
	if h.ID == lb {
		t.Error("load-balancer machine must not serve the cluster agent API")
	}

	// A cluster with no Kubernetes nodes at all.
	empty, err := db.CreateCluster(ctx, store.Cluster{Name: "empty", HostID: 1, Flavor: "kubeadm", Topology: TopologyCP3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.clusterAgentHost(ctx, empty); err == nil {
		t.Error("empty cluster must be an error")
	}
}
