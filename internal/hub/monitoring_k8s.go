package hub

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/store"
)

// Kubernetes в «Мониторинге»: control plane отдаёт в сводке состав
// кластера — узлы с ролью и узел каждого пода. По нему хаб подписывает
// хосты («k8s · worker, кластер lab»), поды — их узлом, а узлы, которых
// нет среди хостов хаба, показывает отдельно.

// monK8sNode — узел кластера из сводки control plane.
type monK8sNode struct {
	Name         string `json:"name"`
	IP           string `json:"ip,omitempty"`
	Ready        bool   `json:"ready"`
	ControlPlane bool   `json:"control_plane,omitempty"`
	// HostID — хост хаба, которым этот узел является (0 — такого нет).
	HostID int64 `json:"host_id,omitempty"`
}

// monK8s — состав кластера, как его отдаёт хост.
type monK8s struct {
	Nodes []monK8sNode      `json:"nodes"`
	Pods  map[string]string `json:"pods"`
}

// monCluster — кластер в разделе: имя, хост control plane, узлы.
type monCluster struct {
	Name   string       `json:"name"`
	HostID int64        `json:"host_id"`
	Nodes  []monK8sNode `json:"nodes"`
	pods   map[string]string
}

// monK8sTag — подпись хоста хаба, который входит в кластер.
type monK8sTag struct {
	Cluster string
	Role    string // control-plane | worker
	Node    string
}

func monK8sKey(id int64) string { return "mon.k8s." + itoa64(id) }

// saveMonK8s запоминает состав кластера хоста (nil — хост не control plane).
func (s *Server) saveMonK8s(ctx context.Context, id int64, k *monK8s) {
	raw := ""
	if k != nil && len(k.Nodes) > 0 {
		if b, err := json.Marshal(k); err == nil {
			raw = string(b)
		}
	}
	_ = s.db.KVSet(ctx, monK8sKey(id), raw)
}

func (s *Server) loadMonK8s(ctx context.Context, id int64) *monK8s {
	raw, ok, err := s.db.KVGet(ctx, monK8sKey(id))
	if err != nil || !ok || raw == "" {
		return nil
	}
	var k monK8s
	if json.Unmarshal([]byte(raw), &k) != nil || len(k.Nodes) == 0 {
		return nil
	}
	return &k
}

// monClusters — кластеры по сводкам control plane и подписи хостов хаба:
// узел сопоставляется с хостом по адресу, затем по имени. Хосты кластеров,
// созданных хабом, подписаны и без сводки — по своей роли в «Кластерах».
func (s *Server) monClusters(ctx context.Context, hosts []store.Host) ([]monCluster, map[int64]monK8sTag) {
	names := map[int64]string{}
	if list, err := s.db.ListClusters(ctx); err == nil {
		for _, c := range list {
			names[c.ID] = c.Name
		}
	}
	tags := map[int64]monK8sTag{}
	var out []monCluster
	seen := map[string]bool{}
	for _, h := range hosts {
		k := s.loadMonK8s(ctx, h.ID)
		if k == nil {
			continue
		}
		name := h.Name
		if h.ClusterID > 0 && names[h.ClusterID] != "" {
			name = names[h.ClusterID]
		}
		// Несколько control plane одного кластера отдают одно и то же.
		if seen[name] {
			continue
		}
		seen[name] = true
		c := monCluster{Name: name, HostID: h.ID, pods: k.Pods}
		self := false
		for _, n := range k.Nodes {
			n.HostID = matchNodeHost(n, hosts)
			self = self || n.HostID == h.ID
			c.Nodes = append(c.Nodes, n)
		}
		// Хост, отдавший состав кластера, сам — control plane. Если ни один
		// узел с ним не сопоставился (адрес за NAT, другое имя), а
		// несопоставленный управляющий узел один — это он.
		if !self {
			idx := -1
			for i, n := range c.Nodes {
				if n.ControlPlane && n.HostID == 0 {
					if idx >= 0 {
						idx = -2
						break
					}
					idx = i
				}
			}
			if idx >= 0 {
				c.Nodes[idx].HostID = h.ID
			} else {
				tags[h.ID] = monK8sTag{Cluster: name, Role: RoleControlPlane}
			}
		}
		for _, n := range c.Nodes {
			role := RoleWorker
			if n.ControlPlane {
				role = RoleControlPlane
			}
			if n.HostID != 0 {
				tags[n.HostID] = monK8sTag{Cluster: name, Role: role, Node: n.Name}
			}
		}
		sort.Slice(c.Nodes, func(i, j int) bool {
			if c.Nodes[i].ControlPlane != c.Nodes[j].ControlPlane {
				return c.Nodes[i].ControlPlane
			}
			return c.Nodes[i].Name < c.Nodes[j].Name
		})
		out = append(out, c)
	}
	for _, h := range hosts {
		if _, ok := tags[h.ID]; ok || h.ClusterID == 0 || h.K8sRole == "" {
			continue
		}
		tags[h.ID] = monK8sTag{Cluster: names[h.ClusterID], Role: h.K8sRole}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, tags
}

// matchNodeHost — хост хаба, которым является узел: по адресу, затем по имени.
func matchNodeHost(n monK8sNode, hosts []store.Host) int64 {
	if n.IP != "" {
		for _, h := range hosts {
			if h.Addr == n.IP {
				return h.ID
			}
		}
	}
	for _, h := range hosts {
		if strings.EqualFold(h.Name, n.Name) {
			return h.ID
		}
	}
	return 0
}
