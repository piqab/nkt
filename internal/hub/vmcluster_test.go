package hub

import (
	"context"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// Кластер токена (1.17.9): только single и cp1 на одном хосте, в сети
// токена; узлы — машины в потолках, учёт пишется сразу; API — порт пула
// (нет пула — отказ); потолок кластеров; удаление снимает учёт и порт.
func TestTokenClusterPrepare(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	kvm, _ := db.CreateHost(ctx, "kvm1", "203.0.113.20", 22, "root", store.HostAuthKey, []byte("x"))
	tok := store.APIToken{ID: 7, Name: "client", Role: store.TokenRoleCustom, Perms: store.TokenPerms{Actions: []string{store.TokenActK8sCluster},
		Limits: store.TokenLimits{K8sClusters: 1, VMs: 3, VMCPU: 2, VMMemMB: 4096, VMDiskGB: 40}}}
	base := func() ClusterSpec {
		return ClusterSpec{Name: "lab", HostID: kvm, Topology: TopologyCP1, Workers: 2, ImageID: "ubuntu-24.04", User: "deploy",
			CPVCPUs: 2, CPMemoryMB: 4096, CPDiskGB: 20, WVCPUs: 1, WMemoryMB: 2048, WDiskGB: 20}
	}
	for what, mod := range map[string]func(*ClusterSpec){
		"cp3": func(s *ClusterSpec) { s.Topology = TopologyCP3 },
		"placements": func(s *ClusterSpec) {
			s.Placements = []Placement{{HostID: kvm, Role: RoleWorker, Kind: KindVM, Count: 1}}
		},
		"host image": func(s *ClusterSpec) { s.ImageID = "host:other.qcow2" },
		"vm cpu":     func(s *ClusterSpec) { s.CPVCPUs = 4 },
		"vm count":   func(s *ClusterSpec) { s.Workers = 3 },
	} {
		s := base()
		mod(&s)
		if err := srv.tokenClusterPrepare(ctx, tok, &s); err == nil {
			t.Errorf("%s accepted", what)
		}
	}
	s := base()
	if err := srv.tokenClusterPrepare(ctx, tok, &s); err == nil || !strings.Contains(err.Error(), "kvm1") {
		t.Fatalf("no pool: %v", err)
	}
	_ = db.KVSet(ctx, portPoolKV, `{"kvm1":{"from":21000,"to":21001}}`)
	s = base()
	if err := srv.tokenClusterPrepare(ctx, tok, &s); err != nil {
		t.Fatal(err)
	}
	if !s.Expose || s.ExposeAPI != 21000 || s.ExposeHTTP != 0 || s.Network != "nkt-t7" || s.Flavor != "k3s" {
		t.Fatalf("spec: %+v", s)
	}
	if u, _ := db.ListTokenUsage(ctx, 7); len(u) != 3 {
		t.Fatalf("usage: %+v", u)
	}
	if a := srv.portAllocs(ctx); len(a) != 1 || a[0].VM != "lab-cp-1" || a[0].VMPort != 6443 {
		t.Fatalf("allocs: %+v", a)
	}
	// Потолок кластеров: один уже есть.
	_ = srv.saveOwnClusters(ctx, []OwnCluster{{ClusterID: 1, Name: "lab", TokenID: 7, Token: "client"}})
	s2 := base()
	s2.Name = "lab2"
	if err := srv.tokenClusterPrepare(ctx, tok, &s2); err == nil {
		t.Fatal("clusters limit ignored")
	}
	releaseClusterToken(ctx, db, s, "kvm1")
	forgetOwnCluster(ctx, db, 1)
	if u, _ := db.ListTokenUsage(ctx, 7); len(u) != 0 || len(srv.portAllocs(ctx)) != 0 || len(srv.ownClusters(ctx)) != 0 {
		t.Fatalf("after release: %+v %+v", u, srv.portAllocs(ctx))
	}
}
