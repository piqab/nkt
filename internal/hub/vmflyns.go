package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Traffic Fly для namespace клиента (1.17.13): администратор один раз
// задаёт у кластера ingress для клиентов — узел и порт, куда идёт HTTP
// (ingress Cilium или другой контроллер); токен публикует свой namespace
// по имени в своём домене: маршрут HTTPS с сертификатом на шлюзе ведёт на
// этот ingress, а Ingress клиента с тем же именем (а другого политики
// допуска и не пустят) направляет трафик в его сервис. Настройка — по
// именам кластера и узла (переживает экспорт).

const clientIngressKV = "k8s.clientingress"

// ClientIngress — куда Fly ведёт трафик клиентов кластера.
type ClientIngress struct {
	Node string `json:"node"`
	Port int    `json:"port"`
}

func (s *Server) clientIngresses(ctx context.Context) map[string]ClientIngress {
	out := map[string]ClientIngress{}
	if raw, ok, _ := s.db.KVGet(ctx, clientIngressKV); ok {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// handleClientIngress — GET/PUT /hub/clusters/{id}/client-ingress
// {"node":"lab-w-1","port":80} (port 0 — выключить).
func (s *Server) handleClientIngress(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	cl, err := s.db.ClusterByID(ctx, id)
	if err != nil {
		fail(w, r, err)
		return
	}
	all := s.clientIngresses(ctx)
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"ingress": all[cl.Name]})
		return
	}
	var req ClientIngress
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	before := all[cl.Name]
	if req.Port == 0 {
		delete(all, cl.Name)
	} else {
		hosts, _ := s.db.ClusterHosts(ctx, cl.ID)
		if req.Port < 1 || req.Port > 65535 || !slices.ContainsFunc(hosts, func(h store.Host) bool { return h.Name == req.Node }) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.clientIngressBad", cl.Name))
			return
		}
		all[cl.Name] = req
	}
	raw, _ := json.Marshal(all)
	if err := s.db.KVSet(ctx, clientIngressKV, string(raw)); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, auth.Username(ctx), "cluster.client_ingress", cl.Name, "ok", map[string]any{"before": before, "after": req})
	writeJSON(w, http.StatusOK, map[string]any{"ingress": req})
}

// ownFlyClusterTarget — цель маршрута токена на ingress кластера: только
// для своего namespace (или из прав) в кластере, где ingress задан.
func (s *Server) ownFlyClusterTarget(ctx context.Context, t store.APIToken, cluster, ns string) (FlyTarget, error) {
	if !slices.Contains(s.tokenAllowedNamespaces(ctx, t), ns) {
		return FlyTarget{}, msgs.Errorf("hub.ownNSNotOwn", ns)
	}
	cl, _, err := s.ownNSTarget(ctx, t, cluster)
	if err != nil {
		return FlyTarget{}, err
	}
	in, ok := s.clientIngresses(ctx)[cl.Name]
	if !ok || in.Port == 0 {
		return FlyTarget{}, msgs.Errorf("hub.clientIngressOff", cl.Name)
	}
	hosts, err := s.db.ClusterHosts(ctx, cl.ID)
	if err != nil {
		return FlyTarget{}, err
	}
	for _, h := range hosts {
		if h.Name != in.Node {
			continue
		}
		if h.ParentID != 0 {
			return FlyTarget{ID: "t1", Label: cl.Name + "/" + ns, HostID: h.ParentID, Kind: "vm", Ref: h.Name, Port: in.Port}, nil
		}
		return FlyTarget{ID: "t1", Label: cl.Name + "/" + ns, HostID: h.ID, Kind: "host", Port: in.Port}, nil
	}
	return FlyTarget{}, msgs.Errorf("hub.clientIngressOff", cl.Name)
}
