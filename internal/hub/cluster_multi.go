package hub

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// Хаб над несколькими кластерами сразу: сводка находок Kubernetes со
// всех control plane и один Helm-релиз во многие кластеры.

// ClusterFindings — находки Kubernetes одного кластера.
type ClusterFindings struct {
	ClusterID int64           `json:"cluster_id"`
	Cluster   string          `json:"cluster"`
	Findings  []model.Finding `json:"findings"`
	Error     string          `json:"error,omitempty"`
}

// handleClustersFindings — GET /hub/k8s/findings: находки со всех
// кластеров (control plane опрашиваются параллельно).
func (s *Server) handleClustersFindings(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListClusters(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	lang := msgs.LangFromRequest(r)
	out := make([]ClusterFindings, len(list))
	var wg sync.WaitGroup
	for i, cl := range list {
		out[i] = ClusterFindings{ClusterID: cl.ID, Cluster: cl.Name, Findings: []model.Finding{}}
		hosts, _ := s.db.ClusterHosts(r.Context(), cl.ID)
		if len(hosts) == 0 {
			out[i].Error = msgs.T(lang, "hub.clusterNoNodes")
			continue
		}
		wg.Add(1)
		go func(i int, hostID int64) {
			defer wg.Done()
			var res struct {
				Findings []model.Finding `json:"findings"`
			}
			path := "/api/findings?service=" + model.ServiceK8s + "&lang=" + url.QueryEscape(string(lang))
			if _, err := s.hub.HostAPI(r.Context(), hostID, "GET", path, nil, &res); err != nil {
				out[i].Error = msgs.Localize(lang, err)
				return
			}
			if res.Findings != nil {
				out[i].Findings = res.Findings
			}
		}(i, hosts[0].ID)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"clusters": out})
}

// KindHelmMulti — задание «Helm-релиз в несколько кластеров».
const KindHelmMulti = "k8s.helm.multi"

// HelmMultiParams — вход задания.
type HelmMultiParams struct {
	Clusters []int64                `json:"clusters"`
	Release  k8s.HelmInstallRequest `json:"release"`
}

type helmMultiResume struct {
	Done   int      `json:"done"`
	Failed []string `json:"failed"`
}

// HelmMultiRunner — исполнитель: кластер за кластером ставит релиз
// заданием на его control plane. Ошибка в одном кластере не мешает
// остальным — в итоге перечисляются неудачные.
type HelmMultiRunner struct{ m *Manager }

// NewHelmMultiRunner строит исполнителя.
func NewHelmMultiRunner(m *Manager) *HelmMultiRunner { return &HelmMultiRunner{m: m} }

// Resumable — да: пройденные кластеры сохранены.
func (r *HelmMultiRunner) Resumable() bool { return true }

// Run обходит кластеры.
func (r *HelmMultiRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p HelmMultiParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	var done helmMultiResume
	if err := jc.LoadResume(&done); err != nil {
		return msgs.Errorf("hub.parsingResumeState", err)
	}
	waiter := &GroupApplyRunner{m: r.m}
	for i := done.Done; i < len(p.Clusters); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		cl, err := r.m.db.ClusterByID(ctx, p.Clusters[i])
		name := fmt.Sprint(p.Clusters[i])
		if err == nil {
			name = cl.Name
		}
		jc.Step(i+1, len(p.Clusters), name)
		jc.Log("hub.helmMultiCluster", i+1, len(p.Clusters), name)
		if err == nil {
			err = r.installOn(ctx, jc, waiter, cl.ID, p.Release)
		}
		if err != nil {
			jc.Log("hub.helmMultiFailed", name, err)
			done.Failed = append(done.Failed, name)
		}
		done.Done = i + 1
		jc.SaveResume(done)
	}
	if len(done.Failed) > 0 {
		sort.Strings(done.Failed)
		return msgs.Errorf("hub.helmMultiFailedList", len(done.Failed), len(p.Clusters), strings.Join(done.Failed, ", "))
	}
	jc.Log("hub.helmMultiDone", len(p.Clusters))
	return nil
}

func (r *HelmMultiRunner) installOn(ctx context.Context, jc *jobs.Context, waiter *GroupApplyRunner, clusterID int64, rel k8s.HelmInstallRequest) error {
	hosts, err := r.m.db.ClusterHosts(ctx, clusterID)
	if err != nil || len(hosts) == 0 {
		return msgs.Errorf("hub.clusterNoNodes")
	}
	cp := hosts[0]
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := r.m.HostAPI(ctx, cp.ID, "POST", "/api/k8s/helm/install?job=1", rel, &started); err != nil {
		return err
	}
	return waiter.waitHostJob(ctx, jc, cp, started.JobID)
}

// handleHelmMulti — POST /hub/k8s/helm/install {clusters, release}.
func (s *Server) handleHelmMulti(w http.ResponseWriter, r *http.Request) {
	var req HelmMultiParams
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(req.Clusters) == 0 || len(req.Clusters) > maxManifestClusters {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.manifestClusters", maxManifestClusters))
		return
	}
	if err := req.Release.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var names []string
	for _, id := range req.Clusters {
		cl, err := s.db.ClusterByID(r.Context(), id)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		names = append(names, cl.Name)
	}
	user := auth.Username(r.Context())
	target := req.Release.Namespace + "/" + req.Release.Release
	jobID, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindHelmMulti, TitleKey: "hub.helmMultiJobTitle", TitleArgs: []any{target, len(req.Clusters)},
		Queue: "helm:multi", Author: user, Steps: len(req.Clusters), Params: req,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "hub.k8s.helm", target+" "+req.Release.ChartRef()+" → "+strings.Join(names, ", "), "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}
