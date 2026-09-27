package topology

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/model"
)

func TestBuildK8s(t *testing.T) {
	s := &model.Snapshot{
		VMs: []model.VirtualMachine{{Name: "lab-w-1", State: "running"}},
		K8s: &model.K8sState{
			Nodes: []model.K8sNode{{Name: "lab-w-1", Ready: true, IP: "10.0.0.2"}},
			Pods: []model.K8sPod{
				{Namespace: "shop", Name: "api-1", Node: "lab-w-1", Phase: "Running", Ready: true, Labels: map[string]string{"app": "api"}},
				{Namespace: "shop", Name: "api-2", Node: "lab-w-1", Phase: "Running", Reason: "CrashLoopBackOff", Labels: map[string]string{"app": "api"}},
			},
			Services:  []model.K8sService{{Namespace: "shop", Name: "api", Type: "NodePort", Selector: map[string]string{"app": "api"}, Ports: []model.K8sServicePort{{Port: 80, NodePort: 30080}}}},
			Ingresses: []model.K8sIngress{{Namespace: "shop", Name: "web", Hosts: []string{"shop.example.com"}, Services: []string{"api", "gone"}}},
		},
	}
	g := Build(context.Background(), s)
	edges := map[string]bool{}
	for _, e := range g.Edges {
		edges[e.From+"->"+e.To] = true
	}
	for _, want := range []string{
		"internet->k8s:ing:shop/web", "k8s:ing:shop/web->k8s:svc:shop/api", "k8s:svc:shop/api->k8s:pod:shop/api-1",
		"k8s:svc:shop/api->k8s:pod:shop/api-2", "k8s:pod:shop/api-1->k8s:node:lab-w-1", "k8s:node:lab-w-1->vm:lab-w-1",
		"internet->k8s:svc:shop/api", "k8s:ing:shop/web->k8s:svc:shop/gone",
	} {
		if !edges[want] {
			t.Errorf("нет связи %s", want)
		}
	}
	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	if nodes["k8s:pod:shop/api-2"].Status != StatusError || nodes["k8s:ing:shop/web"].Status != StatusError || nodes["k8s:svc:shop/gone"].Status != StatusError {
		t.Errorf("статусы: %+v", nodes)
	}
}
