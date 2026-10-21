package hub

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// «fly route … targets cluster lab port 8080»: цели — все узлы кластера,
// машины — «vm» на своём хосте (имя машины = имя в хабе), узел-хост —
// «host»; маршрут проходит проверку карты, как при сохранении.
func TestScriptFlyRouteClusterTargets(t *testing.T) {
	s, gw, hv1 := flyTestServer(t)
	ctx := context.Background()
	db := s.db
	r := NewScriptRunner(s)
	hv2, err := db.CreateHost(ctx, "hv2", "192.0.2.6", 22, "root", store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	cl, err := db.CreateCluster(ctx, store.Cluster{Name: "lab", HostID: hv1, Flavor: "kubeadm", Topology: "cp1", Workers: 2, SpecJSON: `{"cni":"cilium","kube_proxy_replacement":true}`})
	if err != nil {
		t.Fatal(err)
	}
	add := func(name string, parent int64, role string) {
		id, err := db.CreateHost(ctx, name, "10.101.1.2", 22, "nkt", store.HostAuthKey, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		if parent != 0 {
			if err := db.SetHostParent(ctx, id, parent); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.SetHostCluster(ctx, id, cl, role); err != nil {
			t.Fatal(err)
		}
	}
	add("lab-cp-1", hv1, "control-plane")
	add("lab-w-1", hv1, "worker")
	add("lab-w-2", hv2, "worker")
	if err := db.SetHostCluster(ctx, hv2, cl, "worker"); err != nil { // и сам хост — узел
		t.Fatal(err)
	}
	sc, issues := script.Parse("fly route hello https names hello.example.com tls gateway check tcp targets cluster lab port 8080", nil)
	if len(issues) > 0 {
		t.Fatal(issues[0].Err)
	}
	route, err := r.flyRouteFromStep(ctx, &scriptRunResume{}, sc.Steps[0], false)
	if err != nil || route == nil {
		t.Fatalf("%v %v", route, err)
	}
	if route.Gateway != gw || !route.TLS || route.Port != 443 || route.Kind != "https" || len(route.Targets) != 4 {
		t.Fatalf("%+v", route)
	}
	byRef := map[string]FlyTarget{}
	for _, tg := range route.Targets {
		byRef[tg.Ref+"@"+tg.Kind] = tg
	}
	if tg := byRef["lab-cp-1@vm"]; tg.HostID != hv1 || tg.Port != 8080 {
		t.Errorf("cp: %+v", tg)
	}
	if tg := byRef["lab-w-2@vm"]; tg.HostID != hv2 {
		t.Errorf("w-2: %+v", tg)
	}
	if tg := byRef["@host"]; tg.HostID != hv2 || tg.Port != 8080 {
		t.Errorf("узел-хост: %+v", tg)
	}
	d := s.flyDoc(ctx)
	d.Routes = append(d.Routes, *route)
	if _, err := s.flySave(ctx, d, "admin", "test"); err != nil {
		t.Fatalf("карта с маршрутом не сохраняется: %v", err)
	}
	// Ingress — только у кластера с Cilium без kube-proxy.
	cl2, _ := db.CreateCluster(ctx, store.Cluster{Name: "plain", HostID: hv1, Flavor: "k3s", Topology: "single", SpecJSON: `{}`})
	id, _ := db.CreateHost(ctx, "plain-cp-1", "10.101.1.9", 22, "nkt", store.HostAuthKey, []byte("x"))
	_ = db.SetHostCluster(ctx, id, cl2, "control-plane")
	sc, _ = script.Parse("k8s plain ingress cilium", nil)
	if err := r.k8sIngress(ctx, nil, &scriptRunResume{}, sc.Steps[0], false); err == nil {
		t.Error("Ingress на кластере без Cilium не отказан")
	}
}
