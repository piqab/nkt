package topology

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/model"
)

// Kubernetes на карте ресурсов: Ingress → Service → поды → узел кластера →
// машина хоста, на которой узел работает (по имени или адресу).
const (
	KindK8sIngress = "k8s_ingress"
	KindK8sService = "k8s_service"
	KindK8sPod     = "k8s_pod"
	KindK8sNode    = "k8s_node"
)

func k8sKey(ns, name string) string {
	if ns == "" {
		return name
	}
	return ns + "/" + name
}

// selects — все пары селектора есть среди меток пода.
func selects(sel, labels map[string]string) bool {
	if len(sel) == 0 {
		return false
	}
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func buildK8s(b *builder, s *model.Snapshot) {
	k := s.K8s
	if k == nil {
		return
	}
	// Узлы кластера и машины под ними.
	for _, n := range k.Nodes {
		id := "k8s:node:" + n.Name
		status := StatusOK
		if !n.Ready {
			status = StatusError
		} else if n.Unschedulable {
			status = StatusWarn
		}
		sub := "Ready"
		if !n.Ready {
			sub = "NotReady"
		}
		if n.Unschedulable {
			sub += ", cordon"
		}
		b.node(Node{ID: id, Kind: KindK8sNode, Label: n.Name, Sublabel: sub, Group: model.ServiceK8s, Status: status,
			Meta: map[string]string{"ip": n.IP}})
		b.attachFindings(id, n.Name)
		for _, cand := range b.order {
			m := b.nodes[cand]
			if m.Kind != KindVM && m.Kind != KindLXD {
				continue
			}
			sameIP := n.IP != "" && m.Meta != nil && containsIP(m.Meta["ip"], n.IP)
			if m.Label == n.Name || sameIP {
				b.edge(id, cand, "runs_on", n.IP, StatusOK)
			}
		}
	}
	// Поды.
	for _, p := range k.Pods {
		key := k8sKey(p.Namespace, p.Name)
		id := "k8s:pod:" + key
		status := StatusOK
		sub := p.Phase
		switch {
		case p.Reason != "":
			status, sub = StatusError, p.Reason
		case p.Phase == "Pending" || p.Phase == "Failed" || p.Phase == "Unknown":
			status = StatusWarn
		case p.Phase == "Running" && !p.Ready:
			status = StatusWarn
		}
		meta := map[string]string{"namespace": p.Namespace, "phase": p.Phase, "restarts": strconv.Itoa(p.Restarts)}
		if p.IP != "" {
			meta["ip"] = p.IP
		}
		if p.Owner != "" {
			meta["owner"] = p.Owner
		}
		b.node(Node{ID: id, Kind: KindK8sPod, Label: p.Name, Sublabel: p.Namespace + " · " + sub, Group: model.ServiceK8s, Status: status, Meta: meta})
		b.attachFindings(id, key)
		if p.Node != "" {
			b.edge(id, "k8s:node:"+p.Node, "scheduled", "", StatusOK)
		}
	}
	// Сервисы: к подам по селектору, NodePort/LoadBalancer — вход снаружи.
	for _, svc := range k.Services {
		key := k8sKey(svc.Namespace, svc.Name)
		id := "k8s:svc:" + key
		var ports []string
		for _, p := range svc.Ports {
			if p.NodePort > 0 {
				ports = append(ports, fmt.Sprintf("%d:%d", p.Port, p.NodePort))
			} else {
				ports = append(ports, strconv.Itoa(p.Port))
			}
		}
		meta := map[string]string{"namespace": svc.Namespace, "type": svc.Type, "ports": strings.Join(ports, ", ")}
		if len(svc.External) > 0 {
			meta["external"] = strings.Join(svc.External, ", ")
		}
		b.node(Node{ID: id, Kind: KindK8sService, Label: svc.Name, Sublabel: svc.Namespace + " · " + svc.Type, Group: model.ServiceK8s, Status: StatusOK, Meta: meta})
		for _, p := range svc.Ports {
			if svc.Type == "NodePort" || svc.Type == "LoadBalancer" {
				port := p.NodePort
				if svc.Type == "LoadBalancer" {
					port = p.Port
				}
				if port > 0 {
					b.nodes[id].Public = true
					b.edge("internet", id, "ingress", strconv.Itoa(port), StatusOK)
				}
			}
		}
		matched := 0
		for _, pod := range k.Pods {
			if pod.Namespace == svc.Namespace && selects(svc.Selector, pod.Labels) {
				b.edge(id, "k8s:pod:"+k8sKey(pod.Namespace, pod.Name), "selects", "", StatusOK)
				matched++
			}
		}
		// Сервис с селектором, под которым нет ни одного пода, — трафику
		// некуда идти.
		if len(svc.Selector) > 0 && matched == 0 {
			b.nodes[id].Status = StatusWarn
		}
		b.attachFindings(id, key)
	}
	// Ingress — к сервисам своего namespace.
	for _, in := range k.Ingresses {
		key := k8sKey(in.Namespace, in.Name)
		id := "k8s:ing:" + key
		hosts := append([]string(nil), in.Hosts...)
		sort.Strings(hosts)
		b.node(Node{ID: id, Kind: KindK8sIngress, Label: in.Name, Sublabel: strings.Join(hosts, ", "), Group: model.ServiceK8s, Status: StatusOK,
			Public: true, Meta: map[string]string{"namespace": in.Namespace, "hosts": strings.Join(hosts, ", ")}})
		b.edge("internet", id, "ingress", "http", StatusOK)
		for _, svc := range in.Services {
			to := "k8s:svc:" + k8sKey(in.Namespace, svc)
			status := StatusOK
			if b.nodes[to] == nil {
				// Ingress ведёт на несуществующий сервис — узел-заглушка,
				// чтобы разрыв был виден на карте.
				status = StatusError
				b.nodes[id].Status = StatusError
				b.node(Node{ID: to, Kind: KindK8sService, Label: svc, Sublabel: in.Namespace + " · —", Group: model.ServiceK8s, Status: StatusError})
			}
			b.edge(id, to, "routes", svc, status)
		}
		b.attachFindings(id, key)
	}
}

// containsIP — адрес среди перечисленных через запятую.
func containsIP(list, ip string) bool {
	for _, p := range strings.Split(list, ",") {
		if strings.TrimSpace(p) == ip {
			return true
		}
	}
	return false
}
