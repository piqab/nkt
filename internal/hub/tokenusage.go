package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Учёт токенов «по правам»: окно токена (занято и записи), «освободить»
// вручную и сверка с хостами — стек или машину удалили мимо nkt, запись
// снимается. Недоступный хост — запись остаётся (не знаем).

const tokenUsageReconcileEvery = 10 * time.Minute

// StartTokenUsageReconcile — сверка учёта токенов по таймеру.
func (s *Server) StartTokenUsageReconcile(ctx context.Context) {
	go func() {
		t := time.NewTicker(tokenUsageReconcileEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.reconcileTokenUsage(ctx)
			}
		}
	}()
}

// reconcileTokenUsage снимает записи о стеках и машинах, которых на
// хостах больше нет.
func (s *Server) reconcileTokenUsage(ctx context.Context) {
	usage, err := s.db.ListTokenUsage(ctx, 0)
	if err != nil || len(usage) == 0 {
		return
	}
	user := s.firstAdmin(ctx)
	projects := map[int64]map[string]bool{}
	vms := map[int64]map[string]bool{}
	reach := map[int64]bool{}
	for _, u := range usage {
		if u.Kind == store.UsageK8sNS {
			continue
		}
		if _, seen := reach[u.HostID]; !seen {
			reach[u.HostID] = false
			var cs struct {
				Containers []struct {
					Project string `json:"project"`
				} `json:"containers"`
			}
			if _, err := s.hostCall(ctx, user, u.HostID, "GET", "/api/containers", nil, &cs); err == nil {
				reach[u.HostID] = true
				projects[u.HostID] = map[string]bool{}
				for _, c := range cs.Containers {
					projects[u.HostID][c.Project] = true
				}
				var vs struct {
					VMs []struct {
						Name string `json:"name"`
					} `json:"vms"`
				}
				vms[u.HostID] = map[string]bool{}
				if _, err := s.hostCall(ctx, user, u.HostID, "GET", "/api/vms", nil, &vs); err == nil {
					for _, v := range vs.VMs {
						vms[u.HostID][v.Name] = true
					}
				} else {
					vms[u.HostID] = nil
				}
			}
		}
		if !reach[u.HostID] {
			continue
		}
		gone := (u.Kind == store.UsageCompose && !projects[u.HostID][u.Ref]) || (u.Kind == store.UsageVM && vms[u.HostID] != nil && !vms[u.HostID][u.Ref])
		if gone {
			_ = s.db.DeleteTokenUsage(ctx, u.TokenID, u.Kind, u.HostID, u.Ref)
		}
	}
}

// tokenUsageView — окно токена: потолки, занято и записи.
type tokenUsageView struct {
	Limits store.TokenLimits  `json:"limits"`
	Usage  []store.TokenUsage `json:"usage"`
	Used   struct {
		CPU    float64 `json:"cpu"`
		MemMB  int64   `json:"mem_mb"`
		DiskGB int64   `json:"disk_gb"`
		VMs    int     `json:"vms"`
		// Порты пула, свои маршруты Fly, namespace и кластеры (1.17.10).
		Ports      int `json:"ports"`
		FlyRoutes  int `json:"fly_routes"`
		Namespaces int `json:"k8s_namespaces"`
		Clusters   int `json:"k8s_clusters"`
	} `json:"used"`
	// Actions и FlyDomain — что токену можно (для самого клиента).
	Actions   []string `json:"actions,omitempty"`
	FlyDomain string   `json:"fly_domain,omitempty"`
}

// tokenUsageViewFor — «занято из» токена со сверкой.
func (s *Server) tokenUsageViewFor(ctx context.Context, t store.APIToken) (tokenUsageView, error) {
	s.reconcileTokenUsage(ctx)
	usage, err := s.db.ListTokenUsage(ctx, t.ID)
	if err != nil {
		return tokenUsageView{}, err
	}
	v := tokenUsageView{Limits: t.Perms.Limits, Usage: usage, Actions: t.Perms.Actions, FlyDomain: t.Perms.FlyDomain}
	if v.Usage == nil {
		v.Usage = []store.TokenUsage{}
	}
	for _, u := range usage {
		v.Used.CPU += u.CPU
		v.Used.MemMB += u.MemMB
		v.Used.DiskGB += u.DiskGB
		if u.Kind == store.UsageVM {
			v.Used.VMs++
		}
	}
	for _, a := range s.portAllocs(ctx) {
		if a.Token == t.Name {
			v.Used.Ports++
		}
	}
	for _, r := range s.flyDoc(ctx).Routes {
		if strings.HasPrefix(r.ID, ownFlyPrefix(t)) {
			v.Used.FlyRoutes++
		}
	}
	for _, o := range s.ownNamespaces(ctx) {
		if o.TokenID == t.ID {
			v.Used.Namespaces++
		}
	}
	for _, o := range s.ownClusters(ctx) {
		if o.TokenID == t.ID {
			v.Used.Clusters++
		}
	}
	return v, nil
}

// handleTokenSelfUsage — GET /hub/tokens/self/usage: «занято из» своего
// токена — для клиента (и MCP my_usage).
func (s *Server) handleTokenSelfUsage(w http.ResponseWriter, r *http.Request) {
	t, ok := tokenFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "auth.tokenOnly"))
		return
	}
	v, err := s.tokenUsageViewFor(r.Context(), t)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleTokenUsage — GET /hub/tokens/{id}/usage (со сверкой).
func (s *Server) handleTokenUsage(w http.ResponseWriter, r *http.Request) {
	id, err := tokenIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	t, err := s.db.APITokenByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	v, err := s.tokenUsageViewFor(r.Context(), t)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleTokenUsageRelease — POST /hub/tokens/{id}/usage/release {kind,
// host_id, ref}: «освободить» запись вручную (стек убран мимо хаба,
// хост недоступен навсегда).
func (s *Server) handleTokenUsageRelease(w http.ResponseWriter, r *http.Request) {
	id, err := tokenIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Kind   string `json:"kind"`
		HostID int64  `json:"host_id"`
		Ref    string `json:"ref"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !slices.Contains([]string{store.UsageCompose, store.UsageVM, store.UsageK8sNS}, req.Kind) || strings.TrimSpace(req.Ref) == "" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.tokenBadPerms", req.Kind))
		return
	}
	t, err := s.db.APITokenByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.db.DeleteTokenUsage(r.Context(), id, req.Kind, req.HostID, req.Ref); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	user := auth.Username(r.Context())
	s.db.Audit(r.Context(), user, "token.usage.release", t.Name, "ok", map[string]any{"kind": req.Kind, "host_id": req.HostID, "ref": req.Ref})
	raw, _ := json.Marshal(map[string]any{"ok": true})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}
