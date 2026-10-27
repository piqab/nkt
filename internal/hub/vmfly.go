package hub

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Свои маршруты Traffic Fly (1.17.5): токен «по правам» с действием
// «свои маршруты Fly» публикует свою машину по имени — HTTPS с
// сертификатом на шлюзе, имя только в выданном ему домене (FlyDomain),
// цель — только своя машина. Карту целиком токен не видит и не правит;
// маршрут токена — с номером «t<ID токена>-…», чужие ему недоступны.

// ownFlyPrefix — начало номера маршрута токена.
func ownFlyPrefix(t store.APIToken) string { return "t" + strconv.FormatInt(t.ID, 10) + "-" }

// ownFlyRoute — маршрут для ответа токену.
type ownFlyRoute struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	HostID    int64  `json:"host_id,omitempty"`
	VM        string `json:"vm,omitempty"`
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Port      int    `json:"port"`
}

func (s *Server) ownFlyRoutes(ctx context.Context, t store.APIToken) []ownFlyRoute {
	out := []ownFlyRoute{}
	vmIDs := map[string]int64{}
	if hosts, err := s.db.ListAllHosts(ctx); err == nil {
		for _, h := range hosts {
			if h.ParentID != 0 {
				vmIDs[strconv.FormatInt(h.ParentID, 10)+"/"+h.Name] = h.ID
			}
		}
	}
	for _, r := range s.flyDoc(ctx).Routes {
		if !strings.HasPrefix(r.ID, ownFlyPrefix(t)) || len(r.Names) == 0 || len(r.Targets) == 0 {
			continue
		}
		tg := r.Targets[0]
		out = append(out, ownFlyRoute{ID: r.ID, Name: r.Names[0], VM: tg.Ref, Port: tg.Port,
			HostID: vmIDs[strconv.FormatInt(tg.HostID, 10)+"/"+tg.Ref]})
	}
	return out
}

// handleOwnFly — GET /hub/own/fly: свои маршруты токена.
func (s *Server) handleOwnFly(w http.ResponseWriter, r *http.Request) {
	t, ok := tokenFromContext(r.Context())
	if !ok || t.Role != store.TokenRoleCustom {
		writeError(w, http.StatusForbidden, msgs.Tc(r.Context(), "auth.tokenOnly"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": s.ownFlyRoutes(r.Context(), t), "domain": t.Perms.FlyDomain})
}

// handleOwnFlyAdd — POST /hub/own/fly {"name":"app.c.example.com",
// "host_id":<машина>,"port":8080}: маршрут HTTPS с сертификатом на шлюзе.
func (s *Server) handleOwnFlyAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, ok := tokenFromContext(ctx)
	if !ok || t.Role != store.TokenRoleCustom {
		writeError(w, http.StatusForbidden, msgs.Tc(ctx, "auth.tokenOnly"))
		return
	}
	var req OwnFlyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	route, jobID, code, err := s.ownFlyAdd(ctx, t, req)
	if err != nil {
		writeErr(w, r, code, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"route": route, "job_id": jobID})
}

// OwnFlyRequest — свой маршрут: имя и цель — своя машина (host_id, port)
// или свой namespace общего кластера (cluster, namespace) — тогда трафик
// идёт на ingress кластера, заданный администратором.
type OwnFlyRequest struct {
	Name      string `json:"name"`
	HostID    int64  `json:"host_id,omitempty"`
	Port      int    `json:"port,omitempty"`
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// ownFlyAdd — маршрут HTTPS с сертификатом на шлюзе по имени в домене
// токена; код ответа — для HTTP.
func (s *Server) ownFlyAdd(ctx context.Context, t store.APIToken, req OwnFlyRequest) (ownFlyRoute, int64, int, error) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Name), "."))
	domain := t.Perms.FlyDomain
	if domain == "" || !flyDomainRe.MatchString(name) || !inDomain(name, domain) {
		return ownFlyRoute{}, 0, http.StatusForbidden, msgs.Errorf("auth.tokenFlyName", name, domain)
	}
	var target FlyTarget
	out := ownFlyRoute{Name: name}
	if req.Cluster != "" {
		tg, err := s.ownFlyClusterTarget(ctx, t, req.Cluster, req.Namespace)
		if err != nil {
			return ownFlyRoute{}, 0, http.StatusForbidden, err
		}
		target, out.Cluster, out.Namespace, out.Port = tg, req.Cluster, req.Namespace, tg.Port
	} else {
		if req.Port < 1 || req.Port > 65535 {
			return ownFlyRoute{}, 0, http.StatusBadRequest, msgs.Errorf("fly.opBadPort", req.Port)
		}
		vm, err := s.db.HostByID(ctx, req.HostID)
		if err != nil || vm.ParentID == 0 {
			return ownFlyRoute{}, 0, http.StatusNotFound, msgs.Errorf("err.notFound")
		}
		if err := s.tokenOwnsVM(ctx, t, strconv.FormatInt(vm.ParentID, 10), vm.Name); err != nil {
			return ownFlyRoute{}, 0, http.StatusForbidden, err
		}
		target = FlyTarget{ID: "t1", Label: vm.Name, HostID: vm.ParentID, Kind: "vm", Ref: vm.Name, Port: req.Port}
		out.HostID, out.VM, out.Port = vm.ID, vm.Name, req.Port
	}
	ownFlyMu.Lock()
	defer ownFlyMu.Unlock()
	d := s.flyDoc(ctx)
	own := 0
	for _, rt := range d.Routes {
		if strings.HasPrefix(rt.ID, ownFlyPrefix(t)) {
			own++
		}
		if slices.Contains(rt.Names, name) {
			return ownFlyRoute{}, 0, http.StatusConflict, msgs.Errorf("auth.tokenFlyTaken", name)
		}
	}
	if own >= t.Perms.Limits.FlyRoutes {
		return ownFlyRoute{}, 0, http.StatusForbidden, msgs.Errorf("auth.tokenFlyRoutes", t.Perms.Limits.FlyRoutes)
	}
	gw, err := s.ownFlyGateway(ctx, t)
	if err != nil {
		return ownFlyRoute{}, 0, http.StatusConflict, err
	}
	id := ""
	for n := 1; ; n++ {
		id = fmt.Sprintf("%s%d", ownFlyPrefix(t), n)
		if !slices.ContainsFunc(d.Routes, func(rt FlyRoute) bool { return rt.ID == id }) {
			break
		}
	}
	route := FlyRoute{ID: id, Label: name, Gateway: gw, Kind: fly.KindHTTPS, Names: []string{name}, Port: 443, TLS: true,
		Algorithm: fly.AlgoRoundRobin, Check: fly.Check{Kind: fly.CheckTCP}, DNS: true, Targets: []FlyTarget{target}}
	user := auth.Username(ctx)
	saved, err := s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: append(slices.Clone(d.Routes), route)}, user, msgs.Tc(ctx, "fly.ownAddedNote", name, t.Name))
	s.db.Audit(ctx, user, "fly.own_add", name, auditOutcome(err), map[string]any{"route": id, "target": target.Label, "port": target.Port})
	if err != nil {
		return ownFlyRoute{}, 0, http.StatusBadRequest, err
	}
	jobID, err := s.startFlyApply(ctx, saved.Revision, user)
	if err != nil {
		return ownFlyRoute{}, 0, http.StatusInternalServerError, err
	}
	out.ID = id
	return out, jobID, http.StatusOK, nil
}

// handleOwnFlyDelete — DELETE /hub/own/fly/{route}: только свой маршрут.
func (s *Server) handleOwnFlyDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, ok := tokenFromContext(ctx)
	id := chi.URLParam(r, "route")
	if !ok || t.Role != store.TokenRoleCustom || !strings.HasPrefix(id, ownFlyPrefix(t)) {
		writeError(w, http.StatusForbidden, msgs.Tc(ctx, "auth.tokenFlyNotOwn", id))
		return
	}
	jobID, err := s.removeFlyRoutes(ctx, auth.Username(ctx), func(rt FlyRoute) bool { return rt.ID == id })
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if jobID == 0 {
		writeError(w, http.StatusNotFound, msgs.Tc(ctx, "fly.routeNotFound", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

// removeFlyRoutes убирает маршруты по условию, сохраняет версию и
// раскладывает её; 0 — убирать было нечего.
func (s *Server) removeFlyRoutes(ctx context.Context, user string, drop func(FlyRoute) bool) (int64, error) {
	ownFlyMu.Lock()
	defer ownFlyMu.Unlock()
	d := s.flyDoc(ctx)
	kept := slices.DeleteFunc(slices.Clone(d.Routes), drop)
	if len(kept) == len(d.Routes) {
		return 0, nil
	}
	saved, err := s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: kept}, user, msgs.Tc(ctx, "fly.ownRemovedNote"))
	s.db.Audit(ctx, user, "fly.own_remove", strconv.FormatInt(saved.Revision, 10), auditOutcome(err), nil)
	if err != nil {
		return 0, err
	}
	return s.startFlyApply(ctx, saved.Revision, user)
}

// releaseVMFly убирает маршруты токенов, ведущие на машину (перед её
// удалением). Маршруты администратора не трогаются.
func (s *Server) releaseVMFly(ctx context.Context, vm store.Host) {
	_, _ = s.removeFlyRoutes(ctx, auth.Username(ctx), func(rt FlyRoute) bool {
		if len(rt.ID) < 2 || rt.ID[0] != 't' || !strings.Contains(rt.ID, "-") {
			return false
		}
		return slices.ContainsFunc(rt.Targets, func(tg FlyTarget) bool {
			return tg.Kind == "vm" && tg.HostID == vm.ParentID && tg.Ref == vm.Name
		})
	})
}

// ownFlyGateway — шлюз маршрутов токена: заданный в правах или
// единственный шлюз Fly.
func (s *Server) ownFlyGateway(ctx context.Context, t store.APIToken) (int64, error) {
	var gws []int64
	for _, e := range s.edges(ctx) {
		if e.Has(EdgeRoleFly) {
			gws = append(gws, e.ID)
		}
	}
	if t.Perms.FlyGateway != 0 {
		if slices.Contains(gws, t.Perms.FlyGateway) {
			return t.Perms.FlyGateway, nil
		}
		return 0, msgs.Errorf("auth.tokenFlyGateway")
	}
	if len(gws) != 1 {
		return 0, msgs.Errorf("auth.tokenFlyGateway")
	}
	return gws[0], nil
}

// startFlyApply ставит раскладку версии.
func (s *Server) startFlyApply(ctx context.Context, revision int64, user string) (int64, error) {
	return s.jobs.Start(ctx, jobs.Spec{Kind: KindFlyApply, TitleKey: "fly.jobTitle", TitleArgs: []any{revision}, Queue: "fly", Author: user, Steps: 4,
		Params: FlyApplyParams{Revision: revision}})
}

// ownFlyMu — чтение карты и запись маршрута токена одним шагом (flySave
// держит flyMu сам).
var ownFlyMu sync.Mutex
