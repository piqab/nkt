package hub

import (
	"context"
	"encoding/json"
	"fmt"
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
	ImageID  string `json:"image_id"`
	Network  string `json:"network,omitempty"`
	User     string `json:"user"`
	// Размеры control plane и worker'ов.
	CPVCPUs    int `json:"cp_vcpus"`
	CPMemoryMB int `json:"cp_memory_mb"`
	CPDiskGB   int `json:"cp_disk_gb"`
	WVCPUs     int `json:"w_vcpus"`
	WMemoryMB  int `json:"w_memory_mb"`
	WDiskGB    int `json:"w_disk_gb"`
}

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

// ControlPlanes — сколько control plane в топологии.
func (s ClusterSpec) ControlPlanes() int {
	if s.Topology == TopologyCP3 {
		return 3
	}
	return 1
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
		host, err := r.m.db.HostByID(ctx, p.Spec.HostID)
		if err != nil {
			return err
		}
		jc.Step(1, 1, msgs.T(jc.Lang(), "hub.clusterStepPreflight"))
		failed, err := r.runPreflight(ctx, jc, host, *p.Spec, p.Prepare)
		if err != nil {
			return err
		}
		if failed > 0 {
			return msgs.Errorf("hub.preflightFailed", failed)
		}
		return nil
	}
	cl, err := r.m.db.ClusterByID(ctx, p.ClusterID)
	if err != nil {
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
	host, err := r.m.db.HostByID(ctx, cl.HostID)
	if err != nil {
		return err
	}
	cps, workers := spec.nodeNames()
	if p.AddWorkers > 0 {
		// Пополнение: новые worker'ы получают следующие номера.
		existing, _ := r.m.db.ClusterHosts(ctx, cl.ID)
		n := 0
		for _, h := range existing {
			if h.K8sRole == "worker" {
				n++
			}
		}
		workers = nil
		for i := n + 1; i <= n+p.AddWorkers; i++ {
			workers = append(workers, fmt.Sprintf("%s-w-%d", spec.Name, i))
		}
		for _, h := range existing {
			done.Hosts[h.Name] = h.ID
			done.Installed[h.Name] = true
		}
	}
	all := append(append([]string{}, cps...), workers...)
	total := len(all) + 4
	step := 0

	// 0. Проверки — до первой машины. При продолжении после перезапуска
	// не повторяются: машины уже есть, и «имя занято» было бы ложью.
	step++
	jc.Step(step, total, msgs.T(jc.Lang(), "hub.clusterStepPreflight"))
	if len(done.Hosts) == 0 && p.AddWorkers == 0 {
		failed, err := r.runPreflight(ctx, jc, host, spec, false)
		if err != nil {
			return err
		}
		if failed > 0 {
			return msgs.Errorf("hub.preflightFailed", failed)
		}
	} else {
		jc.Log("hub.preflightSkipped")
	}

	// 1. Машины: по одной, с установкой nkt — как «Новая машина».
	for _, name := range all {
		step++
		jc.Step(step, total, msgs.T(jc.Lang(), "hub.clusterStepVM", name))
		if id, ok := done.Hosts[name]; ok && id != 0 {
			if done.Installed[name] {
				jc.Log("hub.clusterVMExists", name)
				continue
			}
			if _, err := r.m.db.HostByID(ctx, id); err == nil {
				jc.Log("hub.clusterVMExists", name)
				continue
			}
		}
		if err := r.createNode(ctx, jc, host, spec, cl.ID, name, isCP(name, cps), done); err != nil {
			return err
		}
	}

	// 2. Control plane и токен.
	step++
	jc.Step(step, total, msgs.T(jc.Lang(), "hub.clusterStepControlPlane"))
	cp1, err := r.m.db.HostByID(ctx, done.Hosts[cps[0]])
	if err != nil {
		return err
	}
	tlsSANs := []string{cp1.Addr}
	if spec.Expose && host.Addr != "" {
		tlsSANs = append(tlsSANs, host.Addr)
	}
	if !done.Installed[cps[0]] {
		if err := r.installRole(ctx, jc, cp1, k8s.InstallSpec{
			Flavor: spec.Flavor, Role: k8s.RoleServer, Single: spec.Topology == TopologySingle,
			ClusterInit: spec.Topology == TopologyCP3, TLSSANs: tlsSANs, NodeName: cps[0],
		}); err != nil {
			return err
		}
		done.Installed[cps[0]] = true
		jc.SaveResume(*done)
	}
	var join k8s.JoinInfo
	if _, err := r.m.HostAPI(ctx, cp1.ID, "GET", "/api/k8s/join?server="+url.QueryEscape(cp1.Addr), nil, &join); err != nil {
		return msgs.Errorf("hub.clusterJoinInfo", err)
	}
	jc.Log("hub.clusterJoinReady", cp1.Name)

	// 3. Остальные узлы.
	step++
	jc.Step(step, total, msgs.T(jc.Lang(), "hub.clusterStepJoin"))
	for _, name := range cps[1:] {
		if done.Installed[name] {
			continue
		}
		h, err := r.m.db.HostByID(ctx, done.Hosts[name])
		if err != nil {
			return err
		}
		if err := r.installRole(ctx, jc, h, k8s.InstallSpec{
			Flavor: spec.Flavor, Role: k8s.RoleServer, ServerURL: join.ServerURL, Token: join.Token,
			CAHash: join.CAHash, CertKey: join.CertKey, TLSSANs: tlsSANs, NodeName: name,
		}); err != nil {
			return err
		}
		done.Installed[name] = true
		jc.SaveResume(*done)
	}
	for _, name := range workers {
		if done.Installed[name] {
			continue
		}
		h, err := r.m.db.HostByID(ctx, done.Hosts[name])
		if err != nil {
			return err
		}
		if err := r.installRole(ctx, jc, h, k8s.InstallSpec{
			Flavor: spec.Flavor, Role: k8s.RoleAgent, ServerURL: join.ServerURL, Token: join.Token, CAHash: join.CAHash, NodeName: name,
		}); err != nil {
			return err
		}
		done.Installed[name] = true
		jc.SaveResume(*done)
	}

	// 4. Ready, проброс, kubeconfig.
	step++
	jc.Step(step, total, msgs.T(jc.Lang(), "hub.clusterStepReady"))
	want := len(done.Hosts)
	if err := r.waitNodesReady(ctx, jc, cp1, want); err != nil {
		return err
	}
	serverAddr := cp1.Addr
	if spec.Expose && !done.Exposed {
		if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/vm/portforward",
			map[string]any{"name": cp1.Name, "ip": cp1.Addr, "ports": []int{6443, 80, 443}}, nil); err != nil {
			return msgs.Errorf("hub.clusterExpose", err)
		}
		done.Exposed = true
		jc.SaveResume(*done)
		jc.Log("hub.clusterExposed", host.Addr, cp1.Addr)
	}
	if spec.Expose {
		serverAddr = host.Addr
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

func isCP(name string, cps []string) bool {
	for _, c := range cps {
		if c == name {
			return true
		}
	}
	return false
}

// createNode создаёт машину заданием VMProvision и ждёт его.
func (r *ClusterRunner) createNode(ctx context.Context, jc *jobs.Context, host store.Host, spec ClusterSpec, clusterID int64, name string, cp bool, done *clusterResume) error {
	vm := vmcreate.Spec{Name: name, ImageID: spec.ImageID, Network: spec.Network, User: spec.User, Autostart: true}
	if cp {
		vm.VCPUs, vm.MemoryMB, vm.DiskGB = spec.CPVCPUs, spec.CPMemoryMB, spec.CPDiskGB
	} else {
		vm.VCPUs, vm.MemoryMB, vm.DiskGB = spec.WVCPUs, spec.WMemoryMB, spec.WDiskGB
	}
	if r.s.jobs == nil {
		return msgs.Errorf("api.backgroundJobsAreUnavailable")
	}
	jobID, err := r.s.jobs.Start(ctx, jobs.Spec{
		Kind: KindVMProvision, Title: msgs.Tc(ctx, "hub.machineOnHostJobTitle", name, host.Name),
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
		if h.Name == name && h.ParentID == host.ID {
			role := "worker"
			if cp {
				role = "control-plane"
			}
			_ = r.m.db.SetHostCluster(ctx, h.ID, clusterID, role)
			_ = r.m.db.SetHostGroup(ctx, h.ID, spec.Name)
			done.Hosts[name] = h.ID
			jc.SaveResume(*done)
			return nil
		}
	}
	return msgs.Errorf("hub.clusterVMMissing", name)
}

// installRole ставит роль заданием хоста и ждёт его.
func (r *ClusterRunner) installRole(ctx context.Context, jc *jobs.Context, h store.Host, spec k8s.InstallSpec) error {
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
		jc.Step(i+1, total, msgs.T(jc.Lang(), "hub.clusterStepDeleteVM", h.Name))
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
	jc.Step(total, total, msgs.T(jc.Lang(), "hub.clusterStepDeleteRecord"))
	_ = r.m.db.DeleteHostGroup(ctx, cl.Name)
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
