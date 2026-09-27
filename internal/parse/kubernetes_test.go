package parse

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

func TestKubernetesFromFixtures(t *testing.T) {
	c := collect.NewFixtures(filepath.Join("..", "..", "fixtures", "host"))
	res := Kubernetes(context.Background(), c)
	if res.State == nil {
		t.Fatalf("нет сводки: %+v", res.Status)
	}
	st := res.State
	if st.Flavor != "k3s" || len(st.Pods) == 0 || len(st.Nodes) != 3 || len(st.Services) == 0 {
		t.Fatalf("%+v", st)
	}
	var crash, priv bool
	for _, p := range st.Pods {
		if p.Reason == "CrashLoopBackOff" && p.Restarts == 14 {
			crash = true
		}
		if len(p.Privileged) > 0 && p.HostNetwork {
			priv = true
		}
	}
	if !crash || !priv {
		t.Errorf("crash=%v priv=%v", crash, priv)
	}
	var notReady bool
	for _, n := range st.Nodes {
		if n.Name == "lab-w-2" && !n.Ready {
			notReady = true
		}
	}
	if !notReady {
		t.Error("lab-w-2 не NotReady")
	}
}
