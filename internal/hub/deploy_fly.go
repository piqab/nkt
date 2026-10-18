package hub

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Маршруты Traffic Fly конвейера (compose.fly): после успешной выкладки
// целями маршрута становятся контейнеры сервиса на хостах выкладки.
// Маршрута нет — создаётся; есть — сверяется: вид, имена и сертификат не
// трогаются (расхождение — предупреждение), меняются только цели: новые
// добавляются и должны ожить, прежние сначала выводятся (без новых
// соединений) и только потом убираются. Ничего не изменилось — маршрут не
// перекладывается, только проверяется.

// deployFlyLog — журнал выкладки для применения Traffic Fly: шаги
// применения — строками, а не шагами задания выкладки.
type deployFlyLog struct{ jc flyJobLog }

// flyJobLog — журнал задания выкладки (и подмена в тестах).
type flyJobLog interface {
	flyLog
	Lang() msgs.Lang
}

func (l deployFlyLog) Log(key string, args ...any)               { l.jc.Log(key, args...) }
func (l deployFlyLog) Logf(format string, args ...any)           { l.jc.Logf(format, args...) }
func (l deployFlyLog) StepKey(_, _ int, key string, args ...any) { l.jc.Log(key, args...) }

// flyAliveWait — сколько ждать, пока новые цели пройдут проверки, прежде
// чем убрать прежние.
const flyAliveWait = 90 * time.Second

// pipelineFly — все блоки fly: стека; ошибка одного — в журнал, выкладка
// от неё не падает (стек уже обновлён).
func (s *Server) pipelineFly(ctx context.Context, jc *jobs.Context, user string, pl store.Pipeline, c *deploy.ComposeSpec, targets []targetHost) {
	if len(c.Fly) == 0 {
		return
	}
	// Контейнеры сервисов — по свежему скану хостов (заодно хост сразу
	// подхватывает новые адреса пересозданных контейнеров).
	containers := map[int64][]flyContainer{}
	for _, t := range targets {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		var res struct {
			Containers []flyContainer `json:"containers"`
		}
		_, err := s.hostCall(cctx, user, t.ID, "POST", "/api/fly/refresh", nil, &res)
		cancel()
		if err != nil {
			jc.Log("fly.pipelineHostFailed", t.Name, msgs.Localize(jc.Lang(), err))
			continue
		}
		containers[t.ID] = res.Containers
	}
	for _, f := range c.Fly {
		if err := s.pipelineFlyRoute(ctx, jc, user, pl, c.Project, f, targets, containers); err != nil {
			jc.Log("fly.pipelineFailed", f.Route, msgs.Localize(jc.Lang(), err))
		}
	}
}

// flyContainer — контейнер хоста из /api/fly/refresh.
type flyContainer struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	Service string `json:"service"`
	State   string `json:"state"`
	IP      string `json:"ip"`
}

// flyPipelineGateway — шлюз по имени из описания (хост, адрес, edge);
// пусто — единственный.
func (s *Server) flyPipelineGateway(ctx context.Context, want string) (flyGatewayJSON, error) {
	gws := s.flyGateways(ctx)
	if want == "" {
		if len(gws) == 1 {
			return gws[0], nil
		}
		return flyGatewayJSON{}, msgs.Errorf("fly.pipelineGatewayAmbiguous", len(gws))
	}
	for _, g := range gws {
		if strings.EqualFold(g.Name, want) || g.Address == want {
			return g, nil
		}
		for _, e := range s.edges(ctx) {
			if e.ID == g.EdgeID && (strings.EqualFold(edgeName(e), want) || strings.EqualFold(e.Domain, want)) {
				return g, nil
			}
			if h, _, err := net.SplitHostPort(e.Address); err == nil && e.ID == g.EdgeID && h == want {
				return g, nil
			}
		}
	}
	return flyGatewayJSON{}, msgs.Errorf("fly.pipelineGatewayNotFound", want)
}

// flyTargetKey — цель для сравнения: где и что (без веса, вывода, id).
func flyTargetKey(t FlyTarget) string {
	return fmt.Sprintf("%d/%s/%s/%d", t.HostID, t.Kind, t.Ref, t.Port)
}

func (s *Server) pipelineFlyRoute(ctx context.Context, jc flyJobLog, user string, pl store.Pipeline, project string, f deploy.FlySpec,
	targets []targetHost, containers map[int64][]flyContainer) error {
	gw, err := s.flyPipelineGateway(ctx, f.Gateway)
	if err != nil {
		return err
	}
	// Цели, которые должны быть: контейнер сервиса на каждом хосте.
	var want []FlyTarget
	for _, t := range targets {
		cs, ok := containers[t.ID]
		if !ok {
			continue
		}
		i := slices.IndexFunc(cs, func(c flyContainer) bool { return c.Project == project && c.Service == f.Service })
		if i < 0 {
			jc.Log("fly.pipelineNoContainer", f.Route, f.Service, t.Name)
			continue
		}
		port := f.TargetPort
		if f.PortTo > 0 {
			port = f.Port
		}
		want = append(want, FlyTarget{ID: fmt.Sprintf("c%d", t.ID), HostID: t.ID, Kind: "container", Ref: cs[i].Name, Port: port})
	}
	if len(want) == 0 {
		return msgs.Errorf("fly.pipelineNoTargets", f.Service)
	}

	d := s.flyDoc(ctx)
	idx := slices.IndexFunc(d.Routes, func(r FlyRoute) bool { return r.ID == f.Route })
	note := msgs.T(jc.Lang(), "fly.pipelineNote", pl.Name)
	log := deployFlyLog{jc}
	if idx < 0 {
		r := flyRouteFromSpec(f, gw.EdgeID)
		r.Targets = want
		saved, err := s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: append(d.Routes, r)}, user, note)
		if err != nil {
			return err
		}
		jc.Log("fly.pipelineCreated", f.Route, len(want))
		return s.flyApplyRun(ctx, log, user, FlyApplyParams{Revision: saved.Revision})
	}

	cur := d.Routes[idx]
	if diff := flySpecDiff(cur, f, gw.EdgeID); len(diff) > 0 {
		jc.Log("fly.pipelineDiffers", f.Route, strings.Join(diff, ", "))
	}
	wantKeys := map[string]bool{}
	for _, t := range want {
		wantKeys[flyTargetKey(t)] = true
	}
	haveKeys := map[string]bool{}
	for _, t := range cur.Targets {
		haveKeys[flyTargetKey(t)] = true
	}
	same := len(wantKeys) == len(haveKeys)
	for k := range wantKeys {
		same = same && haveKeys[k]
	}
	undrained := !slices.ContainsFunc(cur.Targets, func(t FlyTarget) bool { return t.Drain && wantKeys[flyTargetKey(t)] })
	if same && undrained {
		// Ничего не изменилось: не перекладывать, только проверить.
		jc.Log("fly.pipelineUnchanged", f.Route)
		s.flyPipelineCheck(ctx, jc, cur)
		return nil
	}

	// Цели поменялись: новые — добавить, прежние, которых больше нет, —
	// вывести (новых соединений не получают, открытые доживают).
	next := cur
	next.Targets = nil
	var added, old []string
	used := map[string]bool{}
	for _, t := range cur.Targets {
		used[t.ID] = true
		if wantKeys[flyTargetKey(t)] {
			t.Drain = false
			next.Targets = append(next.Targets, t)
			continue
		}
		t.Drain = true
		old = append(old, t.ID)
		next.Targets = append(next.Targets, t)
	}
	for _, t := range want {
		if haveKeys[flyTargetKey(t)] {
			continue
		}
		id := t.ID
		for n := 2; used[id]; n++ {
			id = fmt.Sprintf("%s-%d", t.ID, n)
		}
		used[id] = true
		t.ID = id
		next.Targets = append(next.Targets, t)
		added = append(added, id)
	}
	routes := slices.Clone(d.Routes)
	routes[idx] = next
	saved, err := s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: routes}, user, note)
	if err != nil {
		return err
	}
	jc.Log("fly.pipelineTargets", f.Route, len(added), len(old))
	if err := s.flyApplyRun(ctx, log, user, FlyApplyParams{Revision: saved.Revision}); err != nil {
		return err
	}
	if len(old) == 0 {
		return nil
	}
	// Прежние убрать, только когда новые ожили; не ожили за flyAliveWait —
	// оставить прежние в работе (снять вывод) и сказать.
	alive := s.flyWaitAlive(ctx, f.Route, added)
	d = s.flyDoc(ctx)
	idx = slices.IndexFunc(d.Routes, func(r FlyRoute) bool { return r.ID == f.Route })
	if idx < 0 {
		return nil
	}
	r := d.Routes[idx]
	var keep []FlyTarget
	for _, t := range r.Targets {
		if slices.Contains(old, t.ID) {
			if alive {
				continue
			}
			t.Drain = false
		}
		keep = append(keep, t)
	}
	r.Targets = keep
	routes = slices.Clone(d.Routes)
	routes[idx] = r
	if !alive {
		jc.Log("fly.pipelineNotAlive", f.Route, int(flyAliveWait.Seconds()))
	} else {
		jc.Log("fly.pipelineOldRemoved", f.Route, len(old))
	}
	saved, err = s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: routes}, user, note)
	if err != nil {
		return err
	}
	return s.flyApplyRun(ctx, log, user, FlyApplyParams{Revision: saved.Revision})
}

// flyWaitAlive — все цели ids маршрута живы по сводке шлюза.
func (s *Server) flyWaitAlive(ctx context.Context, route string, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	deadline := time.Now().Add(flyAliveWait)
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, 5*time.Second) {
			return false
		}
		live := 0
		for _, g := range s.flyStatuses(ctx) {
			if g.Status == nil {
				continue
			}
			for _, r := range g.Status.Routes {
				if r.ID != route {
					continue
				}
				for _, t := range r.Targets {
					if slices.Contains(ids, t.ID) && t.Checked && t.Alive {
						live++
					}
				}
			}
		}
		if live == len(ids) {
			return true
		}
	}
	return false
}

// flyPipelineCheck — маршрут на месте: цели живы, сертификаты действуют.
func (s *Server) flyPipelineCheck(ctx context.Context, jc flyJobLog, r FlyRoute) {
	for _, g := range s.flyStatuses(ctx) {
		if g.EdgeID != r.Gateway || g.Status == nil {
			continue
		}
		for _, rs := range g.Status.Routes {
			if rs.ID != r.ID {
				continue
			}
			for _, t := range rs.Targets {
				if t.Alive {
					jc.Log("fly.pipelineTargetAlive", r.ID, t.ID)
				} else {
					jc.Log("fly.pipelineTargetDown", r.ID, t.ID, t.LastError)
				}
			}
		}
		var certs []fly.CertStatus
		for _, c := range g.Status.Certs {
			if slices.Contains(r.Names, c.Name) {
				certs = append(certs, c)
			}
		}
		s.flyLogCerts(deployFlyLog{jc}, fly.GatewayStatus{Certs: certs})
	}
}

// flyRouteFromSpec — новый маршрут по описанию конвейера.
func flyRouteFromSpec(f deploy.FlySpec, gateway int64) FlyRoute {
	r := FlyRoute{ID: f.Route, Gateway: gateway, Kind: f.Kind, Names: f.Names, Port: f.Port, PortTo: f.PortTo,
		ProxyProtocol: f.ProxyProtocol, TLS: f.TLS == "gateway", Check: fly.Check{Kind: f.Check, Path: f.CheckPath, Port: f.CheckPort}}
	r.Algorithm = fly.AlgoSourceHash
	if f.Named() {
		r.Algorithm = fly.AlgoLeastConn
	}
	return r
}

// flySpecDiff — чем существующий маршрут отличается от описания (кроме
// целей): такое конвейер не перезаписывает.
func flySpecDiff(r FlyRoute, f deploy.FlySpec, gateway int64) []string {
	var out []string
	if r.Gateway != gateway {
		out = append(out, "gateway")
	}
	if r.Kind != f.Kind {
		out = append(out, "kind")
	}
	if f.Named() && !slices.Equal(r.Names, f.Names) {
		out = append(out, "names")
	}
	if r.Port != f.Port || r.PortTo != f.PortTo {
		out = append(out, "port")
	}
	if r.TLS != (f.TLS == "gateway") {
		out = append(out, "tls")
	}
	if r.ProxyProtocol != f.ProxyProtocol {
		out = append(out, "proxy_protocol")
	}
	return out
}

// dryRunFly — что выкладка сделает с маршрутами Traffic Fly: шлюз есть,
// маршрут будет создан или сверен (что в описании отличается — не
// перезапишется). Ничего не меняет; ответ — число проблем.
func (s *Server) dryRunFly(ctx context.Context, jc flyJobLog, c *deploy.ComposeSpec) int {
	problems := 0
	d := s.flyDoc(ctx)
	for _, f := range c.Fly {
		gw, err := s.flyPipelineGateway(ctx, f.Gateway)
		if err != nil {
			jc.Log("fly.pipelineFailed", f.Route, msgs.Localize(jc.Lang(), err))
			problems++
			continue
		}
		i := slices.IndexFunc(d.Routes, func(r FlyRoute) bool { return r.ID == f.Route })
		if i < 0 {
			jc.Log("fly.dryWillCreate", f.Route, gw.Name, f.Service, f.TargetPort)
			continue
		}
		jc.Log("fly.dryWillReconcile", f.Route, f.Service)
		if diff := flySpecDiff(d.Routes[i], f, gw.EdgeID); len(diff) > 0 {
			jc.Log("fly.pipelineDiffers", f.Route, strings.Join(diff, ", "))
		}
	}
	return problems
}
