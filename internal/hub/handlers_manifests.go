package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Один YAML — в несколько кластеров (раздел «Кластеры»). Хаб сам
// kubectl не запускает: у каждого кластера есть узел control plane — хост
// nkt, и дифф с записью идут его API (/api/k8s/yaml/diff, /api/k8s/apply)
// по тому же каналу, что и узлы кластера. Манифест хранится на хабе с
// историей: каждое применение — редакция с итогом по кластерам.

// ManifestResult — итог по одному кластеру.
type ManifestResult struct {
	ClusterID int64  `json:"cluster_id"`
	Cluster   string `json:"cluster"`
	Diff      string `json:"diff,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

type manifestRequest struct {
	Name     string  `json:"name"`
	Note     string  `json:"note"`
	Content  string  `json:"content"`
	Clusters []int64 `json:"clusters"`
}

// maxManifestClusters — предел кластеров за раз.
const maxManifestClusters = 50

func (req manifestRequest) validate(apply bool) error {
	if _, err := k8s.ParseManifest(req.Content); err != nil {
		return err
	}
	if len(req.Clusters) == 0 || len(req.Clusters) > maxManifestClusters {
		return msgs.Errorf("hub.manifestClusters", maxManifestClusters)
	}
	if apply {
		n := strings.TrimSpace(req.Name)
		if n == "" || utf8.RuneCountInString(n) > 80 || strings.ContainsAny(n, "\n\r\t") {
			return msgs.Errorf("hub.manifestName")
		}
	}
	return nil
}

// forEachCluster вызывает fn для каждого кластера с его control plane.
func (s *Server) forEachCluster(ctx context.Context, lang msgs.Lang, ids []int64, fn func(cl store.Cluster, hostID int64) ManifestResult) []ManifestResult {
	out := make([]ManifestResult, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		cl, err := s.db.ClusterByID(ctx, id)
		if err != nil {
			out = append(out, ManifestResult{ClusterID: id, Cluster: fmt.Sprint(id), Error: msgs.Localize(lang, err)})
			continue
		}
		hosts, err := s.db.ClusterHosts(ctx, id)
		if err != nil || len(hosts) == 0 {
			out = append(out, ManifestResult{ClusterID: id, Cluster: cl.Name, Error: msgs.Tc(ctx, "hub.clusterNoNodes")})
			continue
		}
		res := fn(cl, hosts[0].ID)
		res.ClusterID, res.Cluster = cl.ID, cl.Name
		out = append(out, res)
	}
	return out
}

// handleManifestDiff — POST /hub/k8s/manifests/diff {content, clusters}:
// kubectl diff в каждом кластере.
func (s *Server) handleManifestDiff(w http.ResponseWriter, r *http.Request) {
	var req manifestRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := req.validate(false); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	results := s.forEachCluster(r.Context(), msgs.LangFromRequest(r), req.Clusters, func(cl store.Cluster, hostID int64) ManifestResult {
		var res struct {
			Diff string `json:"diff"`
		}
		if _, err := s.hub.HostAPI(r.Context(), hostID, "POST", "/api/k8s/yaml/diff", map[string]string{"content": req.Content}, &res); err != nil {
			return ManifestResult{Error: msgs.Localize(msgs.LangFromRequest(r), err)}
		}
		return ManifestResult{Diff: res.Diff}
	})
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleManifestApply — POST /hub/k8s/manifests/apply {name, note,
// content, clusters}: kubectl apply в каждом кластере, редакция с итогом.
// С ?job=1 — заданием хаба: кластеров много, и запрос браузера обрывался
// раньше, чем доходил до последнего.
func (s *Server) handleManifestApply(w http.ResponseWriter, r *http.Request) {
	var req manifestRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := req.validate(true); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	if r.URL.Query().Get("job") == "1" && s.jobs != nil {
		id, err := s.jobs.Start(r.Context(), jobs.Spec{
			Kind: KindManifestApply, TitleKey: "hub.manifestApplyJob", TitleArgs: []any{strings.TrimSpace(req.Name), len(req.Clusters)},
			Queue: "hub-manifests", Author: user, Steps: len(req.Clusters), Params: req,
		})
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
		return
	}
	results, id, vid, err := s.applyManifest(r.Context(), msgs.LangFromRequest(r), user, req, nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "manifest_id": id, "version_id": vid})
}

// applyManifest применяет манифест в кластерах по очереди и сохраняет
// редакцию с итогом. step (может быть nil) — ход по кластерам для журнала
// задания.
func (s *Server) applyManifest(ctx context.Context, lang msgs.Lang, user string, req manifestRequest, jc *jobs.Context) ([]ManifestResult, int64, int64, error) {
	note := req.Note
	if note == "" {
		note = msgs.T(lang, "hub.manifestApplied")
	}
	n := 0
	results := s.forEachCluster(ctx, lang, req.Clusters, func(cl store.Cluster, hostID int64) ManifestResult {
		n++
		if jc != nil {
			jc.StepKey(n, len(req.Clusters), "hub.manifestApplyStep", cl.Name)
		}
		var res struct {
			Output string `json:"output"`
		}
		body := map[string]string{"content": req.Content, "note": msgs.T(lang, "hub.manifestNote", req.Name, user)}
		if _, err := s.hub.HostAPI(ctx, hostID, "POST", "/api/k8s/apply", body, &res); err != nil {
			if jc != nil {
				jc.Log("hub.manifestApplyFailed", cl.Name, msgs.Localize(lang, err))
			}
			return ManifestResult{Error: msgs.Localize(lang, err)}
		}
		if jc != nil {
			jc.Log("hub.manifestApplyOK", cl.Name)
			for _, l := range strings.Split(strings.TrimSpace(res.Output), "\n") {
				if l != "" {
					jc.Logf("  %s", l)
				}
			}
		}
		return ManifestResult{Output: res.Output}
	})
	raw, _ := json.Marshal(results)
	id, vid, err := s.db.SaveManifestVersion(context.WithoutCancel(ctx), store.Manifest{Name: strings.TrimSpace(req.Name), Content: req.Content, Note: note, Author: user}, string(raw))
	names, failed := make([]string, 0, len(results)), 0
	for _, res := range results {
		names = append(names, res.Cluster)
		if res.Error != "" {
			failed++
		}
	}
	outcome := "ok"
	if failed > 0 {
		outcome = "error"
	}
	s.db.Audit(context.WithoutCancel(ctx), user, "hub.k8s.apply", req.Name+" → "+strings.Join(names, ", "), outcome, map[string]any{"failed": failed, "version_id": vid})
	if err == nil && failed > 0 && jc != nil {
		err = msgs.Errorf("hub.manifestApplySomeFailed", failed, len(results))
	}
	return results, id, vid, err
}

// KindManifestApply — задание хаба «применить манифест в кластерах».
const KindManifestApply = "hub.manifestapply"

// ManifestApplyRunner — исполнитель.
type ManifestApplyRunner struct{ s *Server }

// NewManifestApplyRunner — исполнитель применения манифеста.
func NewManifestApplyRunner(s *Server) *ManifestApplyRunner { return &ManifestApplyRunner{s: s} }

// Run применяет манифест по кластерам с журналом.
func (r *ManifestApplyRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var req manifestRequest
	if err := jc.Params(&req); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	_, _, _, err := r.s.applyManifest(ctx, jc.Lang(), jc.Job.Author, req, jc)
	return err
}

func manifestIDParam(r *http.Request, name string) (int64, error) {
	var id int64
	_, err := fmt.Sscan(chi.URLParam(r, name), &id)
	return id, err
}

// handleManifestList — GET /hub/k8s/manifests.
func (s *Server) handleManifestList(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListManifests(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"manifests": list})
}

// handleManifestGet — GET /hub/k8s/manifests/{id}.
func (s *Server) handleManifestGet(w http.ResponseWriter, r *http.Request) {
	id, err := manifestIDParam(r, "id")
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m, err := s.db.ManifestByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleManifestVersions — GET /hub/k8s/manifests/{id}/versions.
func (s *Server) handleManifestVersions(w http.ResponseWriter, r *http.Request) {
	id, err := manifestIDParam(r, "id")
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	list, err := s.db.ManifestVersions(r.Context(), id, 100)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list})
}

// handleManifestVersion — GET /hub/k8s/manifests/versions/{version}.
func (s *Server) handleManifestVersion(w http.ResponseWriter, r *http.Request) {
	id, err := manifestIDParam(r, "version")
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	v, err := s.db.ManifestVersion(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleManifestDelete — DELETE /hub/k8s/manifests/{id}: из библиотеки
// хаба (в кластерах объекты остаются).
func (s *Server) handleManifestDelete(w http.ResponseWriter, r *http.Request) {
	id, err := manifestIDParam(r, "id")
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m, _ := s.db.ManifestByID(r.Context(), id)
	err = s.db.DeleteManifest(r.Context(), id)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "hub.k8s.manifest.delete", m.Name, outcome, "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleManifestBlocks — POST /hub/k8s/manifests/blocks {content}: блоки
// манифеста для блочного режима редактора.
func (s *Server) handleManifestBlocks(w http.ResponseWriter, r *http.Request) {
	var req manifestRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	blocks, err := k8s.ManifestBlocks(req.Content)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks})
}
