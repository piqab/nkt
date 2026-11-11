package hub

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Cluster-scoped agent proxy (1.21.0): the Clusters section reuses the
// host Kubernetes UI, and every request of that UI goes through
// /api/hub/clusters/{id}/agent/... — resolved here to the cluster's first
// control plane and proxied to its agent exactly like /api/hosts/{id}/...
// (proxyHost). API tokens are denied this route by default (tokenPolicy
// knows no such pattern); browser sessions pass the usual RequireAdmin.

// clusterAgentHost — хост кластера, которому адресованы запросы агентского
// API: первый control plane (ClusterHosts сортирует их первыми). Машины
// балансировщика не в счёт: на них нет flavor Kubernetes и k8s API агента.
func (s *Server) clusterAgentHost(ctx context.Context, clusterID int64) (store.Host, error) {
	hosts, err := s.db.ClusterHosts(ctx, clusterID)
	if err != nil {
		return store.Host{}, err
	}
	nodes := k8sNodeHosts(hosts)
	if len(nodes) == 0 {
		return store.Host{}, msgs.Errorf("hub.clusterNoNodes")
	}
	return nodes[0], nil
}

// proxyClusterAgent — обратный прокси на агент control plane кластера.
func (s *Server) proxyClusterAgent(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cp, err := s.clusterAgentHost(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	// Derived from the URL path itself, not chi.URLParam(r, "*") — same
	// reasoning as proxyHost's identical comment: the raw {id} segment
	// can't mismatch on a non-canonical numeric spelling.
	rest := strings.TrimPrefix(r.URL.Path, "/api/hub/clusters/"+chi.URLParam(r, "id")+"/agent/")

	r2 := r.Clone(r.Context())
	r2.URL.Path = "/api/" + rest
	r2.URL.RawPath = ""
	// Large transfers ride their own SSH connection (see proxyHost).
	if isTransferPath(r.Method, rest) {
		s.hub.ProxyTransfer(cp.ID).ServeHTTP(w, r2)
		return
	}
	s.hub.Proxy(cp.ID).ServeHTTP(w, r2)
}
