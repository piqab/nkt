package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Экспорт и импорт того, что хаб хранит в kv про выкладки и сеть: Traffic
// Fly, где выложены стеки конвейеров, учёт переносов контейнеров. Номера
// хостов и edge в другом хабе другие — в файле рядом с ними имена хостов и
// адреса туннелей edge, при импорте номера пересчитываются по ним.

// flyExport — Traffic Fly в файле экспорта.
type flyExport struct {
	Doc      FlyDoc       `json:"doc"`
	Versions []FlyVersion `json:"versions,omitempty"`
	// Hosts — номер хоста → имя; Gateways — номер edge → адрес туннеля.
	Hosts    map[string]string `json:"hosts"`
	Gateways map[string]string `json:"gateways"`
}

func (m *Manager) loadFly(ctx context.Context) (FlyDoc, []FlyVersion) {
	var d FlyDoc
	if raw, ok, err := m.db.KVGet(ctx, flyDocKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	if d.Routes == nil {
		d.Routes = []FlyRoute{}
	}
	if d.Gateways == nil {
		d.Gateways = []FlyGateway{}
	}
	var vs []FlyVersion
	if raw, ok, err := m.db.KVGet(ctx, flyVersionsKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &vs)
	}
	return d, vs
}

// exportFly — документ, история и словари номеров.
func (m *Manager) exportFly(ctx context.Context, export *store.HubExport) {
	d, vs := m.loadFly(ctx)
	if len(d.Routes) == 0 && len(vs) == 0 {
		return
	}
	byID, _ := m.hostNames(ctx)
	fe := flyExport{Doc: d, Versions: vs, Hosts: map[string]string{}, Gateways: map[string]string{}}
	note := func(doc FlyDoc) {
		for _, r := range doc.Routes {
			for _, t := range r.Targets {
				if n, ok := byID[t.HostID]; ok {
					fe.Hosts[strconv.FormatInt(t.HostID, 10)] = n
				}
			}
		}
	}
	note(d)
	for _, v := range vs {
		note(v.Doc)
	}
	for _, e := range loadEdges(ctx, m.db) {
		if e.Address != "" {
			fe.Gateways[strconv.FormatInt(e.ID, 10)] = e.Address
		}
	}
	export.Fly, _ = json.Marshal(fe)
}

// flyRemap — номера файла → номера этого хаба.
type flyRemap struct {
	host     map[int64]int64
	hostName map[int64]string
	gw       map[int64]int64
	gwAddr   map[int64]string
}

func (m *Manager) newFlyRemap(ctx context.Context, fe flyExport) flyRemap {
	_, byName := m.hostNames(ctx)
	r := flyRemap{host: map[int64]int64{}, hostName: map[int64]string{}, gw: map[int64]int64{}, gwAddr: map[int64]string{}}
	for k, name := range fe.Hosts {
		old, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			continue
		}
		r.hostName[old] = name
		if id, ok := byName[name]; ok {
			r.host[old] = id
		}
	}
	byAddr := map[string]int64{}
	for _, e := range loadEdges(ctx, m.db) {
		byAddr[e.Address] = e.ID
	}
	for k, addr := range fe.Gateways {
		old, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			continue
		}
		r.gwAddr[old] = addr
		if id, ok := byAddr[addr]; ok {
			r.gw[old] = id
		}
	}
	return r
}

// flyDropped — цель, которую не на что переложить.
type flyDropped struct{ target, host string }

// route — маршрут в номерах этого хаба; gwMissing — нет шлюза (маршрут
// не переносится), dropped — цели без хоста (переносится без них).
func (r flyRemap) route(rt FlyRoute) (out FlyRoute, gwMissing string, dropped []flyDropped) {
	gw, ok := r.gw[rt.Gateway]
	if !ok {
		addr := r.gwAddr[rt.Gateway]
		if addr == "" {
			addr = "#" + strconv.FormatInt(rt.Gateway, 10)
		}
		return rt, addr, nil
	}
	out = rt
	out.Gateway = gw
	out.Names = append([]string(nil), rt.Names...)
	out.Allow = append([]string(nil), rt.Allow...)
	out.Targets = []FlyTarget{}
	for _, t := range rt.Targets {
		h, ok := r.host[t.HostID]
		if !ok {
			name := r.hostName[t.HostID]
			if name == "" {
				name = "#" + strconv.FormatInt(t.HostID, 10)
			}
			label := t.Label
			if label == "" {
				label = t.ID
			}
			dropped = append(dropped, flyDropped{target: label, host: name})
			continue
		}
		t.HostID = h
		out.Targets = append(out.Targets, t)
	}
	return out, "", dropped
}

// allocKey — ключ раздачи адресов ("gw/host[/route/target]") в номерах
// этого хаба.
func (r flyRemap) allocKey(k string) (string, bool) {
	parts := strings.SplitN(k, "/", 3)
	if len(parts) < 2 {
		return "", false
	}
	gw, err1 := strconv.ParseInt(parts[0], 10, 64)
	host, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return "", false
	}
	ng, ok1 := r.gw[gw]
	nh, ok2 := r.host[host]
	if !ok1 || !ok2 {
		return "", false
	}
	out := fmt.Sprintf("%d/%d", ng, nh)
	if len(parts) == 3 {
		out += "/" + parts[2]
	}
	return out, true
}

// flyRouteName — маршрут в плане и отчёте.
func flyRouteName(r FlyRoute) string {
	if r.Label != "" {
		return r.Label
	}
	return r.ID
}

func parseFlyExport(raw json.RawMessage) (flyExport, bool) {
	var fe flyExport
	if len(raw) == 0 || json.Unmarshal(raw, &fe) != nil {
		return fe, false
	}
	return fe, true
}

// planFly — маршруты файла; совпадение — маршрут с тем же id здесь.
func (m *Manager) planFly(ctx context.Context, export store.HubExport) store.PlanSection {
	sec := store.PlanSection{Section: store.SectionFly, Items: []store.PlanItem{}}
	fe, ok := parseFlyExport(export.Fly)
	if !ok {
		return sec
	}
	cur, _ := m.loadFly(ctx)
	have := map[string]bool{}
	for _, r := range cur.Routes {
		have[r.ID] = true
	}
	for _, r := range fe.Doc.Routes {
		sec.Items = append(sec.Items, store.PlanItem{Name: r.ID, Conflict: have[r.ID], Replaceable: true})
	}
	return sec
}

// importFly — маршруты файла в документ этого хаба (новые — добавляются,
// совпавшие по id — заменяются по выбору). Раздача адресов туннелей и
// портов берётся из файла, где здесь она свободна: адреса на шлюзах и
// хостах остаются прежними. Историю файл приносит, только если здесь
// Traffic Fly ещё не было. Разложить по узлам — «Применить» в Traffic Fly.
func (m *Manager) importFly(ctx context.Context, raw json.RawMessage, res store.ImportResolutions, rep *store.ImportReport) {
	fe, ok := parseFlyExport(raw)
	if !ok {
		if len(raw) > 0 {
			rep.Err("fly: %v", json.Unmarshal(raw, &flyExport{}))
		}
		return
	}
	if len(fe.Doc.Routes) == 0 {
		return
	}
	cnt := rep.Count(store.SectionFly)
	rm := m.newFlyRemap(ctx, fe)
	flyMu.Lock()
	defer flyMu.Unlock()
	cur, curVs := m.loadFly(ctx)
	fresh := len(cur.Routes) == 0 && len(curVs) == 0
	idx := map[string]int{}
	for i, r := range cur.Routes {
		idx[r.ID] = i
	}
	changed := false
	for _, rt := range fe.Doc.Routes {
		i, exists := idx[rt.ID]
		if exists && !res.Replace(store.SectionFly, rt.ID) {
			cnt.Skipped++
			continue
		}
		nr, gwMissing, dropped := rm.route(rt)
		if gwMissing != "" {
			rep.Errors = append(rep.Errors, msgs.Tc(ctx, "hub.importFlyNoGateway", flyRouteName(rt), gwMissing))
			continue
		}
		for _, d := range dropped {
			rep.Errors = append(rep.Errors, msgs.Tc(ctx, "hub.importFlyNoHost", flyRouteName(rt), d.target, d.host))
		}
		if exists {
			cur.Routes[i] = nr
			cnt.Replaced++
		} else {
			idx[nr.ID] = len(cur.Routes)
			cur.Routes = append(cur.Routes, nr)
			cnt.Added++
		}
		changed = true
	}
	if !changed {
		return
	}
	// Шлюзы: номер интерфейса и порт WireGuard — из файла, если свободны.
	takenIdx := map[int]bool{}
	haveGw := map[int64]bool{}
	for _, g := range cur.Gateways {
		takenIdx[g.Index], haveGw[g.EdgeID] = true, true
	}
	for _, g := range fe.Doc.Gateways {
		id, ok := rm.gw[g.EdgeID]
		if !ok || haveGw[id] || takenIdx[g.Index] {
			continue
		}
		g.EdgeID = id
		cur.Gateways = append(cur.Gateways, g)
		takenIdx[g.Index], haveGw[id] = true, true
	}
	cur.Alloc = mergeFlyAlloc(cur.Alloc, fe.Doc.Alloc, rm)
	cur.normalize()
	if fresh {
		for _, v := range fe.Versions {
			var routes []FlyRoute
			for _, rt := range v.Doc.Routes {
				if nr, miss, _ := rm.route(rt); miss == "" {
					routes = append(routes, nr)
				}
			}
			v.Doc.Routes = routes
			v.Doc.Gateways = nil
			v.Doc.Alloc = FlyAlloc{}
			v.Doc.normalize()
			curVs = append(curVs, v)
		}
		if fe.Doc.Revision > cur.Revision {
			cur.Revision = fe.Doc.Revision
		}
	}
	cur.Revision++
	curVs = append([]FlyVersion{{N: cur.Revision, TS: time.Now().UTC().Format(time.RFC3339), User: "import", Note: msgs.Tc(ctx, "store.importReplacedNote"), Doc: cur}}, curVs...)
	if len(curVs) > flyMaxVersions {
		curVs = curVs[:flyMaxVersions]
	}
	rawDoc, _ := json.Marshal(cur)
	rawVs, _ := json.Marshal(curVs)
	if err := m.db.KVSet(ctx, flyDocKey, string(rawDoc)); err != nil {
		rep.Err("fly: %v", err)
		return
	}
	if err := m.db.KVSet(ctx, flyVersionsKey, string(rawVs)); err != nil {
		rep.Err("fly: %v", err)
	}
	rep.Errors = append(rep.Errors, msgs.Tc(ctx, "hub.importFlyApply"))
}

// mergeFlyAlloc — раздача адресов из файла там, где ключ здесь свободен и
// номер в своей группе (шлюз, шлюз+хост) не занят.
func mergeFlyAlloc(cur, file FlyAlloc, rm flyRemap) FlyAlloc {
	out := FlyAlloc{Hosts: map[string]int{}, Ports: map[string]int{}}
	for k, v := range cur.Hosts {
		out.Hosts[k] = v
	}
	for k, v := range cur.Ports {
		out.Ports[k] = v
	}
	group := func(k string, n int) string {
		parts := strings.SplitN(k, "/", n+1)
		return strings.Join(parts[:n], "/")
	}
	merge := func(dst, src map[string]int, n int) {
		busy := map[string]bool{}
		for k, v := range dst {
			busy[group(k, n)+"#"+strconv.Itoa(v)] = true
		}
		for k, v := range src {
			nk, ok := rm.allocKey(k)
			if !ok {
				continue
			}
			if _, taken := dst[nk]; taken || busy[group(nk, n)+"#"+strconv.Itoa(v)] {
				continue
			}
			dst[nk] = v
			busy[group(nk, n)+"#"+strconv.Itoa(v)] = true
		}
	}
	merge(out.Hosts, file.Hosts, 1)
	merge(out.Ports, file.Ports, 2)
	return out
}

// exportDeployed — где выложены стеки конвейеров и неубранные старые.
func (m *Manager) exportDeployed(ctx context.Context, export *store.HubExport) {
	list, err := m.db.ListPipelines(ctx)
	if err != nil {
		return
	}
	ids := map[string]int64{}
	for _, p := range list {
		ids[p.Name] = p.ID
	}
	byID, _ := m.hostNames(ctx)
	for i := range export.Pipelines {
		id, ok := ids[export.Pipelines[i].Name]
		if !ok {
			continue
		}
		if raw, ok, err := m.db.KVGet(ctx, deployedKey(id)); err == nil && ok && raw != "" {
			var d deployedStack
			if json.Unmarshal([]byte(raw), &d) == nil && d.Project != "" {
				de := &store.DeployedExport{Project: d.Project, Hosts: []string{}}
				for _, h := range d.Hosts {
					name := h.Name
					if n, ok := byID[h.ID]; ok {
						name = n
					}
					de.Hosts = append(de.Hosts, name)
				}
				export.Pipelines[i].Deployed = de
			}
		}
		if raw, ok, err := m.db.KVGet(ctx, stoppedKey(id)); err == nil && ok && raw != "" {
			var st pipelineStopped
			if json.Unmarshal([]byte(raw), &st) == nil && len(st.Hosts) > 0 {
				se := &store.StoppedExport{By: st.By, At: st.At, Project: st.Project, Hosts: []string{}, Containers: map[string][]string{}}
				for _, h := range st.Hosts {
					name := h.Name
					if n, ok := byID[h.ID]; ok {
						name = n
					}
					se.Hosts = append(se.Hosts, name)
					if cs := st.Containers[strconv.FormatInt(h.ID, 10)]; len(cs) > 0 {
						se.Containers[name] = cs
					}
				}
				export.Pipelines[i].Stopped = se
			}
		}
		if raw, ok, err := m.db.KVGet(ctx, leftoversKey(id)); err == nil && ok && raw != "" {
			var ls []leftoverStack
			if json.Unmarshal([]byte(raw), &ls) == nil {
				for _, l := range ls {
					name := l.Host
					if n, ok := byID[l.HostID]; ok {
						name = n
					}
					export.Pipelines[i].Leftovers = append(export.Pipelines[i].Leftovers, store.LeftoverExport{Host: name, Project: l.Project, Reason: l.Reason, At: l.At})
				}
			}
		}
	}
}

// importDeployed — состояние выкладки для конвейеров, которые импорт
// добавил или заменил (existing — имена конвейеров до импорта).
func (m *Manager) importDeployed(ctx context.Context, list []store.PipelineExport, existing map[string]bool, res store.ImportResolutions, rep *store.ImportReport) {
	pipes, err := m.db.ListPipelines(ctx)
	if err != nil {
		return
	}
	ids := map[string]int64{}
	for _, p := range pipes {
		ids[p.Name] = p.ID
	}
	_, byName := m.hostNames(ctx)
	for _, p := range list {
		if p.Deployed == nil && len(p.Leftovers) == 0 && p.Stopped == nil {
			continue
		}
		if existing[p.Name] && !res.Replace(store.SectionPipelines, p.Name) {
			continue
		}
		id, ok := ids[p.Name]
		if !ok {
			continue
		}
		if p.Deployed != nil {
			d := deployedStack{Project: p.Deployed.Project, Hosts: []deployedHost{}}
			for _, h := range p.Deployed.Hosts {
				hid, ok := byName[h]
				if !ok {
					rep.Errors = append(rep.Errors, msgs.Tc(ctx, "hub.importDeployedNoHost", p.Name, h))
					continue
				}
				d.Hosts = append(d.Hosts, deployedHost{ID: hid, Name: h})
			}
			raw, _ := json.Marshal(d)
			if err := m.db.KVSet(ctx, deployedKey(id), string(raw)); err != nil {
				rep.Err("%s: %v", p.Name, err)
			}
		}
		if p.Stopped != nil {
			st := pipelineStopped{By: p.Stopped.By, At: p.Stopped.At, Project: p.Stopped.Project, Containers: map[string][]string{}}
			for _, h := range p.Stopped.Hosts {
				if hid, ok := byName[h]; ok {
					st.Hosts = append(st.Hosts, deployedHost{ID: hid, Name: h})
					if cs := p.Stopped.Containers[h]; len(cs) > 0 {
						st.Containers[strconv.FormatInt(hid, 10)] = cs
					}
				}
			}
			if len(st.Hosts) > 0 {
				raw, _ := json.Marshal(st)
				if err := m.db.KVSet(ctx, stoppedKey(id), string(raw)); err != nil {
					rep.Err("%s: %v", p.Name, err)
				}
			}
		}
		if len(p.Leftovers) > 0 {
			ls := []leftoverStack{}
			for _, l := range p.Leftovers {
				// Хоста здесь нет — строка остаётся с именем: «Убрать» его
				// не найдёт, «Забыть» уберёт.
				ls = append(ls, leftoverStack{HostID: byName[l.Host], Host: l.Host, Project: l.Project, Reason: l.Reason, At: l.At})
			}
			raw, _ := json.Marshal(ls)
			if err := m.db.KVSet(ctx, leftoversKey(id), string(raw)); err != nil {
				rep.Err("%s: %v", p.Name, err)
			}
		}
	}
}

func (m *Manager) loadMigrationsKV(ctx context.Context) []migrateRecord {
	var out []migrateRecord
	if raw, ok, err := m.db.KVGet(ctx, migrationsKey); err == nil && ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// exportMigrations — учёт переносов (имена хостов в записях уже есть).
func (m *Manager) exportMigrations(ctx context.Context, export *store.HubExport) {
	list := m.loadMigrationsKV(ctx)
	if len(list) == 0 {
		return
	}
	byID, _ := m.hostNames(ctx)
	for i := range list {
		if n, ok := byID[list[i].From]; ok {
			list[i].FromName = n
		}
		if n, ok := byID[list[i].To]; ok {
			list[i].ToName = n
		}
	}
	export.Migrations, _ = json.Marshal(list)
}

// migrationName — запись переноса в плане и отчёте.
func migrationName(r migrateRecord) string {
	ts := r.TS
	if len(ts) > 16 {
		ts = ts[:16]
	}
	return fmt.Sprintf("%s · %s → %s · %s", r.Project, r.FromName, r.ToName, ts)
}

func parseMigrations(raw json.RawMessage) []migrateRecord {
	var list []migrateRecord
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &list)
	}
	return list
}

func (m *Manager) planMigrations(ctx context.Context, export store.HubExport) store.PlanSection {
	sec := store.PlanSection{Section: store.SectionMigrations, Items: []store.PlanItem{}}
	have := map[string]bool{}
	for _, r := range m.loadMigrationsKV(ctx) {
		have[r.ID] = true
	}
	for _, r := range parseMigrations(export.Migrations) {
		sec.Items = append(sec.Items, store.PlanItem{Name: migrationName(r), Conflict: have[r.ID], Replaceable: true})
	}
	return sec
}

// importMigrations — записи переносов: хосты — по именам; совпавшие по id
// — замена по выбору.
func (m *Manager) importMigrations(ctx context.Context, raw json.RawMessage, res store.ImportResolutions, rep *store.ImportReport) {
	list := parseMigrations(raw)
	if len(list) == 0 {
		return
	}
	cnt := rep.Count(store.SectionMigrations)
	_, byName := m.hostNames(ctx)
	migrationsMu.Lock()
	defer migrationsMu.Unlock()
	cur := m.loadMigrationsKV(ctx)
	idx := map[string]int{}
	for i, r := range cur {
		idx[r.ID] = i
	}
	changed := false
	for _, r := range list {
		if r.ID == "" {
			continue
		}
		name := migrationName(r)
		i, exists := idx[r.ID]
		if exists && !res.Replace(store.SectionMigrations, name) {
			cnt.Skipped++
			continue
		}
		from, ok1 := byName[r.FromName]
		to, ok2 := byName[r.ToName]
		if !ok1 || !ok2 {
			missing := r.FromName
			if ok1 {
				missing = r.ToName
			}
			rep.Errors = append(rep.Errors, msgs.Tc(ctx, "hub.importMigrationNoHost", name, missing))
			continue
		}
		r.From, r.To = from, to
		if exists {
			cur[i] = r
			cnt.Replaced++
		} else {
			idx[r.ID] = len(cur)
			cur = append(cur, r)
			cnt.Added++
		}
		changed = true
	}
	if !changed {
		return
	}
	out, _ := json.Marshal(cur)
	if err := m.db.KVSet(ctx, migrationsKey, string(out)); err != nil {
		rep.Err("migrations: %v", err)
	}
}
