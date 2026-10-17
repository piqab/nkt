package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// flyGatewayJSON — шлюз для интерфейса.
type flyGatewayJSON struct {
	EdgeID    int64  `json:"edge_id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	Connected bool   `json:"connected"`
	HostID    int64  `json:"host_id,omitempty"`
}

func (s *Server) flyGateways(ctx context.Context) []flyGatewayJSON {
	out := []flyGatewayJSON{}
	hosts := map[int64]string{}
	if list, err := s.db.ListHosts(ctx); err == nil {
		for _, h := range list {
			hosts[h.ID] = h.Name
		}
	}
	for _, e := range s.edges(ctx) {
		if !e.Has(EdgeRoleFly) {
			continue
		}
		connected, _, _ := s.edgeState(e.ID)
		// У шлюза обычно нет имени с сертификатом: зовём его хостом хаба,
		// на который он поставлен, иначе — адресом без порта туннеля.
		addr := e.Address
		if h, _, err := net.SplitHostPort(addr); err == nil {
			addr = h
		}
		name := e.Domain
		if name == "" {
			name = hosts[e.HostID]
		}
		if name == "" {
			name = addr
		}
		out = append(out, flyGatewayJSON{EdgeID: e.ID, Name: name, Address: addr, Connected: connected, HostID: e.HostID})
	}
	return out
}

// handleFly — GET /hub/fly: документ, его текст (для диффа), шлюзы,
// применённая ревизия.
func (s *Server) handleFly(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := s.flyDoc(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"doc": d, "text": d.Text(), "gateways": s.flyGateways(ctx), "applied": s.flyAppliedState(ctx).Revision,
	})
}

// handleFlySave — PUT /hub/fly {gateways, routes, note}: новая версия.
func (s *Server) handleFlySave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Gateways []FlyGateway `json:"gateways"`
		Routes   []FlyRoute   `json:"routes"`
		Note     string       `json:"note"`
		// Base — ревизия, которую правили: чужая правка между чтением и
		// записью — отказ, а не молчаливая перезапись.
		Base int64 `json:"base"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	if cur := s.flyDoc(ctx); req.Base != cur.Revision {
		writeError(w, http.StatusConflict, msgs.Tc(ctx, "fly.conflict", req.Base, cur.Revision))
		return
	}
	if req.Routes == nil {
		req.Routes = []FlyRoute{}
	}
	user := auth.Username(ctx)
	d, err := s.flySave(ctx, FlyDoc{Gateways: req.Gateways, Routes: req.Routes}, user, req.Note)
	s.db.Audit(ctx, user, "fly.save", strconv.FormatInt(d.Revision, 10), auditOutcome(err), map[string]any{"routes": len(req.Routes)})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"doc": d, "text": d.Text()})
}

// handleFlyVersions — GET /hub/fly/versions: история (текстом для диффа).
func (s *Server) handleFlyVersions(w http.ResponseWriter, r *http.Request) {
	type row struct {
		N    int64  `json:"n"`
		TS   string `json:"ts"`
		User string `json:"user"`
		Note string `json:"note,omitempty"`
		Text string `json:"text"`
	}
	out := []row{}
	for _, v := range s.flyVersions(r.Context()) {
		out = append(out, row{N: v.N, TS: v.TS, User: v.User, Note: v.Note, Text: v.Doc.Text()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out})
}

// handleFlyRestore — POST /hub/fly/versions/{n}/restore: версия — снова
// текущая (новой записью; применить — отдельно).
func (s *Server) handleFlyRestore(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.ParseInt(chi.URLParam(r, "n"), 10, 64)
	ctx := r.Context()
	for _, v := range s.flyVersions(ctx) {
		if v.N != n {
			continue
		}
		user := auth.Username(ctx)
		d, err := s.flySave(ctx, FlyDoc{Gateways: v.Doc.Gateways, Routes: v.Doc.Routes}, user, msgs.Tc(ctx, "fly.restoredNote", n))
		s.db.Audit(ctx, user, "fly.restore", strconv.FormatInt(n, 10), auditOutcome(err), nil)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"doc": d, "text": d.Text()})
		return
	}
	writeError(w, http.StatusNotFound, msgs.Tc(ctx, "fly.noVersion", n))
}

// handleFlyApply — POST /hub/fly/apply {dry_run}: задание.
func (s *Server) handleFlyApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	_ = decodeJSON(r, &req)
	ctx := r.Context()
	d := s.flyDoc(ctx)
	user := auth.Username(ctx)
	title := "fly.jobTitle"
	if req.DryRun {
		title = "fly.jobTitleDry"
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindFlyApply, TitleKey: title, TitleArgs: []any{d.Revision}, Queue: "fly", Author: user, Steps: 4,
		Params: FlyApplyParams{Revision: d.Revision, DryRun: req.DryRun}})
	s.db.Audit(ctx, user, "fly.apply", strconv.FormatInt(d.Revision, 10), auditOutcome(err), map[string]any{"dry_run": req.DryRun})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// flyGatewayStatusJSON — сводка шлюза для карты.
type flyGatewayStatusJSON struct {
	EdgeID    int64              `json:"edge_id"`
	Connected bool               `json:"connected"`
	Status    *fly.GatewayStatus `json:"status,omitempty"`
	Error     string             `json:"error,omitempty"`
}

var flyStatusCache struct {
	sync.Mutex
	at   time.Time
	list []flyGatewayStatusJSON
}

// flyStatuses — сводки всех шлюзов документа (кэш на 5 с: карту смотрят
// несколько человек, а шлюз не обязан отвечать каждому).
func (s *Server) flyStatuses(ctx context.Context) []flyGatewayStatusJSON {
	flyStatusCache.Lock()
	defer flyStatusCache.Unlock()
	if time.Since(flyStatusCache.at) < 5*time.Second && flyStatusCache.list != nil {
		return flyStatusCache.list
	}
	out := []flyGatewayStatusJSON{}
	for _, g := range s.flyDoc(ctx).Gateways {
		row := flyGatewayStatusJSON{EdgeID: g.EdgeID}
		_, sess, ok := s.flySession(ctx, g.EdgeID)
		row.Connected = ok
		if ok {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			var st fly.GatewayStatus
			if err := flyGatewayCall(cctx, sess, "GET", fly.PathStatus, nil, &st); err != nil {
				row.Error = msgs.Localize(msgs.FromContext(ctx), err)
			} else {
				row.Status = &st
			}
			cancel()
		}
		out = append(out, row)
	}
	flyStatusCache.at, flyStatusCache.list = time.Now(), out
	return out
}

// handleFlyStatus — GET /hub/fly/status: сводка для карты. IP клиентов —
// только администратору; остальным — число.
func (s *Server) handleFlyStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list := s.flyStatuses(ctx)
	if u, ok := auth.UserFromContext(ctx); !ok || u.Role != store.RoleAdmin {
		raw, _ := json.Marshal(list)
		var copyList []flyGatewayStatusJSON
		_ = json.Unmarshal(raw, &copyList)
		for _, g := range copyList {
			if g.Status == nil {
				continue
			}
			for i := range g.Status.Routes {
				g.Status.Routes[i].Top = nil
			}
		}
		list = copyList
	}
	names := map[string]string{fmt.Sprint(localHostID): "localhost"}
	if hosts, err := s.db.ListHosts(ctx); err == nil {
		for _, h := range hosts {
			names[fmt.Sprint(h.ID)] = h.Name
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"gateways": list, "hosts": names, "now": time.Now().Unix()})
}

// flyTargetItem — что на хосте можно сделать целью.
type flyTargetItem struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	IP    string `json:"ip,omitempty"`
	State string `json:"state,omitempty"`
	Ports []int  `json:"ports,omitempty"`
}

// flyTargetsCache — списки целей по хосту и виду на flyTargetsTTL:
// редактор маршрута открывают и закрывают, а хост каждый раз заново
// перечислять контейнеры и машины не обязан.
var flyTargetsCache = struct {
	sync.Mutex
	m map[string]flyTargetsEntry
}{m: map[string]flyTargetsEntry{}}

type flyTargetsEntry struct {
	at    time.Time
	items []flyTargetItem
}

const (
	flyTargetsTTL = 30 * time.Second
	// flyTargetsTimeout — на один вид: нет libvirt или LXD — отказ не
	// должен держать список контейнеров.
	flyTargetsTimeout = 8 * time.Second
)

// handleFlyTargets — GET /hub/fly/targets?host_id=&kind=container|vm|lxd
// [&refresh=1]: что на хосте можно сделать целью. Без kind — все виды,
// параллельно.
func (s *Server) handleFlyTargets(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("host_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "fly.badHost"))
		return
	}
	kinds := []string{"container", "vm", "lxd"}
	if k := r.URL.Query().Get("kind"); k != "" {
		if !slices.Contains(kinds, k) {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "fly.badHost"))
			return
		}
		kinds = []string{k}
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	ctx := r.Context()
	user := auth.Username(ctx)
	lists := make([][]flyTargetItem, len(kinds))
	var wg sync.WaitGroup
	for i, k := range kinds {
		key := fmt.Sprintf("%d/%s", id, k)
		flyTargetsCache.Lock()
		e, ok := flyTargetsCache.m[key]
		flyTargetsCache.Unlock()
		if ok && !refresh && time.Since(e.at) < flyTargetsTTL {
			lists[i] = e.items
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, flyTargetsTimeout)
			defer cancel()
			items, err := s.flyTargetsOf(cctx, user, id, k)
			lists[i] = items
			if err == nil {
				flyTargetsCache.Lock()
				flyTargetsCache.m[key] = flyTargetsEntry{at: time.Now(), items: items}
				flyTargetsCache.Unlock()
			}
		}()
	}
	wg.Wait()
	out := []flyTargetItem{}
	for _, l := range lists {
		out = append(out, l...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": out})
}

// flyTargetsOf — один вид целей хоста.
func (s *Server) flyTargetsOf(ctx context.Context, user string, id int64, kind string) ([]flyTargetItem, error) {
	out := []flyTargetItem{}
	switch kind {
	case "container":
		var cs struct {
			Containers []model.Container `json:"containers"`
		}
		if _, err := s.hostCall(ctx, user, id, "GET", "/api/containers", nil, &cs); err != nil {
			return out, err
		}
		for _, c := range cs.Containers {
			it := flyTargetItem{Kind: "container", Name: c.Name, State: c.State}
			for _, n := range c.Networks {
				if n.IPAddress != "" {
					it.IP = n.IPAddress
					break
				}
			}
			for _, p := range c.Ports {
				it.Ports = append(it.Ports, p.ContainerPort)
			}
			out = append(out, it)
		}
	case "vm":
		var vms struct {
			VMs []model.VirtualMachine `json:"vms"`
		}
		if _, err := s.hostCall(ctx, user, id, "GET", "/api/vms", nil, &vms); err != nil {
			return out, err
		}
		for _, v := range vms.VMs {
			it := flyTargetItem{Kind: "vm", Name: v.Name, State: v.State}
			for _, n := range v.Networks {
				if n.IP != "" {
					it.IP = n.IP
					break
				}
			}
			out = append(out, it)
		}
	case "lxd":
		var lxd struct {
			Instances []model.LXDInstance `json:"instances"`
		}
		if _, err := s.hostCall(ctx, user, id, "GET", "/api/lxd/instances", nil, &lxd); err != nil {
			return out, err
		}
		for _, in := range lxd.Instances {
			it := flyTargetItem{Kind: "lxd", Name: in.Name, State: in.Status}
			if len(in.IPv4) > 0 {
				it.IP = in.IPv4[0]
			}
			out = append(out, it)
		}
	}
	return out, nil
}
