package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
	"github.com/piqab/nkt/internal/vmnet"
)

// Свой кластер токена (1.17.9): k3s на машинах токена — одна машина
// (single) или control plane и N worker'ов (cp1), все на одном хосте, в
// сети токена под nkt_isolation. Узлы — машины токена (общий счёт и
// потолки), кластеров — не больше потолка «кластеров». API кластера
// снаружи — порт из пула хоста (пула нет — отказ); kubeconfig
// (cluster-admin своего кластера) отдаётся один раз. Те же правила — и
// у «k8s … create» в сценарии от токена.

const ownClusterKV = "k8s.owncl"

// OwnCluster — кластер токена.
type OwnCluster struct {
	ClusterID int64  `json:"cluster_id"`
	Name      string `json:"name"`
	TokenID   int64  `json:"token_id"`
	Token     string `json:"token"`
	// Issued — kubeconfig уже выдан.
	Issued bool `json:"issued,omitempty"`
}

var ownClusterMu sync.Mutex

func (s *Server) ownClusters(ctx context.Context) []OwnCluster {
	var out []OwnCluster
	if raw, ok, _ := s.db.KVGet(ctx, ownClusterKV); ok {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) saveOwnClusters(ctx context.Context, list []OwnCluster) error {
	raw, _ := json.Marshal(list)
	return s.db.KVSet(ctx, ownClusterKV, string(raw))
}

// tokenClusterPrepare — описание кластера токена по его правилам: k3s,
// single или cp1, один хост (0 — выбрать), сеть токена, проброс только API
// портом из пула; потолки «кластеров» и машин; учёт узлов и порт
// записываются сразу (не создалось — их снимет удаление кластера).
func (s *Server) tokenClusterPrepare(ctx context.Context, t store.APIToken, spec *ClusterSpec) error {
	if spec.Topology != TopologySingle && spec.Topology != TopologyCP1 || len(spec.Placements) > 0 || spec.NetworkMode != "" && spec.NetworkMode != NetworkNAT {
		return msgs.Errorf("hub.ownClusterTopology")
	}
	spec.Flavor, spec.NetworkMode, spec.Network, spec.CNI = "k3s", NetworkNAT, vmnet.IsolatedName(t.ID), "cilium"
	if spec.Topology == TopologySingle {
		spec.Workers = 0
	}
	// Образ — из каталога: библиотечный образ хаба ложится на хост файлом
	// host:…, а такие токену нельзя.
	if err := tokenVMImage(vmcreate.Spec{ImageID: spec.ImageID}); err != nil || hubImageName(spec.ImageID) != "" {
		return msgs.Errorf("auth.tokenVMHostImage")
	}
	ownClusterMu.Lock()
	n := 0
	for _, o := range s.ownClusters(ctx) {
		if o.TokenID == t.ID {
			n++
		}
	}
	ownClusterMu.Unlock()
	if n >= t.Perms.Limits.K8sClusters {
		return msgs.Errorf("auth.tokenClusters", t.Perms.Limits.K8sClusters)
	}
	cps, workers := spec.nodeNames()
	sum := vmcreate.Spec{Name: spec.Name, VCPUs: spec.CPVCPUs*len(cps) + spec.WVCPUs*len(workers),
		MemoryMB: spec.CPMemoryMB*len(cps) + spec.WMemoryMB*len(workers), DiskGB: spec.CPDiskGB*len(cps) + spec.WDiskGB*len(workers)}
	if spec.HostID == 0 {
		h, err := s.pickVMHost(ctx, sum)
		if err != nil {
			return err
		}
		spec.HostID = h.ID
	} else if !s.tokenHostAllowed(ctx, t, strconv.FormatInt(spec.HostID, 10)) {
		return msgs.Errorf("auth.tokenHostDenied")
	}
	host, err := s.db.HostByID(ctx, spec.HostID)
	if err != nil {
		return err
	}
	// Каждый узел — машина токена в его потолках.
	var adds []store.TokenUsage
	l := t.Perms.Limits
	node := func(name string, cpu, mem, disk int) error {
		for _, c := range []struct {
			what       string
			got, limit int64
		}{{"vcpus", int64(cpu), int64(l.VMCPU)}, {"memory_mb", int64(mem), l.VMMemMB}, {"disk_gb", int64(disk), l.VMDiskGB}} {
			if c.limit > 0 && c.got > c.limit {
				return msgs.Errorf("hub.tokenVMSpec", name, c.what, c.got, c.limit)
			}
		}
		adds = append(adds, store.TokenUsage{TokenID: t.ID, Kind: store.UsageVM, HostID: host.ID, Ref: name, Label: name + "@" + host.Name,
			CPU: float64(cpu), MemMB: int64(mem), DiskGB: int64(disk)})
		return nil
	}
	for _, name := range cps {
		if err := node(name, spec.CPVCPUs, spec.CPMemoryMB, spec.CPDiskGB); err != nil {
			return err
		}
	}
	for _, name := range workers {
		if err := node(name, spec.WVCPUs, spec.WMemoryMB, spec.WDiskGB); err != nil {
			return err
		}
	}
	if err := s.tokenFits(ctx, t, adds...); err != nil {
		return err
	}
	// API наружу — порт пула хоста.
	portsMu.Lock()
	defer portsMu.Unlock()
	pool, ok := s.portPools(ctx)[host.Name]
	if !ok || pool.From == 0 {
		return msgs.Errorf("hub.ownClusterNoPool", host.Name)
	}
	allocs := s.portAllocs(ctx)
	busy := map[int]bool{}
	for _, a := range allocs {
		if a.Host == host.Name {
			busy[a.Port] = true
		}
	}
	port := 0
	for p := pool.From; p <= pool.To && port == 0; p++ {
		if !busy[p] {
			port = p
		}
	}
	if port == 0 {
		return msgs.Errorf("hub.portPoolFull", host.Name)
	}
	spec.Expose, spec.ExposeAPI, spec.ExposeHTTP, spec.ExposeHTTPS = true, port, 0, 0
	if err := spec.Validate(); err != nil {
		return err
	}
	for _, u := range adds {
		if err := s.db.SetTokenUsage(ctx, u); err != nil {
			return err
		}
	}
	return s.savePortAllocs(ctx, append(allocs, PortAlloc{Host: host.Name, Port: port, Proto: "tcp", VM: cps[0], VMPort: 6443, Token: t.Name}))
}

// releaseClusterToken снимает учёт узлов и порты кластера (перед его
// удалением и после неудачного создания записи).
func releaseClusterToken(ctx context.Context, db *store.DB, spec ClusterSpec, hostName string) {
	cps, workers := spec.nodeNames()
	names := append(cps, workers...)
	for _, n := range names {
		_ = db.DeleteTokenUsage(ctx, 0, store.UsageVM, spec.HostID, n)
		_ = db.DeleteTokenUsagePrefix(ctx, store.UsageSnapshot, spec.HostID, n+"/")
	}
	portsMu.Lock()
	defer portsMu.Unlock()
	var allocs []PortAlloc
	if raw, ok, _ := db.KVGet(ctx, portAllocKV); ok {
		_ = json.Unmarshal([]byte(raw), &allocs)
	}
	kept := slices.DeleteFunc(allocs, func(a PortAlloc) bool { return a.Host == hostName && slices.Contains(names, a.VM) })
	raw, _ := json.Marshal(kept)
	_ = db.KVSet(ctx, portAllocKV, string(raw))
}

// handleOwnClusters — GET /hub/k8s/clusters/own: кластеры токена.
func (s *Server) handleOwnClusters(w http.ResponseWriter, r *http.Request) {
	t, ok := tokenFromContext(r.Context())
	if !ok || t.Role != store.TokenRoleCustom {
		writeError(w, http.StatusForbidden, msgs.Tc(r.Context(), "auth.tokenOnly"))
		return
	}
	type row struct {
		OwnCluster
		Status string `json:"status"`
		Server string `json:"server,omitempty"`
	}
	out := []row{}
	for _, o := range s.ownClusters(r.Context()) {
		if o.TokenID != t.ID {
			continue
		}
		rw := row{OwnCluster: o}
		if cl, err := s.db.ClusterByID(r.Context(), o.ClusterID); err == nil {
			rw.Status, rw.Server = cl.Status, cl.ServerAddr
		}
		out = append(out, rw)
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": out})
}

// handleOwnClusterCreate — POST /hub/k8s/clusters/own (ClusterSpec:
// name, topology single|cp1, workers, host_id 0 — выбрать, image_id, user,
// размеры cp_* и w_*) → id, job_id.
func (s *Server) handleOwnClusterCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, ok := tokenFromContext(ctx)
	if !ok || t.Role != store.TokenRoleCustom {
		writeError(w, http.StatusForbidden, msgs.Tc(ctx, "auth.tokenOnly"))
		return
	}
	var spec ClusterSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if spec.User == "" {
		spec.User = "deploy"
	}
	if err := s.tokenClusterPrepare(ctx, t, &spec); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	host, _ := s.db.HostByID(ctx, spec.HostID)
	id, jobID, err := s.startOwnCluster(ctx, t, spec, host, auth.Username(ctx))
	if err != nil {
		releaseClusterToken(context.WithoutCancel(ctx), s.db, spec, host.Name)
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "job_id": jobID, "api_port": spec.ExposeAPI, "address": host.Addr})
}

// startOwnCluster — запись кластера, владелец и задание создания.
func (s *Server) startOwnCluster(ctx context.Context, t store.APIToken, spec ClusterSpec, host store.Host, author string) (int64, int64, error) {
	raw, _ := json.Marshal(spec)
	_, workers := spec.NodeCounts()
	id, err := s.db.CreateCluster(ctx, store.Cluster{Name: spec.Name, HostID: host.ID, Flavor: spec.Flavor,
		Topology: spec.Topology, Workers: workers, Expose: spec.Expose, SpecJSON: string(raw)})
	if err != nil {
		return 0, 0, msgs.Errorf("hub.clusterCreate", err)
	}
	ownClusterMu.Lock()
	list := append(s.ownClusters(ctx), OwnCluster{ClusterID: id, Name: spec.Name, TokenID: t.ID, Token: t.Name})
	err = s.saveOwnClusters(ctx, list)
	ownClusterMu.Unlock()
	if err != nil {
		return 0, 0, err
	}
	jobID, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindClusterCreate, TitleKey: "hub.clusterJobTitle", TitleArgs: []any{spec.Name, host.Name},
		Queue: fmt.Sprintf("cluster:%d", id), Author: author, Steps: spec.jobSteps(),
		Params: ClusterJobParams{ClusterID: id},
	})
	if err != nil {
		_ = s.db.DeleteCluster(ctx, id)
		return 0, 0, err
	}
	s.db.Audit(ctx, author, "cluster.create", spec.Name, "ok", host.Name)
	return id, jobID, nil
}

// ownCluster — кластер токена по {id}.
func (s *Server) ownCluster(w http.ResponseWriter, r *http.Request) (store.APIToken, store.Cluster, bool) {
	ctx := r.Context()
	t, ok := tokenFromContext(ctx)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if ok && t.Role == store.TokenRoleCustom && slices.ContainsFunc(s.ownClusters(ctx), func(o OwnCluster) bool { return o.TokenID == t.ID && o.ClusterID == id }) {
		if cl, err := s.db.ClusterByID(ctx, id); err == nil {
			return t, cl, true
		}
	}
	writeErr(w, r, http.StatusForbidden, msgs.Errorf("hub.ownClusterNotOwn", id))
	return t, store.Cluster{}, false
}

// handleOwnClusterKubeconfig — POST /hub/k8s/clusters/own/{id}/kubeconfig:
// kubeconfig кластера, один раз (после готовности).
func (s *Server) handleOwnClusterKubeconfig(w http.ResponseWriter, r *http.Request) {
	_, cl, ok := s.ownCluster(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ownClusterMu.Lock()
	defer ownClusterMu.Unlock()
	list := s.ownClusters(ctx)
	i := slices.IndexFunc(list, func(o OwnCluster) bool { return o.ClusterID == cl.ID })
	if list[i].Issued {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.ownClusterIssued", cl.Name))
		return
	}
	cfg, err := s.hub.ClusterKubeconfig(ctx, cl.ID)
	if err != nil || cfg == "" {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.ownClusterNotReady", cl.Name))
		return
	}
	list[i].Issued = true
	if err := s.saveOwnClusters(ctx, list); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, auth.Username(ctx), "cluster.kubeconfig", cl.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"kubeconfig": cfg})
}

// handleOwnClusterDelete — DELETE /hub/k8s/clusters/own/{id}: кластер с
// машинами, учёт и порт.
func (s *Server) handleOwnClusterDelete(w http.ResponseWriter, r *http.Request) {
	_, cl, ok := s.ownCluster(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	_ = s.db.SetClusterStatus(ctx, cl.ID, store.ClusterDeleting, "")
	jobID, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindClusterDelete, TitleKey: "hub.clusterDeleteJobTitle", TitleArgs: []any{cl.Name},
		Queue: fmt.Sprintf("cluster:%d", cl.ID), Author: user, Steps: 1,
		Params: ClusterJobParams{ClusterID: cl.ID},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(ctx, user, "cluster.delete", cl.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

// forgetOwnCluster — запись токена о кластере уходит вместе с ним.
func forgetOwnCluster(ctx context.Context, db *store.DB, clusterID int64) {
	ownClusterMu.Lock()
	defer ownClusterMu.Unlock()
	var list []OwnCluster
	if raw, ok, _ := db.KVGet(ctx, ownClusterKV); ok {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	kept := slices.DeleteFunc(list, func(o OwnCluster) bool { return o.ClusterID == clusterID })
	if len(kept) != len(list) {
		raw, _ := json.Marshal(kept)
		_ = db.KVSet(ctx, ownClusterKV, string(raw))
	}
}
