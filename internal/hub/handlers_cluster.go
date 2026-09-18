package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Кластеры Kubernetes (см. cluster.go): список с узлами, создание,
// добавление worker'ов, kubeconfig, удаление.

type clusterNodeJSON struct {
	HostID    int64  `json:"host_id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Addr      string `json:"addr"`
	Status    string `json:"status"`
	Reachable *bool  `json:"reachable,omitempty"`
}

type clusterJSON struct {
	store.Cluster
	HostName string            `json:"host_name"`
	Nodes    []clusterNodeJSON `json:"nodes"`
	HasKube  bool              `json:"has_kubeconfig"`
}

func (s *Server) clusterJSON(ctx context.Context, cl store.Cluster) clusterJSON {
	out := clusterJSON{Cluster: cl, Nodes: []clusterNodeJSON{}, HasKube: len(cl.KubeconfigEnc) > 0}
	if h, err := s.db.HostByID(ctx, cl.HostID); err == nil {
		out.HostName = h.Name
	}
	hosts, _ := s.db.ClusterHosts(ctx, cl.ID)
	for _, h := range hosts {
		n := clusterNodeJSON{HostID: h.ID, Name: h.Name, Role: h.K8sRole, Addr: h.Addr, Status: h.Status}
		if ov, ok := s.hub.Overview(h.ID); ok {
			r := ov.Reachable
			n.Reachable = &r
		}
		out.Nodes = append(out.Nodes, n)
	}
	return out
}

func (s *Server) handleClusterList(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListClusters(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	out := make([]clusterJSON, 0, len(list))
	for _, cl := range list {
		out = append(out, s.clusterJSON(r.Context(), cl))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleClusterCreate(w http.ResponseWriter, r *http.Request) {
	var spec ClusterSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := spec.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	host, err := s.db.HostByID(r.Context(), spec.HostID)
	if err != nil {
		fail(w, r, err)
		return
	}
	raw, _ := json.Marshal(spec)
	id, err := s.db.CreateCluster(r.Context(), store.Cluster{Name: spec.Name, HostID: host.ID, Flavor: spec.Flavor,
		Topology: spec.Topology, Workers: spec.Workers, Expose: spec.Expose, SpecJSON: string(raw)})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.clusterCreate", err))
		return
	}
	_ = s.db.CreateHostGroup(r.Context(), spec.Name)
	user := auth.Username(r.Context())
	jobID, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindClusterCreate, Title: msgs.Tc(r.Context(), "hub.clusterJobTitle", spec.Name, host.Name),
		Queue: fmt.Sprintf("cluster:%d", id), Author: user, Steps: spec.ControlPlanes() + spec.Workers + 3,
		Params: ClusterJobParams{ClusterID: id},
	})
	if err != nil {
		_ = s.db.DeleteCluster(r.Context(), id)
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "cluster.create", spec.Name, "ok", host.Name)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "job_id": jobID})
}

func clusterIDParam(r *http.Request) (int64, error) {
	var id int64
	_, err := fmt.Sscan(chi.URLParam(r, "id"), &id)
	return id, err
}

func (s *Server) handleClusterKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cfg, err := s.hub.ClusterKubeconfig(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	cl, _ := s.db.ClusterByID(r.Context(), id)
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-kubeconfig.yaml\"", cl.Name))
	_, _ = w.Write([]byte(cfg))
}

// handleClusterNodes — узлы по мнению самого кластера (kubectl на первом
// control plane).
func (s *Server) handleClusterNodes(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	hosts, err := s.db.ClusterHosts(r.Context(), id)
	if err != nil || len(hosts) == 0 {
		writeError(w, http.StatusNotFound, msgs.Tc(r.Context(), "hub.clusterNoNodes"))
		return
	}
	var res struct {
		Status k8s.Status `json:"status"`
		Nodes  []k8s.Node `json:"nodes"`
		Err    string     `json:"nodes_error"`
	}
	if _, err := s.hub.HostAPI(r.Context(), hosts[0].ID, "GET", "/api/k8s", nil, &res); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleClusterAddWorkers(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Count int `json:"count"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Count < 1 || req.Count > 10 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.clusterBadWorkerCount"))
		return
	}
	cl, err := s.db.ClusterByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	if cl.Status != store.ClusterReady {
		writeError(w, http.StatusConflict, msgs.Tc(r.Context(), "hub.clusterNotReady"))
		return
	}
	if cl.Topology == TopologySingle {
		writeError(w, http.StatusConflict, msgs.Tc(r.Context(), "hub.clusterSingleNoWorkers"))
		return
	}
	user := auth.Username(r.Context())
	_ = s.db.SetClusterStatus(r.Context(), id, store.ClusterCreating, "")
	jobID, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindClusterCreate, Title: msgs.Tc(r.Context(), "hub.clusterAddJobTitle", req.Count, cl.Name),
		Queue: fmt.Sprintf("cluster:%d", id), Author: user, Steps: req.Count + 3,
		Params: ClusterJobParams{ClusterID: id, AddWorkers: req.Count},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "cluster.add_workers", cl.Name, "ok", fmt.Sprint(req.Count))
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

func (s *Server) handleClusterDelete(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cl, err := s.db.ClusterByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	user := auth.Username(r.Context())
	_ = s.db.SetClusterStatus(r.Context(), id, store.ClusterDeleting, "")
	jobID, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindClusterDelete, Title: msgs.Tc(r.Context(), "hub.clusterDeleteJobTitle", cl.Name),
		Queue: fmt.Sprintf("cluster:%d", id), Author: user, Steps: 1,
		Params: ClusterJobParams{ClusterID: id},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "cluster.delete", cl.Name, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}
