package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/store"
)

const testLonghornIndex = `apiVersion: v1
entries:
  longhorn:
  - version: 1.13.0
    kubeVersion: '>=1.34.0-0'
    urls:
    - https://github.com/longhorn/charts/releases/download/longhorn-1.13.0/longhorn-1.13.0.tgz
  - version: 1.13.0-rc1
  - version: 1.12.1
    kubeVersion: '>=1.25.0-0'
  - version: 1.12.0
  - version: 1.11.3
  - version: 1.10.2
  - version: 1.9.2
  - version: 1.8.3
  other:
  - version: 9.9.9
`

// cert-manager keeps relative chart URLs in its index — they must resolve
// against the repository base.
const testRelativeURLIndex = `apiVersion: v1
entries:
  cert-manager:
  - version: v1.21.3
    kubeVersion: '>= 1.22.0-0'
    urls:
    - charts/cert-manager-v1.21.3.tgz
`

func TestParseAddonIndex(t *testing.T) {
	all, err := parseAddonIndex([]byte(testLonghornIndex), "longhorn", k8s.LonghornRepoURL)
	if err != nil || len(all) != 7 || all[0].Version != "1.13.0" || all[0].KubeVersion != ">=1.34.0-0" {
		t.Fatalf("%v %+v", err, all)
	}
	if all[0].URL != "https://github.com/longhorn/charts/releases/download/longhorn-1.13.0/longhorn-1.13.0.tgz" {
		t.Errorf("chart URL: %q", all[0].URL)
	}
	latest := addonLatestPatches(all, 4)
	var got []string
	for _, v := range latest {
		got = append(got, v.Version)
	}
	if len(got) != 4 || got[0] != "1.13.0" || got[1] != "1.12.1" || got[3] != "1.10.2" {
		t.Errorf("latest patches: %v", got)
	}
	if _, err := parseAddonIndex([]byte(testLonghornIndex), "rook", k8s.LonghornRepoURL); err == nil {
		t.Error("no versions of an absent chart must be an error")
	}

	rel, err := parseAddonIndex([]byte(testRelativeURLIndex), "cert-manager", "https://charts.jetstack.io")
	if err != nil || len(rel) != 1 || rel[0].Version != "1.21.3" {
		t.Fatalf("%v %+v", err, rel)
	}
	if rel[0].URL != "https://charts.jetstack.io/charts/cert-manager-v1.21.3.tgz" {
		t.Errorf("relative URL resolved to %q", rel[0].URL)
	}
}

func TestValidateAddons(t *testing.T) {
	list := []ClusterAddon{{Name: "longhorn"}}
	if err := validateAddons(list, k8s.FlavorKubeadm); err != nil || list[0].Version != k8s.LonghornDefaultVersion {
		t.Fatalf("%v %+v", err, list)
	}
	// Registry addons get their own pinned default.
	cm := []ClusterAddon{{Name: "cert-manager"}}
	def, _ := k8s.AddonDefOf("cert-manager")
	if err := validateAddons(cm, k8s.FlavorKubeadm); err != nil || cm[0].Version != def.Default {
		t.Fatalf("%v %+v", err, cm)
	}
	// metrics-server is built into k3s — rejected there, fine on kubeadm.
	ms := []ClusterAddon{{Name: "metrics-server"}}
	if err := validateAddons(ms, k8s.FlavorK3s); err == nil {
		t.Error("metrics-server accepted on k3s")
	}
	if err := validateAddons([]ClusterAddon{{Name: "metrics-server"}}, k8s.FlavorKubeadm); err != nil {
		t.Errorf("metrics-server rejected on kubeadm: %v", err)
	}
	for _, bad := range [][]ClusterAddon{
		{{Name: "rook"}},
		{{Name: "longhorn", Version: "latest"}},
		{{Name: "longhorn"}, {Name: "longhorn"}},
	} {
		if validateAddons(bad, k8s.FlavorKubeadm) == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if addonReplicas(1) != 1 || addonReplicas(2) != 2 || addonReplicas(7) != 3 {
		t.Error("replicas")
	}
	spec := ClusterSpec{CPDiskGB: 30, WDiskGB: 15, Workers: 2}
	if spec.addonMinDisk() != 15 {
		t.Errorf("min disk %d", spec.addonMinDisk())
	}
	spec = ClusterSpec{Placements: []Placement{{Kind: KindVM, DiskGB: 40}, {Kind: KindHost}}}
	if spec.addonMinDisk() != 40 {
		t.Errorf("placements min disk %d", spec.addonMinDisk())
	}
}

// Load-balancer machines belong to the cluster record but must never be
// prepared for addons: their agent has no k8s flavor and answers 400, and
// replica counts sized after them exceed the number of real nodes.
func TestAddonPrepareNodesSkipLB(t *testing.T) {
	nodes := []clusterNode{
		{Name: "k8s-lb-1", Role: RoleLoadBalancer},
		{Name: "k8s-cp-1", Role: RoleControlPlane},
		{Name: "k8s-lb-2", Role: RoleLoadBalancer},
		{Name: "k8s-w-1", Role: RoleWorker},
	}
	got := addonPrepareNodes(nodes)
	if len(got) != 2 || got[0].Name != "k8s-cp-1" || got[1].Name != "k8s-w-1" {
		t.Fatalf("addonPrepareNodes = %+v, want cp-1 and w-1 only", got)
	}
	// 1 cp + 1 lb: one replica, not two.
	small := addonPrepareNodes([]clusterNode{
		{Name: "k8s-lb-1", Role: RoleLoadBalancer},
		{Name: "k8s-cp-1", Role: RoleControlPlane},
	})
	if n := addonReplicas(len(small)); n != 1 {
		t.Errorf("replicas for 1 cp + 1 lb = %d, want 1", n)
	}

	hosts := []store.Host{
		{Name: "k8s-cp-1", K8sRole: RoleControlPlane},
		{Name: "k8s-lb-1", K8sRole: RoleLoadBalancer},
		{Name: "k8s-w-1", K8sRole: RoleWorker},
	}
	gotH := k8sNodeHosts(hosts)
	if len(gotH) != 2 || gotH[0].Name != "k8s-cp-1" || gotH[1].Name != "k8s-w-1" {
		t.Fatalf("k8sNodeHosts = %+v, want cp-1 and w-1 only", gotH)
	}
	// ClusterHosts sorts control-plane first; the filter keeps that order,
	// so nodes[0] remains the control plane for the install step.
	if gotH[0].K8sRole != RoleControlPlane {
		t.Error("first node must stay control-plane")
	}
}

// The registry endpoint feeds the addon lists in the UI: every registry
// addon with a pinned default and its flags (storage, flavor conflict).
func TestHandleAddonsRegistry(t *testing.T) {
	srv, _, _ := localFixtureHub(t)
	ctx := context.Background()
	req := httptest.NewRequest(http.MethodGet, "/api/hub/k8s-addons", nil).
		WithContext(auth.WithUser(ctx, store.User{Username: "admin", Role: store.RoleAdmin}))
	w := httptest.NewRecorder()
	srv.handleAddonsRegistry(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Addons []struct {
			Name     string `json:"name"`
			Default  string `json:"default"`
			Storage  bool   `json:"storage"`
			NoFlavor string `json:"no_flavor"`
			Prepare  bool   `json:"prepare"`
		} `json:"addons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Addons) != len(k8s.Addons) {
		t.Fatalf("registry: %d addons, want %d", len(out.Addons), len(k8s.Addons))
	}
	byName := map[string]int{}
	for _, a := range out.Addons {
		byName[a.Name]++
		if a.Default == "" {
			t.Errorf("%s: no default version", a.Name)
		}
	}
	for _, want := range []string{"longhorn", "ingress-nginx", "cert-manager", "metrics-server", "metallb", "kube-prometheus-stack", "kyverno", "argo-cd"} {
		if byName[want] != 1 {
			t.Errorf("addon %s missing or duplicated", want)
		}
	}
	for _, a := range out.Addons {
		switch a.Name {
		case "longhorn":
			if !a.Storage || !a.Prepare {
				t.Errorf("longhorn flags: %+v", a)
			}
		case "metrics-server":
			if a.NoFlavor != k8s.FlavorK3s {
				t.Errorf("metrics-server flavor conflict: %q", a.NoFlavor)
			}
		default:
			if a.Storage || a.Prepare || a.NoFlavor != "" {
				t.Errorf("%s: unexpected flags %+v", a.Name, a)
			}
		}
	}
}
