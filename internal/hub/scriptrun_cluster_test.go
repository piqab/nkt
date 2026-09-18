package hub

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// Размещение из сценария превращается в те же Placements, что и таблица
// раздела «Кластеры»: хосты по именам, мост из строки хоста или общий,
// режим сети.
func TestClusterSpecFromScriptPlacement(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	ids := map[string]int64{}
	for _, n := range []string{"hv1", "hv2", "hv3"} {
		id, err := db.CreateHost(ctx, n, n+".lan", 22, "root", "key", []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		ids[n] = id
	}
	m := NewManager(&config.Config{}, db, make([]byte, 32), "test", slog.Default())
	r := &ScriptRunner{m: m}
	done := &scriptRunResume{HostIDs: map[string]int64{}}
	h, _ := db.HostByID(ctx, ids["hv1"])

	sc, issues := script.Parse(`on hv1 k8s create lab nodes "hv1: cp 1, w 2; hv2: w 2, bridge br1; hv3: host w" network bridge bridge br0 mem 2048 cni cilium no-kube-proxy expose api 16443`, nil)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	spec, err := r.clusterSpecFromStep(ctx, done, h, sc.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if spec.NetworkMode != NetworkBridge || spec.HostID != ids["hv1"] || len(spec.Placements) != 4 || spec.CNI != "cilium" || !spec.KubeProxyReplacement || spec.ExposeAPI != 16443 {
		t.Fatalf("spec: %+v", spec)
	}
	p := spec.Placements
	if p[0].HostID != ids["hv1"] || p[0].Role != RoleControlPlane || p[0].Kind != KindVM || p[0].Count != 1 || p[0].Bridge != "br0" || p[0].MemMB != 2048 || p[0].ImageID != "ubuntu-24.04" {
		t.Errorf("p0: %+v", p[0])
	}
	if p[2].HostID != ids["hv2"] || p[2].Bridge != "br1" || p[2].Count != 2 {
		t.Errorf("p2: %+v", p[2])
	}
	if p[3].HostID != ids["hv3"] || p[3].Kind != KindHost || p[3].Role != RoleWorker || p[3].Bridge != "" {
		t.Errorf("p3: %+v", p[3])
	}
	cps, workers := spec.NodeCounts()
	if cps != 1 || workers != 5 || spec.jobSteps() != 10 {
		t.Errorf("counts: %d %d %d", cps, workers, spec.jobSteps())
	}

	// WireGuard: мосты отбрасываются, шаг туннеля добавляется.
	sc, _ = script.Parse(`on hv1 k8s create lab nodes "hv1: cp 1; hv2: w 1" network wireguard`, nil)
	spec, err = r.clusterSpecFromStep(ctx, done, h, sc.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if spec.NetworkMode != NetworkWireGuard || spec.jobSteps() != 7 {
		t.Errorf("wg spec: %+v", spec)
	}
	// Неизвестный хост в размещении.
	sc, _ = script.Parse(`on hv1 k8s create lab nodes "nope: cp 1"`, nil)
	if _, err := r.clusterSpecFromStep(ctx, done, h, sc.Steps[0]); err == nil {
		t.Errorf("unknown host must fail")
	}
	// Без размещения — как раньше.
	sc, _ = script.Parse(`on hv1 k8s create lab nodes 1+2`, nil)
	spec, err = r.clusterSpecFromStep(ctx, done, h, sc.Steps[0])
	if err != nil || len(spec.Placements) != 0 || spec.Workers != 2 || spec.Topology != TopologyCP1 {
		t.Errorf("legacy: %+v %v", spec, err)
	}
}
