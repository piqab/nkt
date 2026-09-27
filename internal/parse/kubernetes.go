package parse

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// Сводка кластера Kubernetes для находок и карты ресурсов. Снимается
// только на control plane (k3s-сервер или kubeadm с admin.conf), одним
// kubectl get нескольких видов сразу. Полный обзор объектов — в
// internal/k8s, по запросу; здесь — только нужное правилам.

// K8sResult — сводка и строка в таблице источников.
type K8sResult struct {
	Status model.SourceStatus
	State  *model.K8sState
}

// K8sSummaryResources — виды одного kubectl get для сводки.
const K8sSummaryResources = "pods,deployments.apps,statefulsets.apps,nodes,persistentvolumeclaims,services,ingresses.networking.k8s.io"

// k8sCertPaths — сертификат API-сервера у kubeadm и k3s.
var k8sCertPaths = []string{
	"/etc/kubernetes/pki/apiserver.crt",
	"/var/lib/rancher/k3s/server/tls/serving-kube-apiserver.crt",
}

// Kubernetes снимает сводку кластера. Не control plane — пустой результат
// без строки источника.
func Kubernetes(ctx context.Context, c collect.Collector) K8sResult {
	var argv []string
	flavor := ""
	switch {
	case c.Exists("/etc/systemd/system/k3s.service") && collect.Which(ctx, c, "k3s"):
		flavor, argv = "k3s", []string{"k3s", "kubectl"}
	case c.Exists("/etc/kubernetes/admin.conf") && collect.Which(ctx, c, "kubectl"):
		flavor, argv = "kubeadm", []string{"kubectl", "--kubeconfig", "/etc/kubernetes/admin.conf"}
	default:
		return K8sResult{}
	}
	started := time.Now()
	res := K8sResult{Status: model.SourceStatus{Name: model.ServiceK8s}}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()

	args := append(append([]string{}, argv[1:]...), "get", K8sSummaryResources, "-A", "-o", "json")
	out, err := c.Run(ctx, argv[0], args...)
	if err != nil {
		res.Status.Error = err.Error()
		return res
	}
	if !out.OK() {
		res.Status.Error = strings.TrimSpace(out.Stderr)
		return res
	}
	st, err := ParseK8sSummary([]byte(out.Stdout))
	if err != nil {
		res.Status.Error = err.Error()
		return res
	}
	st.Flavor = flavor
	for _, p := range k8sCertPaths {
		raw, err := c.ReadFile(p)
		if err != nil {
			continue
		}
		if b, _ := pem.Decode(raw); b != nil {
			if cert, err := x509.ParseCertificate(b.Bytes); err == nil {
				st.Certs = append(st.Certs, model.K8sCert{Path: p, NotAfter: cert.NotAfter.UTC().Format(time.RFC3339)})
			}
		}
	}
	res.Status.Available = true
	res.State = st
	return res
}

type k8sMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	CreationTimestamp string            `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels"`
	OwnerReferences   []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"ownerReferences"`
}

type k8sItem struct {
	Kind     string          `json:"kind"`
	Metadata k8sMeta         `json:"metadata"`
	Spec     json.RawMessage `json:"spec"`
	Status   json.RawMessage `json:"status"`
}

// ParseK8sSummary разбирает список kubectl get <несколько видов> -o json.
func ParseK8sSummary(raw []byte) (*model.K8sState, error) {
	var l struct {
		Items []k8sItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, err
	}
	st := &model.K8sState{Pods: []model.K8sPod{}, Workloads: []model.K8sWorkload{}, Nodes: []model.K8sNode{},
		PVCs: []model.K8sPVC{}, Services: []model.K8sService{}, Ingresses: []model.K8sIngress{}}
	for _, it := range l.Items {
		md := it.Metadata
		switch it.Kind {
		case "Pod":
			st.Pods = append(st.Pods, k8sPod(it))
		case "Deployment", "StatefulSet":
			var spec struct {
				Replicas *int `json:"replicas"`
				Selector struct {
					MatchLabels map[string]string `json:"matchLabels"`
				} `json:"selector"`
			}
			var status struct {
				AvailableReplicas int `json:"availableReplicas"`
				ReadyReplicas     int `json:"readyReplicas"`
			}
			_ = json.Unmarshal(it.Spec, &spec)
			_ = json.Unmarshal(it.Status, &status)
			want := 1
			if spec.Replicas != nil {
				want = *spec.Replicas
			}
			avail := status.AvailableReplicas
			if it.Kind == "StatefulSet" {
				avail = status.ReadyReplicas
			}
			st.Workloads = append(st.Workloads, model.K8sWorkload{Kind: it.Kind, Namespace: md.Namespace, Name: md.Name,
				Desired: want, Available: avail, Selector: spec.Selector.MatchLabels})
		case "Node":
			var spec struct {
				Unschedulable bool `json:"unschedulable"`
			}
			var status struct {
				Conditions []struct{ Type, Status string } `json:"conditions"`
				Addresses  []struct{ Type, Address string } `json:"addresses"`
			}
			_ = json.Unmarshal(it.Spec, &spec)
			_ = json.Unmarshal(it.Status, &status)
			n := model.K8sNode{Name: md.Name, Unschedulable: spec.Unschedulable}
			for _, c := range status.Conditions {
				if c.Type == "Ready" {
					n.Ready = c.Status == "True"
				}
			}
			for _, a := range status.Addresses {
				if a.Type == "InternalIP" && n.IP == "" {
					n.IP = a.Address
				}
			}
			st.Nodes = append(st.Nodes, n)
		case "PersistentVolumeClaim":
			var status struct {
				Phase string `json:"phase"`
			}
			_ = json.Unmarshal(it.Status, &status)
			st.PVCs = append(st.PVCs, model.K8sPVC{Namespace: md.Namespace, Name: md.Name, Phase: status.Phase, Created: md.CreationTimestamp})
		case "Service":
			var spec struct {
				Type     string            `json:"type"`
				Selector map[string]string `json:"selector"`
				Ports    []struct {
					Port     int    `json:"port"`
					NodePort int    `json:"nodePort"`
					Protocol string `json:"protocol"`
				} `json:"ports"`
				ExternalIPs []string `json:"externalIPs"`
			}
			var status struct {
				LoadBalancer struct {
					Ingress []struct{ IP, Hostname string } `json:"ingress"`
				} `json:"loadBalancer"`
			}
			_ = json.Unmarshal(it.Spec, &spec)
			_ = json.Unmarshal(it.Status, &status)
			svc := model.K8sService{Namespace: md.Namespace, Name: md.Name, Type: spec.Type, Selector: spec.Selector, External: spec.ExternalIPs}
			for _, p := range spec.Ports {
				svc.Ports = append(svc.Ports, model.K8sServicePort{Port: p.Port, NodePort: p.NodePort, Protocol: p.Protocol})
			}
			for _, in := range status.LoadBalancer.Ingress {
				if in.IP != "" {
					svc.External = append(svc.External, in.IP)
				} else if in.Hostname != "" {
					svc.External = append(svc.External, in.Hostname)
				}
			}
			st.Services = append(st.Services, svc)
		case "Ingress":
			var spec struct {
				DefaultBackend *struct {
					Service struct{ Name string } `json:"service"`
				} `json:"defaultBackend"`
				Rules []struct {
					Host string `json:"host"`
					HTTP struct {
						Paths []struct {
							Backend struct {
								Service struct{ Name string } `json:"service"`
							} `json:"backend"`
						} `json:"paths"`
					} `json:"http"`
				} `json:"rules"`
			}
			_ = json.Unmarshal(it.Spec, &spec)
			in := model.K8sIngress{Namespace: md.Namespace, Name: md.Name}
			seen := map[string]bool{}
			addSvc := func(n string) {
				if n != "" && !seen[n] {
					seen[n] = true
					in.Services = append(in.Services, n)
				}
			}
			if spec.DefaultBackend != nil {
				addSvc(spec.DefaultBackend.Service.Name)
			}
			for _, r := range spec.Rules {
				if r.Host != "" {
					in.Hosts = append(in.Hosts, r.Host)
				}
				for _, p := range r.HTTP.Paths {
					addSvc(p.Backend.Service.Name)
				}
			}
			sort.Strings(in.Services)
			st.Ingresses = append(st.Ingresses, in)
		}
	}
	return st, nil
}

func k8sPod(it k8sItem) model.K8sPod {
	md := it.Metadata
	var spec struct {
		NodeName    string `json:"nodeName"`
		HostNetwork bool   `json:"hostNetwork"`
		Containers  []struct {
			Name            string `json:"name"`
			SecurityContext *struct {
				Privileged *bool `json:"privileged"`
			} `json:"securityContext"`
		} `json:"containers"`
	}
	var status struct {
		Phase             string `json:"phase"`
		PodIP             string `json:"podIP"`
		ContainerStatuses []struct {
			Ready        bool `json:"ready"`
			RestartCount int  `json:"restartCount"`
			State        struct {
				Waiting *struct {
					Reason string `json:"reason"`
				} `json:"waiting"`
			} `json:"state"`
		} `json:"containerStatuses"`
	}
	_ = json.Unmarshal(it.Spec, &spec)
	_ = json.Unmarshal(it.Status, &status)
	p := model.K8sPod{Namespace: md.Namespace, Name: md.Name, Node: spec.NodeName, IP: status.PodIP, Phase: status.Phase,
		Created: md.CreationTimestamp, Labels: md.Labels, HostNetwork: spec.HostNetwork, Ready: len(status.ContainerStatuses) > 0}
	if len(md.OwnerReferences) > 0 {
		p.Owner = md.OwnerReferences[0].Kind + "/" + md.OwnerReferences[0].Name
	}
	for _, cs := range status.ContainerStatuses {
		p.Restarts += cs.RestartCount
		if !cs.Ready {
			p.Ready = false
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && p.Reason == "" {
			p.Reason = cs.State.Waiting.Reason
		}
	}
	for _, c := range spec.Containers {
		if c.SecurityContext != nil && c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
			p.Privileged = append(p.Privileged, c.Name)
		}
	}
	return p
}
