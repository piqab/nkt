package hub

import (
	"net/http"
	"net/url"
	"sync"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// Хаб над несколькими кластерами сразу: сводка находок Kubernetes со
// всех control plane.

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
