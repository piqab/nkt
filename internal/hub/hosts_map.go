package hub

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
)

// Карта хостов (раздел «Хосты», переключатель «Карта»): у каждого хоста
// на связи — контейнеры, машины и LXD; слева — маршруты Traffic Fly и
// конвейеры выкладок со связями к своим контейнерам. Списки — тем же
// путём и из того же кэша, что панель «Контейнеры и машины» Traffic Fly.

// mapHostJSON — содержимое хоста на карте.
type mapHostJSON struct {
	Items []flyTargetItem `json:"items"`
	// Error — хост не ответил ни по одному виду (нет связи и т. п.).
	Error string `json:"error,omitempty"`
}

// mapTargetJSON — цель маршрута на карте.
type mapTargetJSON struct {
	HostID  int64  `json:"host_id"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref,omitempty"`
	Port    int    `json:"port"`
	Drain   bool   `json:"drain,omitempty"`
	Alive   bool   `json:"alive"`
	Checked bool   `json:"checked"`
}

// mapRouteJSON — маршрут Traffic Fly.
type mapRouteJSON struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Names   []string        `json:"names,omitempty"`
	Port    int             `json:"port"`
	PortTo  int             `json:"port_to,omitempty"`
	Gateway string          `json:"gateway"`
	Targets []mapTargetJSON `json:"targets"`
}

// mapPipelineJSON — конвейер compose: стек и где он выложен.
type mapPipelineJSON struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Project string  `json:"project"`
	Status  string  `json:"status,omitempty"`
	Hosts   []int64 `json:"hosts"`
}

// mapParallel — сколько хостов опрашивать сразу.
const mapParallel = 8

// handleHostsMap — GET /hub/map[?refresh=1].
func (s *Server) handleHostsMap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := auth.Username(ctx)
	refresh := r.URL.Query().Get("refresh") == "1"
	rows, err := s.hostRows(ctx)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	allow := s.scopeFilter(ctx)
	hosts := map[int64]*mapHostJSON{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, mapParallel)
	for _, h := range rows {
		if allow != nil && !allow(h.ID) {
			continue
		}
		if h.Status != "online" || h.IsPlugin() {
			continue
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out := &mapHostJSON{}
			// Каждый вид — отдельно: у хоста без LXD или libvirt этот вид
			// просто пуст; ошибка хоста — только если не ответил ни один.
			fails := 0
			var first error
			for _, k := range []string{"container", "vm", "lxd"} {
				items, err := s.flyTargetsCached(ctx, user, id, []string{k}, refresh)
				if err != nil {
					fails++
					if first == nil {
						first = err
					}
				}
				out.Items = append(out.Items, items...)
			}
			if fails == 3 && first != nil {
				out.Error = msgs.Localize(msgs.FromContext(ctx), first)
			}
			mu.Lock()
			hosts[id] = out
			mu.Unlock()
		}(h.ID)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{
		"hosts":     hosts,
		"routes":    s.mapRoutes(ctx, allow),
		"pipelines": s.mapPipelines(ctx, allow),
	})
}

// mapRoutes — маршруты Traffic Fly с живостью целей.
func (s *Server) mapRoutes(ctx context.Context, allow func(int64) bool) []mapRouteJSON {
	d := s.flyDoc(ctx)
	if len(d.Routes) == 0 {
		return []mapRouteJSON{}
	}
	gw := map[int64]string{}
	for _, g := range s.flyGateways(ctx) {
		gw[g.EdgeID] = g.Name
	}
	alive := map[string][2]bool{}
	for _, g := range s.flyStatuses(ctx) {
		if g.Status == nil {
			continue
		}
		for _, r := range g.Status.Routes {
			for _, t := range r.Targets {
				alive[r.ID+"/"+t.ID] = [2]bool{t.Alive, t.Checked}
			}
		}
	}
	out := []mapRouteJSON{}
	for _, r := range d.Routes {
		mr := mapRouteJSON{ID: r.ID, Kind: r.Kind, Names: r.Names, Port: r.Port, PortTo: r.PortTo, Gateway: gw[r.Gateway], Targets: []mapTargetJSON{}}
		for _, t := range r.Targets {
			if allow != nil && !allow(t.HostID) {
				continue
			}
			a := alive[r.ID+"/"+t.ID]
			mr.Targets = append(mr.Targets, mapTargetJSON{HostID: t.HostID, Kind: t.Kind, Ref: t.Ref, Port: t.Port, Drain: t.Drain, Alive: a[0], Checked: a[1]})
		}
		out = append(out, mr)
	}
	return out
}

// mapPipelines — конвейеры compose: стек и хосты, где он выложен (по
// последней выкладке; не выкладывался — по описанию).
func (s *Server) mapPipelines(ctx context.Context, allow func(int64) bool) []mapPipelineJSON {
	out := []mapPipelineJSON{}
	list, err := s.db.ListPipelines(ctx)
	if err != nil {
		return out
	}
	for _, p := range list {
		if !s.pipelineInScope(ctx, p) {
			continue
		}
		spec, err := deploy.ParseSpec(p.Content)
		if err != nil || spec.Action != deploy.ActionCompose || spec.Compose == nil {
			continue
		}
		mp := mapPipelineJSON{ID: p.ID, Name: p.Name, Project: spec.Compose.Project, Hosts: []int64{}}
		if st := s.loadDeployed(ctx, p.ID); st != nil {
			mp.Project = st.Project
			for _, h := range st.Hosts {
				if allow == nil || allow(h.ID) {
					mp.Hosts = append(mp.Hosts, h.ID)
				}
			}
		} else if targets, err := s.resolveHosts(ctx, spec.Compose.Hosts, spec.Compose.Group); err == nil {
			for _, t := range targets {
				if allow == nil || allow(t.ID) {
					mp.Hosts = append(mp.Hosts, t.ID)
				}
			}
		}
		if ds, err := s.db.Deployments(ctx, p.ID, 1); err == nil && len(ds) > 0 {
			mp.Status = ds[0].Status
		}
		out = append(out, mp)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
