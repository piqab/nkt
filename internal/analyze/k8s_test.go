package analyze

import (
	"testing"

	"github.com/piqab/nkt/internal/model"
)

func TestRuleKubernetes(t *testing.T) {
	s := &model.Snapshot{TS: "2026-09-27T12:00:00Z", K8s: &model.K8sState{
		Pods: []model.K8sPod{
			{Namespace: "shop", Name: "api-1", Reason: "CrashLoopBackOff", Restarts: 5, Node: "w1"},
			{Namespace: "shop", Name: "web-1", Phase: "Pending", Created: "2026-09-27T11:00:00Z"},
			{Namespace: "shop", Name: "web-2", Phase: "Pending", Created: "2026-09-27T11:58:00Z"},
			{Namespace: "mon", Name: "exp", HostNetwork: true, Privileged: []string{"exp"}},
		},
		Nodes:     []model.K8sNode{{Name: "w2", Ready: false}, {Name: "w1", Ready: true}},
		Workloads: []model.K8sWorkload{{Kind: "Deployment", Namespace: "shop", Name: "api", Desired: 3, Available: 2}, {Kind: "Deployment", Namespace: "shop", Name: "ok", Desired: 1, Available: 1}},
		PVCs:      []model.K8sPVC{{Namespace: "shop", Name: "data", Phase: "Pending", Created: "2026-09-26T00:00:00Z"}},
		Certs:     []model.K8sCert{{Path: "/etc/kubernetes/pki/apiserver.crt", NotAfter: "2026-10-07T00:00:00Z"}},
		Services:  []model.K8sService{{Namespace: "shop", Name: "api", Type: "NodePort", Ports: []model.K8sServicePort{{Port: 80, NodePort: 30080}}}},
	}}
	s.Firewall.Managers = []model.FirewallManagerState{{Name: "ufw", Active: true}}
	got := map[string]string{}
	for _, f := range Run(s) {
		got[f.ID] = f.Severity
	}
	want := map[string]string{
		"k8s-pod-failing:shop/api-1":                          model.SeverityHigh,
		"k8s-pod-pending:shop/web-1":                          model.SeverityMedium,
		"k8s-pod-privileged:mon/exp":                          model.SeverityMedium,
		"k8s-node-notready:w2":                                model.SeverityHigh,
		"k8s-workload-unavailable:Deployment:shop/api":        model.SeverityMedium,
		"k8s-pvc-pending:shop/data":                           model.SeverityMedium,
		"k8s-cert-expiring:/etc/kubernetes/pki/apiserver.crt": model.SeverityHigh,
		"k8s-port-bypasses-firewall:shop/api:30080":           model.SeverityMedium,
	}
	for id, sev := range want {
		if got[id] != sev {
			t.Errorf("%s: %q, ждали %q", id, got[id], sev)
		}
	}
	for _, id := range []string{"k8s-pod-pending:shop/web-2", "k8s-node-notready:w1", "k8s-workload-unavailable:Deployment:shop/ok"} {
		if _, ok := got[id]; ok {
			t.Errorf("лишняя находка %s", id)
		}
	}
}

func TestRuleK8sHygiene(t *testing.T) {
	s := &model.Snapshot{TS: "2026-09-27T12:00:00Z", K8s: &model.K8sState{
		Pods: []model.K8sPod{
			{Namespace: "shop", Name: "web-abc-1", Owner: "ReplicaSet/web-abc", NoLimits: []string{"web"}, Images: []string{"nginx"}},
			{Namespace: "shop", Name: "web-abc-2", Owner: "ReplicaSet/web-abc", NoLimits: []string{"web"}, Images: []string{"nginx"}},
			{Namespace: "sec", Name: "db-0", Owner: "StatefulSet/db", Images: []string{"postgres:16@sha256:abc", "registry:5000/app:1.2"}},
			{Namespace: "kube-system", Name: "x", NoLimits: []string{"x"}},
		},
		AdminBindings:    []model.K8sBinding{{Name: "ci", Subjects: []string{"ServiceAccount:shop/ci", "Group:system:masters", "ServiceAccount:kube-system/helm"}}},
		NetPolNamespaces: []string{"sec"},
	}}
	got := map[string]string{}
	for _, f := range Run(s) {
		got[f.ID] = f.Severity
	}
	for id, sev := range map[string]string{
		"k8s-no-limits:shop/web":                      model.SeverityLow,
		"k8s-image-latest:shop/web":                   model.SeverityLow,
		"k8s-cluster-admin:ci:ServiceAccount:shop/ci": model.SeverityHigh,
		"k8s-no-networkpolicy:shop":                   model.SeverityLow,
	} {
		if got[id] != sev {
			t.Errorf("%s: %q", id, got[id])
		}
	}
	for _, id := range []string{"k8s-image-latest:sec/db", "k8s-no-networkpolicy:sec", "k8s-no-limits:kube-system/x", "k8s-cluster-admin:ci:Group:system:masters", "k8s-cluster-admin:ci:ServiceAccount:kube-system/helm"} {
		if _, ok := got[id]; ok {
			t.Errorf("лишняя %s", id)
		}
	}
}
