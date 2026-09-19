package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Кластер Kubernetes на виртуалках хоста: хаб создаёт машины (тем же
// заданием, что и «Новая машина», с установкой nkt), ставит на первую
// control plane, берёт у неё токен, присоединяет остальные, ждёт Ready,
// при желании пробрасывает порты хоста в control plane и забирает
// kubeconfig. Узлы — обычные хосты в группе с именем кластера.

const (
	KindClusterCreate = "cluster.create"
	KindClusterDelete = "cluster.delete"
	KindClusterAdd    = "cluster.add"

	TopologySingle = "single" // одна машина: control plane + worker
	TopologyCP1    = "cp1"    // 1 control plane + N worker
	TopologyCP3    = "cp3"    // 3 control plane (k3s) + N worker

	clusterReadyTimeout = 15 * time.Minute
	clusterNodePoll     = 10 * time.Second
)

// ClusterSpec — что создаём. Хранится в clusters.spec_json.
type ClusterSpec struct {
	Name     string `json:"name"`
	HostID   int64  `json:"host_id"`
	Flavor   string `json:"flavor"`
	Topology string `json:"topology"`
	Workers  int    `json:"workers"`
	Expose   bool   `json:"expose"`
	// Порты хоста при пробросе: API (→6443), HTTP (→80), HTTPS (→443);
	// 0 — не пробрасывать этот. Пустые при Expose — умолчания.
	ExposeAPI   int `json:"expose_api,omitempty"`
	ExposeHTTP  int `json:"expose_http,omitempty"`
	ExposeHTTPS int `json:"expose_https,omitempty"`
	// CNI — «cilium» или пусто (flannel); KubeProxyReplacement — Cilium
	// вместо kube-proxy.
	CNI                  string `json:"cni,omitempty"`
	KubeProxyReplacement bool   `json:"kube_proxy_replacement,omitempty"`
	// K8sVersion — kubeadm: минорная версия («1.34»). Пусто — хаб
	// подбирает актуальную stable при создании и записывает сюда, чтобы
	// все узлы (и добавленные потом worker'ы) были одной версии.
	K8sVersion string `json:"k8s_version,omitempty"`
	// Placements — размещение по хостам (раздел «Кластеры»); пусто —
	// один хост из HostID/Topology/Workers (диалог у хоста).
	Placements []Placement `json:"placements,omitempty"`
	// NetworkMode — как узлы на разных хостах видят друг друга: nat (один
	// хост, сеть libvirt), bridge (машины в сети хостов), wireguard
	// (туннели между хостами).
	NetworkMode string `json:"network_mode,omitempty"`
	ImageID     string `json:"image_id"`
	Network     string `json:"network,omitempty"`
	User        string `json:"user"`
	// Размеры control plane и worker'ов.
	CPVCPUs    int `json:"cp_vcpus"`
	CPMemoryMB int `json:"cp_memory_mb"`
	CPDiskGB   int `json:"cp_disk_gb"`
	WVCPUs     int `json:"w_vcpus"`
	WMemoryMB  int `json:"w_memory_mb"`
	WDiskGB    int `json:"w_disk_gb"`
}

// Placement — строка размещения: на хосте — N машин с ролью или сам хост.
type Placement struct {
	HostID int64  `json:"host_id"`
	Role   string `json:"role"` // control-plane | worker
	Kind   string `json:"kind"` // vm | host
	Count  int    `json:"count"`
	VCPUs  int    `json:"vcpus,omitempty"`
	MemMB  int    `json:"memory_mb,omitempty"`
	DiskGB int    `json:"disk_gb,omitempty"`
	// ImageID/Network — для машин; Bridge — мост хоста в режиме bridge.
	ImageID string `json:"image_id,omitempty"`
	Network string `json:"network,omitempty"`
	Bridge  string `json:"bridge,omitempty"`
	// Endpoint — адрес этого хоста для соседей по туннелю (режим
	// wireguard); пусто — адрес хоста из списка nkt.
	Endpoint string `json:"endpoint,omitempty"`
}

const (
	NetworkNAT       = "nat"
	NetworkBridge    = "bridge"
	NetworkWireGuard = "wireguard"

	RoleControlPlane = "control-plane"
	RoleWorker       = "worker"
	KindVM           = "vm"
	KindHost         = "host"
)

// clusterNode — один узел после разворачивания размещения.
type clusterNode struct {
	Name    string
	HostID  int64 // хост, на котором машина (или сам узел при Kind=host)
	Role    string
	Kind    string
	VCPUs   int
	MemMB   int
	DiskGB  int
	ImageID string
	Network string
	Bridge  string
}

var k8sMinorRe = regexp.MustCompile(`^\d+\.\d+$`)

// hostnameLikeRe — имя кластера: оно же префикс имён машин и hostname.
var hostnameLikeRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// Validate проверяет форму и заполняет умолчания.
func (s *ClusterSpec) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if !hostnameLikeRe.MatchString(s.Name) {
		return msgs.Errorf("hub.clusterBadName", s.Name)
	}
	if s.Flavor != k8s.FlavorK3s && s.Flavor != k8s.FlavorKubeadm {
		return msgs.Errorf("k8s.badFlavor", s.Flavor)
	}
	s.K8sVersion = strings.TrimPrefix(strings.TrimSpace(s.K8sVersion), "v")
	if s.K8sVersion != "" && !k8sMinorRe.MatchString(s.K8sVersion) {
		return msgs.Errorf("hub.clusterBadK8sVersion", s.K8sVersion)
	}
	if len(s.Placements) > 0 {
		if err := s.validatePlacements(); err != nil {
			return err
		}
	} else {
		switch s.Topology {
		case TopologySingle:
			s.Workers = 0
		case TopologyCP1:
			if s.Workers < 1 {
				return msgs.Errorf("hub.clusterNeedsWorkers")
			}
		case TopologyCP3:
			if s.Flavor != k8s.FlavorK3s {
				return msgs.Errorf("hub.clusterCP3K3sOnly")
			}
		default:
			return msgs.Errorf("hub.clusterBadTopology", s.Topology)
		}
		if s.Workers > 20 {
			return msgs.Errorf("hub.clusterTooManyWorkers")
		}
		if s.ImageID == "" {
			return msgs.Errorf("hub.clusterNeedsImage")
		}
		if s.NetworkMode == "" {
			s.NetworkMode = NetworkNAT
		}
	}
	if s.CNI != "" && s.CNI != "cilium" {
		return msgs.Errorf("k8s.badCNI", s.CNI)
	}
	if s.CNI != "cilium" {
		s.KubeProxyReplacement = false
	}
	if s.Expose {
		if s.ExposeAPI == 0 && s.ExposeHTTP == 0 && s.ExposeHTTPS == 0 {
			s.ExposeAPI, s.ExposeHTTP, s.ExposeHTTPS = 6443, 80, 443
		}
		for _, p := range []int{s.ExposeAPI, s.ExposeHTTP, s.ExposeHTTPS} {
			if p < 0 || p > 65535 {
				return msgs.Errorf("hub.clusterBadPort", p)
			}
		}
		if s.ExposeAPI == 0 {
			// Без API-порта kubeconfig наружу не собрать — проброс без
			// него бессмыслен.
			return msgs.Errorf("hub.clusterExposeNeedsAPI")
		}
	} else {
		s.ExposeAPI, s.ExposeHTTP, s.ExposeHTTPS = 0, 0, 0
	}
	if s.User == "" {
		s.User = "deploy"
	}
	if s.CPVCPUs == 0 {
		s.CPVCPUs = 2
	}
	if s.CPMemoryMB == 0 {
		s.CPMemoryMB = 4096
	}
	if s.CPDiskGB == 0 {
		s.CPDiskGB = 30
	}
	if s.WVCPUs == 0 {
		s.WVCPUs = 2
	}
	if s.WMemoryMB == 0 {
		s.WMemoryMB = 4096
	}
	if s.WDiskGB == 0 {
		s.WDiskGB = 30
	}
	return nil
}

// validatePlacements проверяет размещение по хостам.
func (s *ClusterSpec) validatePlacements() error {
	cps, total := 0, 0
	hosts := map[int64]bool{}
	for i := range s.Placements {
		pl := &s.Placements[i]
		if pl.HostID <= 0 {
			return msgs.Errorf("hub.clusterPlacementHost")
		}
		if pl.Role != RoleControlPlane && pl.Role != RoleWorker {
			return msgs.Errorf("hub.clusterPlacementRole", pl.Role)
		}
		switch pl.Kind {
		case KindHost:
			pl.Count = 1
		case KindVM:
			if pl.Count < 1 || pl.Count > 20 {
				return msgs.Errorf("hub.clusterTooManyWorkers")
			}
			if pl.ImageID == "" {
				pl.ImageID = s.ImageID
			}
			if pl.ImageID == "" {
				return msgs.Errorf("hub.clusterNeedsImage")
			}
			if pl.VCPUs == 0 {
				pl.VCPUs = 2
			}
			if pl.MemMB == 0 {
				pl.MemMB = 4096
			}
			if pl.DiskGB == 0 {
				pl.DiskGB = 30
			}
		default:
			return msgs.Errorf("hub.clusterPlacementKind", pl.Kind)
		}
		if pl.Role == RoleControlPlane {
			cps += pl.Count
		}
		total += pl.Count
		hosts[pl.HostID] = true
	}
	if cps != 1 && cps != 3 {
		return msgs.Errorf("hub.clusterCPCount", cps)
	}
	if cps == 3 && s.Flavor != k8s.FlavorK3s {
		return msgs.Errorf("hub.clusterCP3K3sOnly")
	}
	if total < 1 {
		return msgs.Errorf("hub.clusterNoNodes")
	}
	if s.NetworkMode == "" {
		s.NetworkMode = NetworkNAT
	}
	switch s.NetworkMode {
	case NetworkNAT:
		if len(hosts) > 1 {
			return msgs.Errorf("hub.clusterNATSingleHost")
		}
	case NetworkBridge:
		for _, pl := range s.Placements {
			if pl.Kind == KindVM && pl.Bridge == "" {
				return msgs.Errorf("hub.clusterBridgeNeeded")
			}
		}
	case NetworkWireGuard:
		// Сеть машин на каждом хосте заводится своя (см. cluster_wg.go),
		// мост и выбранная сеть не нужны.
		for i := range s.Placements {
			pl := &s.Placements[i]
			pl.Bridge, pl.Network = "", ""
			pl.Endpoint = strings.TrimSpace(pl.Endpoint)
			if pl.Endpoint != "" && (strings.ContainsAny(pl.Endpoint, " \t\n'\"`$\\;&|/") || strings.Contains(pl.Endpoint, ":")) {
				return msgs.Errorf("hub.clusterBadEndpoint", pl.Endpoint)
			}
		}
	default:
		return msgs.Errorf("hub.clusterBadNetwork", s.NetworkMode)
	}
	// HostID — хост первого control plane: туда идут проброс и адрес
	// kubeconfig.
	for _, pl := range s.Placements {
		if pl.Role == RoleControlPlane {
			s.HostID = pl.HostID
			break
		}
	}
	s.Topology = ""
	return nil
}

// nodes разворачивает размещение в узлы с именами <кластер>-cp-N /
// <кластер>-w-N; узел-хост носит имя самого хоста.
func (s ClusterSpec) nodes(hostName func(int64) string) []clusterNode {
	pls := s.Placements
	if len(pls) == 0 {
		pls = []Placement{
			{HostID: s.HostID, Role: RoleControlPlane, Kind: KindVM, Count: s.ControlPlanes(), VCPUs: s.CPVCPUs, MemMB: s.CPMemoryMB, DiskGB: s.CPDiskGB, ImageID: s.ImageID, Network: s.Network},
			{HostID: s.HostID, Role: RoleWorker, Kind: KindVM, Count: s.Workers, VCPUs: s.WVCPUs, MemMB: s.WMemoryMB, DiskGB: s.WDiskGB, ImageID: s.ImageID, Network: s.Network},
		}
	}
	var out []clusterNode
	cp, w := 0, 0
	// Сначала все control plane, потом worker'ы — порядок установки.
	for _, role := range []string{RoleControlPlane, RoleWorker} {
		for _, pl := range pls {
			if pl.Role != role {
				continue
			}
			for i := 0; i < pl.Count; i++ {
				n := clusterNode{HostID: pl.HostID, Role: pl.Role, Kind: pl.Kind, VCPUs: pl.VCPUs, MemMB: pl.MemMB, DiskGB: pl.DiskGB, ImageID: pl.ImageID, Network: pl.Network, Bridge: pl.Bridge}
				if pl.Kind == KindHost {
					n.Name = hostName(pl.HostID)
				} else if role == RoleControlPlane {
					cp++
					n.Name = fmt.Sprintf("%s-cp-%d", s.Name, cp)
				} else {
					w++
					n.Name = fmt.Sprintf("%s-w-%d", s.Name, w)
				}
				out = append(out, n)
			}
		}
	}
	return out
}

// wgEndpoint — адрес хоста для соседей по туннелю: из размещения или
// адрес хоста.
func (s ClusterSpec) wgEndpoint(h store.Host) string {
	for _, pl := range s.Placements {
		if pl.HostID == h.ID && pl.Endpoint != "" {
			return pl.Endpoint
		}
	}
	return h.Addr
}

// exposeRules — правила проброса хоста в control plane.
func (s ClusterSpec) exposeRules() []map[string]int {
	var out []map[string]int
	for _, pr := range [][2]int{{s.ExposeAPI, 6443}, {s.ExposeHTTP, 80}, {s.ExposeHTTPS, 443}} {
		if pr[0] > 0 {
			out = append(out, map[string]int{"host_port": pr[0], "vm_port": pr[1]})
		}
	}
	return out
}

// APIAddr — адрес API в kubeconfig: хост с портом при пробросе, иначе
// первый control plane.
func (s ClusterSpec) apiAddr(hostAddr, cpAddr string) string {
	if s.Expose && hostAddr != "" {
		return fmt.Sprintf("%s:%d", hostAddr, s.ExposeAPI)
	}
	return cpAddr + ":6443"
}

// ControlPlanes — сколько control plane в топологии.
func (s ClusterSpec) ControlPlanes() int {
	if s.Topology == TopologyCP3 {
		return 3
	}
	return 1
}

// NodeCounts — сколько control plane и worker'ов, с учётом размещения.
func (s ClusterSpec) NodeCounts() (cps, workers int) {
	if len(s.Placements) == 0 {
		return s.ControlPlanes(), s.Workers
	}
	for _, pl := range s.Placements {
		if pl.Role == RoleControlPlane {
			cps += pl.Count
		} else {
			workers += pl.Count
		}
	}
	return
}

// jobSteps — шагов в задании создания: проверки, узлы, control plane,
// присоединение, Ready; в режиме wireguard ещё туннель.
func (s ClusterSpec) jobSteps() int {
	cps, workers := s.NodeCounts()
	n := cps + workers + 4
	if s.NetworkMode == NetworkWireGuard {
		n++
	}
	return n
}

// nodeNames — имена машин: <кластер>-cp-N и <кластер>-w-N.
func (s ClusterSpec) nodeNames() (cps, workers []string) {
	for i := 1; i <= s.ControlPlanes(); i++ {
		cps = append(cps, fmt.Sprintf("%s-cp-%d", s.Name, i))
	}
	for i := 1; i <= s.Workers; i++ {
		workers = append(workers, fmt.Sprintf("%s-w-%d", s.Name, i))
	}
	return
}

// ClusterJobParams — вход заданий кластера.
type ClusterJobParams struct {
	ClusterID int64 `json:"cluster_id"`
	// AddWorkers — для cluster.add: сколько worker'ов добавить.
	AddWorkers int `json:"add_workers,omitempty"`
	// DryRun — только проверки (и подготовка при Prepare); Spec — вместо
	// записи кластера, которой в сухом прогоне не заводится.
	DryRun  bool         `json:"dry_run,omitempty"`
	Prepare bool         `json:"prepare,omitempty"`
	Spec    *ClusterSpec `json:"spec,omitempty"`
}

type clusterResume struct {
	// MeshUp — туннель между хостами уже поднят (режим wireguard).
	MeshUp bool `json:"mesh_up,omitempty"`
	// Hosts — заведённые машины по имени → id хоста.
	Hosts map[string]int64 `json:"hosts"`
	// Installed — узлы, на которых роль уже стоит.
	Installed map[string]bool `json:"installed"`
	Exposed   bool            `json:"exposed"`
}

// ClusterRunner — создание и пополнение кластера.
type ClusterRunner struct {
	m *Manager
	s *Server
}

func NewClusterRunner(s *Server) *ClusterRunner { return &ClusterRunner{m: s.hub, s: s} }

func (r *ClusterRunner) Resumable() bool { return true }

// Run создаёт кластер (или добавляет worker'ы — те же шаги для новых
// машин).
func (r *ClusterRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ClusterJobParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	if p.DryRun && p.Spec != nil {
		jc.StepKey(1, 1, "hub.clusterStepPreflight")
		failed, err := r.runPreflight(ctx, jc, *p.Spec, p.Prepare, 0)
		if err != nil {
			return err
		}
		if len(failed) > 0 {
			return preflightError(failed)
		}
		return nil
	}
	cl, err := r.m.db.ClusterByID(ctx, p.ClusterID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return msgs.Errorf("hub.clusterGone")
		}
		return err
	}
	var spec ClusterSpec
	if err := json.Unmarshal([]byte(cl.SpecJSON), &spec); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	var done clusterResume
	if err := jc.LoadResume(&done); err != nil {
		return msgs.Errorf("hub.parsingResumeState", err)
	}
	if cl.Status == store.ClusterFailed {
		// Продолжение после провала: кластер снова «создаётся».
		_ = r.m.db.SetClusterStatus(ctx, cl.ID, store.ClusterCreating, "")
	}
	if done.Hosts == nil {
		done.Hosts = map[string]int64{}
	}
	if done.Installed == nil {
		done.Installed = map[string]bool{}
	}
	err = r.run(ctx, jc, cl, spec, p, &done)
	if err != nil {
		_ = r.m.db.SetClusterStatus(context.Background(), cl.ID, store.ClusterFailed, msgs.Localize(jc.Lang(), err))
		return err
	}
	return r.m.db.SetClusterStatus(context.Background(), cl.ID, store.ClusterReady, "")
}

func (r *ClusterRunner) run(ctx context.Context, jc *jobs.Context, cl store.Cluster, spec ClusterSpec, p ClusterJobParams, done *clusterResume) error {
	hostName := func(id int64) string {
		if h, err := r.m.db.HostByID(ctx, id); err == nil {
			return h.Name
		}
		return fmt.Sprintf("host-%d", id)
	}
	nodes := spec.nodes(hostName)
	if p.AddWorkers > 0 {
		// Пополнение: новые worker'ы на хосте последнего worker'а (или
		// первого control plane) с теми же размерами, номера — дальше.
		existing, _ := r.m.db.ClusterHosts(ctx, cl.ID)
		n := 0
		for _, h := range existing {
			if h.K8sRole == RoleWorker {
				n++
			}
		}
		tmpl := nodes[0]
		for _, nd := range nodes {
			if nd.Role == RoleWorker && nd.Kind == KindVM {
				tmpl = nd
			}
		}
		nodes = nil
		for i := n + 1; i <= n+p.AddWorkers; i++ {
			nd := tmpl
			nd.Name, nd.Role, nd.Kind = fmt.Sprintf("%s-w-%d", spec.Name, i), RoleWorker, KindVM
			nodes = append(nodes, nd)
		}
		for _, h := range existing {
			done.Hosts[h.Name] = h.ID
			done.Installed[h.Name] = true
		}
	}
	var cps []clusterNode
	for _, nd := range nodes {
		if nd.Role == RoleControlPlane {
			cps = append(cps, nd)
		}
	}
	// Первый control plane — из уже существующих при пополнении.
	var cp1Name string
	if len(cps) > 0 {
		cp1Name = cps[0].Name
	} else if existing, _ := r.m.db.ClusterHosts(ctx, cl.ID); len(existing) > 0 {
		cp1Name = existing[0].Name
	} else {
		return msgs.Errorf("hub.clusterNoNodes")
	}
	total := len(nodes) + 4
	if spec.NetworkMode == NetworkWireGuard {
		total++
	}
	step := 0

	// 0. Проверки — до первой машины. При продолжении после перезапуска
	// не повторяются: машины уже есть, и «имя занято» было бы ложью.
	step++
	jc.StepKey(step, total, "hub.clusterStepPreflight")
	if len(done.Hosts) == 0 && p.AddWorkers == 0 {
		failed, err := r.runPreflight(ctx, jc, spec, false, cl.ID)
		if err != nil {
			return err
		}
		if len(failed) > 0 {
			// Ничего ещё не создано — запись не нужна: иначе в списке
			// висит «кластер» без единой машины, а следующая попытка
			// спотыкается о занятое имя.
			_ = r.m.db.DeleteCluster(context.Background(), cl.ID)
			jc.Log("hub.clusterDiscarded", spec.Name)
			return preflightError(failed)
		}
	} else {
		jc.Log("hub.preflightSkipped")
	}

	// 0.5. Туннель между хостами: машины на каждом хосте — в своей сети
	// libvirt, подсети соседей — через WireGuard.
	var plan *wgPlan
	if spec.NetworkMode == NetworkWireGuard {
		step++
		jc.StepKey(step, total, "hub.clusterStepMesh")
		var err error
		if done.MeshUp {
			if plan, err = r.m.clusterWG(cl); err != nil {
				return err
			}
			if plan == nil {
				return msgs.Errorf("hub.clusterMeshMissing")
			}
			jc.Log("hub.clusterMeshExists", plan.Iface)
		} else {
			if plan, err = r.setupMesh(ctx, jc, cl, spec, nodes); err != nil {
				return err
			}
			done.MeshUp = true
			jc.SaveResume(*done)
		}
		for i := range nodes {
			if nodes[i].Kind == KindVM {
				nodes[i].Network, nodes[i].Bridge = plan.Iface, ""
			}
		}
	}
	// nodeAddr — адрес узла для остальных: у узла-хоста в туннеле — его
	// адрес в туннеле, иначе обычный.
	nodeAddr := func(h store.Host) string {
		if plan != nil && h.ParentID == 0 {
			if wh := plan.host(h.ID); wh != nil {
				return wh.IP
			}
		}
		return h.Addr
	}

	// 1. Узлы: машины — по одной, сам хост — просто запись.
	for _, nd := range nodes {
		step++
		jc.StepKey(step, total, "hub.clusterStepVM", nd.Name)
		if id, ok := done.Hosts[nd.Name]; ok && id != 0 {
			if _, err := r.m.db.HostByID(ctx, id); err == nil {
				jc.Log("hub.clusterVMExists", nd.Name)
				continue
			}
		}
		if nd.Kind == KindHost {
			h, err := r.m.db.HostByID(ctx, nd.HostID)
			if err != nil {
				return err
			}
			_ = r.m.db.SetHostCluster(ctx, h.ID, cl.ID, nd.Role)
			done.Hosts[nd.Name] = h.ID
			jc.SaveResume(*done)
			jc.Log("hub.clusterHostNode", h.Name, nd.Role)
			continue
		}
		if err := r.createNode(ctx, jc, spec, cl.ID, nd, done); err != nil {
			return err
		}
	}

	// 2. Control plane и токен.
	step++
	jc.StepKey(step, total, "hub.clusterStepControlPlane")
	if spec.Flavor == k8s.FlavorKubeadm && spec.K8sVersion == "" {
		// Ветка pkgs.k8s.io — одна на весь кластер, записывается в spec.
		spec.K8sVersion = r.m.k8sStableMinor(ctx)
		if raw, err := json.Marshal(spec); err == nil {
			_ = r.m.db.SetClusterSpec(ctx, cl.ID, string(raw))
		}
	}
	if spec.K8sVersion != "" {
		jc.Log("hub.clusterK8sVersion", spec.K8sVersion)
	} else {
		jc.Log("hub.clusterK3sStable")
	}
	cp1, err := r.m.db.HostByID(ctx, done.Hosts[cp1Name])
	if err != nil {
		return err
	}
	cpHost, err := r.m.db.HostByID(ctx, cl.HostID)
	if err != nil {
		return err
	}
	if cp1.ParentID == 0 {
		// Узел — сам хост: проброс не нужен, адрес API — его собственный.
		cpHost = cp1
	}
	cp1Addr := nodeAddr(cp1)
	tlsSANs := []string{cp1Addr}
	if cp1.Addr != cp1Addr {
		tlsSANs = append(tlsSANs, cp1.Addr)
	}
	if spec.Expose && cpHost.Addr != "" && cpHost.ID != cp1.ID {
		tlsSANs = append(tlsSANs, cpHost.Addr)
	}
	if !done.Installed[cp1Name] {
		is := k8s.InstallSpec{
			Flavor: spec.Flavor, Role: k8s.RoleServer, Single: len(nodes) == 1 && p.AddWorkers == 0,
			ClusterInit: len(cps) == 3, TLSSANs: tlsSANs, NodeName: cp1Name,
			CNI: spec.CNI, KubeProxyReplacement: spec.KubeProxyReplacement, APIAddr: cp1Addr, Version: spec.K8sVersion,
		}
		if cp1.ParentID == 0 && cp1Addr != cp1.Addr {
			is.NodeIP = cp1Addr
		}
		if err := r.installRole(ctx, jc, cp1, is); err != nil {
			return err
		}
		done.Installed[cp1Name] = true
		jc.SaveResume(*done)
	}
	var join k8s.JoinInfo
	if _, err := r.m.HostAPI(ctx, cp1.ID, "GET", "/api/k8s/join?server="+url.QueryEscape(cp1Addr), nil, &join); err != nil {
		return msgs.Errorf("hub.clusterJoinInfo", err)
	}
	jc.Log("hub.clusterJoinReady", cp1.Name)

	// 3. Остальные узлы: сначала control plane, потом worker'ы (nodes уже
	// в этом порядке).
	step++
	jc.StepKey(step, total, "hub.clusterStepJoin")
	for _, nd := range nodes {
		if nd.Name == cp1Name || done.Installed[nd.Name] {
			continue
		}
		h, err := r.m.db.HostByID(ctx, done.Hosts[nd.Name])
		if err != nil {
			return err
		}
		is := k8s.InstallSpec{Flavor: spec.Flavor, Role: k8s.RoleAgent, ServerURL: join.ServerURL, Token: join.Token, CAHash: join.CAHash, NodeName: nd.Name,
			CNI: spec.CNI, KubeProxyReplacement: spec.KubeProxyReplacement, APIAddr: cp1Addr, Version: spec.K8sVersion}
		if h.ParentID == 0 && nodeAddr(h) != h.Addr {
			is.NodeIP = nodeAddr(h)
		}
		if nd.Role == RoleControlPlane {
			is.Role, is.CertKey, is.TLSSANs = k8s.RoleServer, join.CertKey, tlsSANs
		}
		if err := r.installRole(ctx, jc, h, is); err != nil {
			return err
		}
		done.Installed[nd.Name] = true
		jc.SaveResume(*done)
	}

	// 4. Ready, проброс, kubeconfig.
	step++
	jc.StepKey(step, total, "hub.clusterStepReady")
	want := len(done.Hosts)
	if err := r.waitNodesReady(ctx, jc, cp1, want); err != nil {
		return err
	}
	if spec.Expose && !done.Exposed && cp1.ParentID != 0 {
		if _, err := r.m.HostAPI(ctx, cpHost.ID, "POST", "/api/vm/portforward",
			map[string]any{"name": cp1.Name, "ip": cp1.Addr, "rules": spec.exposeRules()}, nil); err != nil {
			return msgs.Errorf("hub.clusterExpose", err)
		}
		done.Exposed = true
		jc.SaveResume(*done)
		jc.Log("hub.clusterExposed", cpHost.Addr, cp1.Addr, exposeSummary(spec))
	}
	serverAddr := spec.apiAddr(cpHost.Addr, cp1.Addr)
	if cp1.ParentID == 0 {
		// Узел-хост: kubeconfig — на его обычный адрес, он в SAN.
		serverAddr = cp1.Addr + ":6443"
	}
	var kubeconfig string
	code, err := r.m.HostAPI(ctx, cp1.ID, "GET", "/api/k8s/kubeconfig?server="+url.QueryEscape(serverAddr), nil, &kubeconfig)
	if err != nil || code != 200 {
		return msgs.Errorf("hub.clusterKubeconfig", err, code)
	}
	enc, err := secretbox.Encrypt(r.m.key, []byte(kubeconfig))
	if err != nil {
		return err
	}
	if err := r.m.db.SetClusterKubeconfig(ctx, cl.ID, serverAddr, enc); err != nil {
		return err
	}
	if p.AddWorkers > 0 {
		_ = r.m.db.SetClusterWorkers(ctx, cl.ID, cl.Workers+p.AddWorkers)
	}
	jc.Log("hub.clusterDone", spec.Name, serverAddr)
	return nil
}

// K8sVersions — актуальная минорная версия и три предыдущие (столько
// веток поддерживает Kubernetes; у k3s каналы на них тоже есть).
func (m *Manager) K8sVersions(ctx context.Context) (stable string, versions []string) {
	stable = m.k8sStableMinor(ctx)
	parts := strings.Split(stable, ".")
	major, minor := parts[0], 0
	fmt.Sscanf(parts[1], "%d", &minor)
	for i := 0; i < 4 && minor-i >= 0; i++ {
		versions = append(versions, fmt.Sprintf("%s.%d", major, minor-i))
	}
	return stable, versions
}

// k8sStableMinor — актуальная минорная версия Kubernetes по
// dl.k8s.io/release/stable.txt («v1.36.2» → «1.36»); недоступно —
// k8s.DefaultKubeadmVersion. Кэшируется на час.
func (m *Manager) k8sStableMinor(ctx context.Context) string {
	m.k8sStableMu.Lock()
	defer m.k8sStableMu.Unlock()
	if m.k8sStable != "" && time.Since(m.k8sStableAt) < time.Hour {
		return m.k8sStable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://dl.k8s.io/release/stable.txt", nil)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64)); resp.StatusCode == 200 {
			v := strings.TrimPrefix(strings.TrimSpace(string(raw)), "v")
			if parts := strings.Split(v, "."); len(parts) >= 2 && k8sMinorRe.MatchString(parts[0]+"."+parts[1]) {
				m.k8sStable, m.k8sStableAt = parts[0]+"."+parts[1], time.Now()
				return m.k8sStable
			}
		}
	}
	return k8s.DefaultKubeadmVersion
}

// exposeSummary — «16443→6443, 80→80» для журнала.
func exposeSummary(s ClusterSpec) string {
	var parts []string
	for _, r := range s.exposeRules() {
		parts = append(parts, fmt.Sprintf("%d→%d", r["host_port"], r["vm_port"]))
	}
	return strings.Join(parts, ", ")
}

// createNode создаёт машину заданием VMProvision и ждёт его.
func (r *ClusterRunner) createNode(ctx context.Context, jc *jobs.Context, spec ClusterSpec, clusterID int64, nd clusterNode, done *clusterResume) error {
	host, err := r.m.db.HostByID(ctx, nd.HostID)
	if err != nil {
		return err
	}
	imageID := nd.ImageID
	if name := hubImageName(imageID); name != "" {
		// Свой образ из библиотеки хаба: на хост — если его там ещё нет.
		img, err := r.m.ClusterImage(name)
		if err != nil {
			return err
		}
		if err := r.m.ensureHostImage(ctx, jc, host.ID, host.Name, img); err != nil {
			return err
		}
		imageID = vmcreate.HostPrefix + name
	}
	vm := vmcreate.Spec{Name: nd.Name, ImageID: imageID, Network: nd.Network, Bridge: nd.Bridge, User: spec.User, Autostart: true,
		VCPUs: nd.VCPUs, MemoryMB: nd.MemMB, DiskGB: nd.DiskGB}
	if nd.Bridge != "" {
		// На мосту адрес машины узнаётся через гостевого агента —
		// cloud-init ставит его при первом запуске.
		vm.Packages = append(vm.Packages, "qemu-guest-agent")
	}
	if r.s.jobs == nil {
		return msgs.Errorf("api.backgroundJobsAreUnavailable")
	}
	jobID, err := r.s.jobs.Start(ctx, jobs.Spec{
		Kind: KindVMProvision, TitleKey: "hub.machineOnHostJobTitle", TitleArgs: []any{nd.Name, host.Name},
		Queue: fmt.Sprintf("vm:%d", host.ID), Author: jc.Job.Author, Steps: 5,
		Params: VMProvisionParams{HostID: host.ID, Spec: vm, InstallNKT: true},
	})
	if err != nil {
		return err
	}
	sr := &ScriptRunner{s: r.s, m: r.m}
	if err := sr.waitHubJob(ctx, jc, jobID); err != nil {
		return err
	}
	hosts, err := r.m.db.ListHosts(ctx)
	if err != nil {
		return err
	}
	for _, h := range hosts {
		if h.Name == nd.Name && h.ParentID == host.ID {
			_ = r.m.db.SetHostCluster(ctx, h.ID, clusterID, nd.Role)
			done.Hosts[nd.Name] = h.ID
			jc.SaveResume(*done)
			return nil
		}
	}
	return msgs.Errorf("hub.clusterVMMissing", nd.Name)
}

// installRole ставит роль заданием хоста и ждёт его.
func (r *ClusterRunner) installRole(ctx context.Context, jc *jobs.Context, h store.Host, spec k8s.InstallSpec) error {
	// Шаги установки выполняет nkt на самом узле — своей версии. Узел,
	// созданный старым хабом, или «железный» хост с отставшим nkt получат
	// старые скрипты; перед ролью узел обновляется до версии хаба.
	if cur, err := r.m.db.HostByID(ctx, h.ID); err == nil {
		h = cur
	}
	if h.NktVersion != "" && h.NktVersion != r.m.Version() {
		jc.Log("hub.clusterNodeUpdate", h.Name, h.NktVersion, r.m.Version())
		vr := &VMProvisionRunner{m: r.m}
		if err := vr.runInstall(ctx, jc, h.ID); err != nil {
			return msgs.Errorf("hub.clusterNodeUpdateFailed", h.Name, err)
		}
	}
	jc.Log("hub.clusterInstalling", spec.Flavor, spec.Role, h.Name)
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/k8s/install", spec, &started); err != nil {
		return msgs.Errorf("hub.clusterInstallStart", h.Name, err)
	}
	g := &GroupApplyRunner{m: r.m}
	return g.waitHostJob(ctx, jc, h, started.JobID)
}

// waitNodesReady опрашивает control plane, пока все узлы не станут Ready.
func (r *ClusterRunner) waitNodesReady(ctx context.Context, jc *jobs.Context, cp store.Host, want int) error {
	deadline := time.Now().Add(clusterReadyTimeout)
	lastMsg := ""
	for {
		var res struct {
			Nodes []k8s.Node `json:"nodes"`
		}
		if _, err := r.m.HostAPI(ctx, cp.ID, "GET", "/api/k8s", nil, &res); err == nil {
			ready := 0
			for _, n := range res.Nodes {
				if n.Ready {
					ready++
				}
			}
			msg := fmt.Sprintf("%d/%d", ready, want)
			if msg != lastMsg {
				jc.Log("hub.clusterNodesReady", ready, want)
				lastMsg = msg
			}
			if ready >= want && len(res.Nodes) >= want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.clusterReadyTimeout", clusterReadyTimeout)
		}
		if !sleepCtx(ctx, clusterNodePoll) {
			return ctx.Err()
		}
	}
}

// ClusterDeleteRunner удаляет узлы (машины с дисками) и запись.
type ClusterDeleteRunner struct{ m *Manager }

func NewClusterDeleteRunner(m *Manager) *ClusterDeleteRunner { return &ClusterDeleteRunner{m: m} }

func (r *ClusterDeleteRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ClusterJobParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	cl, err := r.m.db.ClusterByID(ctx, p.ClusterID)
	if err != nil {
		return err
	}
	hosts, err := r.m.db.ClusterHosts(ctx, cl.ID)
	if err != nil {
		return err
	}
	// Машины, заведённые провалившимся созданием, к кластеру привязаться
	// не успели, но по имени и родителю — его: убираются вместе с ним.
	if all, err := r.m.db.ListHosts(ctx); err == nil {
		known := map[int64]bool{}
		for _, h := range hosts {
			known[h.ID] = true
		}
		for _, h := range all {
			if !known[h.ID] && h.ParentID == cl.HostID && (strings.HasPrefix(h.Name, cl.Name+"-cp-") || strings.HasPrefix(h.Name, cl.Name+"-w-")) {
				hosts = append(hosts, h)
			}
		}
	}
	total := len(hosts) + 1
	for i, h := range hosts {
		jc.StepKey(i+1, total, "hub.clusterStepDeleteVM", h.Name)
		if h.ParentID == 0 {
			// Узел-хост: Kubernetes убирается, запись хоста остаётся.
			if _, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/k8s/uninstall", nil, nil); err != nil {
				jc.Log("hub.clusterDeleteVMWarn", h.Name, err)
			}
			_ = r.m.db.SetHostCluster(ctx, h.ID, 0, "")
			jc.Log("hub.clusterHostNodeRemoved", h.Name)
			continue
		}
		if h.ParentID != 0 {
			path := "/api/vms/" + url.PathEscape(h.Name) + "?remove_storage=true&force=true"
			if _, err := r.m.HostAPI(ctx, h.ParentID, "DELETE", path, nil, nil); err != nil {
				jc.Log("hub.clusterDeleteVMWarn", h.Name, err)
			}
			if cl.Expose {
				_, _ = r.m.HostAPI(ctx, h.ParentID, "DELETE", "/api/vm/portforward/"+url.PathEscape(h.Name), nil, nil)
			}
		}
		r.m.CloseHost(h.ID)
		if err := r.m.db.DeleteHost(ctx, h.ID); err != nil {
			return err
		}
	}
	jc.StepKey(total, total, "hub.clusterStepDeleteRecord")
	r.removeMesh(ctx, jc, cl)
	if err := r.m.db.DeleteCluster(ctx, cl.ID); err != nil {
		return err
	}
	jc.Log("hub.clusterDeleted", cl.Name)
	return nil
}

// Kubeconfig расшифровывает сохранённый kubeconfig.
func (m *Manager) ClusterKubeconfig(ctx context.Context, id int64) (string, error) {
	cl, err := m.db.ClusterByID(ctx, id)
	if err != nil {
		return "", err
	}
	if len(cl.KubeconfigEnc) == 0 {
		return "", msgs.Errorf("hub.clusterNoKubeconfig")
	}
	raw, err := secretbox.Decrypt(m.key, cl.KubeconfigEnc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
