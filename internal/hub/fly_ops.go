package hub

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Изменения целей с карты: список операций, которые применяются к той
// версии, что есть на момент выполнения (а не к той, что видел браузер), —
// очередь заданий не ломается от чужих правок между ними. Черновик карты
// уходит одним списком (одна версия, одно применение), «применять сразу» —
// по операции на задание.

// FlyOp — одна операция над целью маршрута.
//
//	add      — контейнер, машина, LXD или адрес в маршрут Route резервом;
//	switch   — трафик на цель Target (она в работу, остальные — в резерв);
//	           только если цель жива по проверке;
//	active   — цель в работу (к остальным рабочим);
//	reserve  — цель в резерв; последнюю рабочую — нельзя;
//	edit     — порт и вес;
//	move     — в маршрут To резервом; последнюю рабочую — нельзя;
//	remove   — убрать; только резервную.
//	swap     — обмен: резервная Target — в работу, рабочая With — в
//	           резерв (остальные цели не трогаются); Target — только
//	           живая по проверке.
type FlyOp struct {
	Op     string `json:"op"`
	Route  string `json:"route"`
	Target string `json:"target,omitempty"`
	To     string `json:"to,omitempty"`
	HostID int64  `json:"host_id,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Port   int    `json:"port,omitempty"`
	Weight int    `json:"weight,omitempty"`
	// With — у swap: рабочая цель, которая уходит в резерв.
	With string `json:"with,omitempty"`
}

// KindFlyOps — задание «изменения Traffic Fly с карты».
const KindFlyOps = "fly.ops"

// FlyOpsParams — операции и пометка к версии.
type FlyOpsParams struct {
	Ops  []FlyOp `json:"ops"`
	Note string  `json:"note,omitempty"`
}

// FlyOpsRunner применяет операции и раскладывает версию.
type FlyOpsRunner struct{ s *Server }

// NewFlyOpsRunner строит исполнителя.
func NewFlyOpsRunner(s *Server) *FlyOpsRunner { return &FlyOpsRunner{s: s} }

// Run — операции к текущей версии, одна новая версия, применение.
func (r *FlyOpsRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p FlyOpsParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	s := r.s
	user := s.actingUser(ctx, jc.Job.Author, s.firstAdmin(ctx))
	d := s.flyDoc(ctx)
	routes, err := s.flyApplyOps(ctx, d.Routes, p.Ops, s.flyAliveTargets(ctx))
	if err != nil {
		return err
	}
	for _, op := range p.Ops {
		jc.Log("fly.opDone", op.Op, op.Route, op.Target+op.Ref)
	}
	saved, err := s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: routes}, user, p.Note)
	if err != nil {
		return err
	}
	return s.flyApplyRun(ctx, jc, user, FlyApplyParams{Revision: saved.Revision})
}

// flyAliveTargets — «маршрут/цель» → жива по последней проверке шлюза.
func (s *Server) flyAliveTargets(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for _, g := range s.flyStatuses(ctx) {
		if g.Status == nil {
			continue
		}
		for _, r := range g.Status.Routes {
			for _, t := range r.Targets {
				out[r.ID+"/"+t.ID] = t.Checked && t.Alive
			}
		}
	}
	return out
}

// flyApplyOps — операции по порядку к копии маршрутов; первая ошибка —
// отказ целиком (ничего не записано).
func (s *Server) flyApplyOps(ctx context.Context, in []FlyRoute, ops []FlyOp, alive map[string]bool) ([]FlyRoute, error) {
	routes := make([]FlyRoute, len(in))
	for i, r := range in {
		r.Targets = slices.Clone(r.Targets)
		routes[i] = r
	}
	find := func(id string) (int, error) {
		i := slices.IndexFunc(routes, func(r FlyRoute) bool { return r.ID == id })
		if i < 0 {
			return -1, msgs.Errorf("fly.routeNotFound", id)
		}
		return i, nil
	}
	target := func(ri int, id string) (int, error) {
		ti := slices.IndexFunc(routes[ri].Targets, func(t FlyTarget) bool { return t.ID == id })
		if ti < 0 {
			return -1, msgs.Errorf("fly.opNoTarget", routes[ri].ID, id)
		}
		return ti, nil
	}
	activeCount := func(ri int) int {
		n := 0
		for _, t := range routes[ri].Targets {
			if !t.Drain {
				n++
			}
		}
		return n
	}
	newID := func(ri int, hostID int64) string {
		used := map[string]bool{}
		for _, t := range routes[ri].Targets {
			used[t.ID] = true
		}
		id := fmt.Sprintf("c%d", hostID)
		for n := 2; used[id]; n++ {
			id = fmt.Sprintf("c%d-%d", hostID, n)
		}
		return id
	}
	isRange := func(ri int) bool { return routes[ri].PortTo > routes[ri].Port }
	for _, op := range ops {
		ri, err := find(op.Route)
		if err != nil {
			return nil, err
		}
		switch op.Op {
		case "add":
			port := op.Port
			if isRange(ri) {
				port = routes[ri].Port
			}
			if port <= 0 || port > 65535 {
				return nil, msgs.Errorf("fly.opBadPort", op.Port)
			}
			// Новая цель — резервом: проверяется, но трафика не получает,
			// пока её не переключат.
			routes[ri].Targets = append(routes[ri].Targets, FlyTarget{ID: newID(ri, op.HostID), HostID: op.HostID, Kind: op.Kind, Ref: op.Ref, Port: port, Drain: true})
		case "switch":
			ti, err := target(ri, op.Target)
			if err != nil {
				return nil, err
			}
			if !alive[op.Route+"/"+op.Target] {
				return nil, msgs.Errorf("fly.opNotAlive", op.Route, op.Target)
			}
			for i := range routes[ri].Targets {
				routes[ri].Targets[i].Drain = i != ti
			}
		case "swap":
			// Одним шагом: двумя операциями маршрут на миг остался бы
			// без этой рабочей цели.
			ti, err := target(ri, op.Target)
			if err != nil {
				return nil, err
			}
			wi, err := target(ri, op.With)
			if err != nil {
				return nil, err
			}
			if !routes[ri].Targets[ti].Drain || routes[ri].Targets[wi].Drain || ti == wi {
				return nil, msgs.Errorf("fly.opSwapBad", op.Route, op.Target, op.With)
			}
			if !alive[op.Route+"/"+op.Target] {
				return nil, msgs.Errorf("fly.opNotAlive", op.Route, op.Target)
			}
			routes[ri].Targets[ti].Drain = false
			routes[ri].Targets[wi].Drain = true
		case "active":
			ti, err := target(ri, op.Target)
			if err != nil {
				return nil, err
			}
			routes[ri].Targets[ti].Drain = false
		case "reserve", "remove", "move":
			ti, err := target(ri, op.Target)
			if err != nil {
				return nil, err
			}
			t := routes[ri].Targets[ti]
			if !t.Drain && activeCount(ri) <= 1 {
				return nil, msgs.Errorf("fly.opLastActive", op.Route, op.Target)
			}
			switch op.Op {
			case "reserve":
				routes[ri].Targets[ti].Drain = true
			case "remove":
				if !t.Drain {
					return nil, msgs.Errorf("fly.opRemoveActive", op.Route, op.Target)
				}
				routes[ri].Targets = slices.Delete(routes[ri].Targets, ti, ti+1)
			case "move":
				to, err := find(op.To)
				if err != nil {
					return nil, err
				}
				if to == ri {
					continue
				}
				routes[ri].Targets = slices.Delete(routes[ri].Targets, ti, ti+1)
				t.ID, t.Drain = newID(to, t.HostID), true
				if isRange(to) {
					t.Port = routes[to].Port
				}
				routes[to].Targets = append(routes[to].Targets, t)
			}
		case "edit":
			ti, err := target(ri, op.Target)
			if err != nil {
				return nil, err
			}
			if op.Port != 0 && !isRange(ri) {
				if op.Port < 0 || op.Port > 65535 {
					return nil, msgs.Errorf("fly.opBadPort", op.Port)
				}
				routes[ri].Targets[ti].Port = op.Port
			}
			if op.Weight > 0 && op.Weight <= 100 {
				routes[ri].Targets[ti].Weight = op.Weight
			}
		default:
			return nil, msgs.Errorf("fly.opUnknown", op.Op)
		}
	}
	return routes, nil
}

// handleFlyOps — POST /hub/fly/ops {ops, note}: проверка на текущей
// версии и задание (в очередь Traffic Fly).
func (s *Server) handleFlyOps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req FlyOpsParams
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(req.Ops) == 0 || len(req.Ops) > 200 || !flyLabelRe.MatchString(req.Note) {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "fly.opUnknown", strconv.Itoa(len(req.Ops))))
		return
	}
	// Сразу видно, что не так: те же операции на текущей версии и
	// проверка результата, как при записи.
	d := s.flyDoc(ctx)
	routes, err := s.flyApplyOps(ctx, d.Routes, req.Ops, s.flyAliveTargets(ctx))
	if err == nil {
		pd := FlyDoc{Gateways: d.Gateways, Routes: routes, Alloc: d.Alloc}
		pd.normalize()
		err = s.flyValidate(ctx, pd)
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(ctx)
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindFlyOps, TitleKey: "fly.opsTitle", TitleArgs: []any{len(req.Ops)}, Queue: "fly", Author: user, Params: req})
	s.db.Audit(ctx, user, "fly.ops", strconv.Itoa(len(req.Ops)), auditOutcome(err), map[string]any{"ops": req.Ops})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}
