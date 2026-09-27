package hub

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Обновление кластера хаба по узлам: control plane первыми, затем
// worker'ы. На каждом узле многоузлового кластера — cordon и drain
// (заданием на control plane), обновление Kubernetes (заданием на самом
// узле), ожидание Ready и uncordon. У одиночного узла выводить поды
// некуда — drain пропускается. На первой ошибке задание останавливается:
// дальше обновлять — множить поломку; «продолжить» идёт с того же узла.

// KindClusterUpgrade — задание «обновить кластер».
const KindClusterUpgrade = "cluster.upgrade"

// ClusterUpgradeParams — вход задания.
type ClusterUpgradeParams struct {
	ClusterID int64  `json:"cluster_id"`
	Version   string `json:"version"`
}

type clusterUpgradeResume struct {
	Done int `json:"done"`
}

// ClusterUpgradeRunner — исполнитель.
type ClusterUpgradeRunner struct{ m *Manager }

// NewClusterUpgradeRunner строит исполнителя.
func NewClusterUpgradeRunner(m *Manager) *ClusterUpgradeRunner { return &ClusterUpgradeRunner{m: m} }

// Resumable — да: пройденные узлы сохранены.
func (r *ClusterUpgradeRunner) Resumable() bool { return true }

// nodeReadyTimeout — сколько ждать Ready после обновления узла.
const nodeReadyTimeout = 10 * time.Minute

// Run обходит узлы кластера.
func (r *ClusterUpgradeRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ClusterUpgradeParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	hosts, err := r.m.db.ClusterHosts(ctx, p.ClusterID)
	if err != nil || len(hosts) == 0 {
		return msgs.Errorf("hub.clusterNoNodes")
	}
	cp := hosts[0]
	var done clusterUpgradeResume
	if err := jc.LoadResume(&done); err != nil {
		return msgs.Errorf("hub.parsingResumeState", err)
	}
	multi := len(hosts) > 1
	waiter := &GroupApplyRunner{m: r.m}
	for i := done.Done; i < len(hosts); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		h := hosts[i]
		jc.Step(i+1, len(hosts), h.Name)
		jc.Log("hub.upgradeNode", i+1, len(hosts), h.Name, p.Version)
		node, err := r.nodeName(ctx, cp, h)
		if err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
		if multi {
			jc.Log("hub.upgradeDrain", node)
			if err := r.action(ctx, cp, node, "cordon"); err != nil {
				return fmt.Errorf("%s: %w", h.Name, err)
			}
			var started struct {
				JobID int64 `json:"job_id"`
			}
			if _, err := r.m.HostAPI(ctx, cp.ID, "POST", "/api/k8s/nodes/drain?job=1", map[string]string{"name": node}, &started); err != nil {
				return fmt.Errorf("%s: drain: %w", h.Name, err)
			}
			if err := waiter.waitHostJob(ctx, jc, cp, started.JobID); err != nil {
				return fmt.Errorf("%s: drain: %w", h.Name, err)
			}
		}
		var started struct {
			JobID int64 `json:"job_id"`
		}
		body := k8s.UpgradeSpec{Version: p.Version, First: i == 0}
		if _, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/k8s/upgrade/node?job=1", body, &started); err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
		if err := waiter.waitHostJob(ctx, jc, h, started.JobID); err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
		jc.Log("hub.upgradeWaitReady", node)
		if err := r.waitReady(ctx, cp, node); err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
		if multi {
			if err := r.action(ctx, cp, node, "uncordon"); err != nil {
				return fmt.Errorf("%s: %w", h.Name, err)
			}
		}
		done.Done = i + 1
		jc.SaveResume(done)
	}
	jc.Log("hub.upgradeDone", len(hosts), p.Version)
	return nil
}

type k8sNodesResp struct {
	Nodes []k8s.Node `json:"nodes"`
}

// nodeName — имя узла в кластере для хоста хаба: по имени, иначе по адресу.
func (r *ClusterUpgradeRunner) nodeName(ctx context.Context, cp, h store.Host) (string, error) {
	var res k8sNodesResp
	if _, err := r.m.HostAPI(ctx, cp.ID, "GET", "/api/k8s", nil, &res); err != nil {
		return "", err
	}
	for _, n := range res.Nodes {
		if n.Name == h.Name {
			return n.Name, nil
		}
	}
	for _, n := range res.Nodes {
		if n.InternalIP != "" && n.InternalIP == h.Addr {
			return n.Name, nil
		}
	}
	return "", msgs.Errorf("hub.upgradeNoNode", h.Name)
}

func (r *ClusterUpgradeRunner) action(ctx context.Context, cp store.Host, node, action string) error {
	_, err := r.m.HostAPI(ctx, cp.ID, "POST", "/api/k8s/objects/action", map[string]string{"kind": "nodes", "name": node, "action": action}, nil)
	return err
}

// waitReady ждёт, пока узел снова станет Ready.
func (r *ClusterUpgradeRunner) waitReady(ctx context.Context, cp store.Host, node string) error {
	deadline := time.Now().Add(nodeReadyTimeout)
	for time.Now().Before(deadline) {
		var res k8sNodesResp
		if _, err := r.m.HostAPI(ctx, cp.ID, "GET", "/api/k8s", nil, &res); err == nil {
			for _, n := range res.Nodes {
				if n.Name == node && n.Ready {
					return nil
				}
			}
		}
		if !sleepCtx(ctx, 5*time.Second) {
			return ctx.Err()
		}
	}
	return msgs.Errorf("hub.upgradeNotReady", node, nodeReadyTimeout)
}

// handleClusterUpgrade — POST /hub/clusters/{id}/upgrade {version}.
func (s *Server) handleClusterUpgrade(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cl, err := s.db.ClusterByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	hosts, _ := s.db.ClusterHosts(r.Context(), id)
	if len(hosts) == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.clusterNoNodes"))
		return
	}
	// Версия и шаг проверяются по control plane — та же проверка, что
	// сделает каждый узел перед своим обновлением.
	var info k8s.UpgradeInfo
	if _, err := s.hub.HostAPI(r.Context(), hosts[0].ID, "GET", "/api/k8s/upgrade", nil, &info); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if err := (k8s.UpgradeSpec{Version: req.Version}).Validate(info.Flavor, info.Version); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	jobID, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindClusterUpgrade, TitleKey: "hub.upgradeJobTitle", TitleArgs: []any{cl.Name, req.Version},
		Queue: fmt.Sprintf("cluster:%d", id), Author: user, Steps: len(hosts),
		Params: ClusterUpgradeParams{ClusterID: id, Version: req.Version},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "cluster.upgrade", cl.Name, "ok", info.Version+" → "+req.Version)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

// handleClusterUpgradeInfo — GET /hub/clusters/{id}/upgrade: версия и
// каналы с control plane.
func (s *Server) handleClusterUpgradeInfo(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	hosts, _ := s.db.ClusterHosts(r.Context(), id)
	if len(hosts) == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.clusterNoNodes"))
		return
	}
	var info k8s.UpgradeInfo
	if _, err := s.hub.HostAPI(r.Context(), hosts[0].ID, "GET", "/api/k8s/upgrade", nil, &info); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}
