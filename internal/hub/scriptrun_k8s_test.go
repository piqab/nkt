package hub

import (
	"testing"

	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// TestClusterSpecFromArgsScript — строка сценария «k8s create» в спеке
// кластера: internal — признак внутреннего пути, блок команды — Helm
// values для Cilium, no-kube-proxy — замена kube-proxy.
func TestClusterSpecFromArgsScript(t *testing.T) {
	h := store.Host{ID: 7, Name: "hv1"}
	st := script.Step{Kind: script.KindK8sCreate, Name: "lab", Host: "hv1",
		Args: map[string]string{
			"flavor": "kubeadm", "cni": "cilium", "no-kube-proxy": "true",
			"internal": "true", "endpoint": "fly1.example",
		},
		Block: "cni:\n  binPath: /usr/lib/cni",
	}
	spec, err := clusterSpecFromArgs(h, st)
	if err != nil {
		t.Fatal(err)
	}
	if spec.CNI != "cilium" || !spec.KubeProxyReplacement {
		t.Errorf("cni: %+v", spec)
	}
	if spec.CiliumValues != st.Block {
		t.Errorf("values: %q", spec.CiliumValues)
	}
	if !spec.EndpointInternal || spec.Endpoint != "fly1.example" {
		t.Errorf("internal: %+v", spec)
	}
	// Без cilium блок в спеку не попадает, internal — только от флага;
	// CNI остаётся пустым (flannel — путь flavor по умолчанию).
	plain, err := clusterSpecFromArgs(h, script.Step{Kind: script.KindK8sCreate, Name: "lab", Host: "hv1",
		Args: map[string]string{"cni": "flannel"}, Block: "a: b"})
	if err != nil {
		t.Fatal(err)
	}
	if plain.CiliumValues != "" || plain.EndpointInternal || plain.CNI != "" {
		t.Errorf("plain: %+v", plain)
	}
}
