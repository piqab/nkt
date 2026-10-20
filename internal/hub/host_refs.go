package hub

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Что ссылается на хост: сайты выкладок, конвейеры (hosts: в описании),
// цели Traffic Fly, учёт переносов. При удалении хоста окно показывает
// это галочками — иначе остаются сайт с несуществующим хостом, выкладка,
// падающая «хост не найден», и неживые цели маршрутов.

type hostRefs struct {
	Sites      []hostRefItem `json:"sites"`
	Pipelines  []hostRefItem `json:"pipelines"`
	Fly        []hostRefItem `json:"fly"`
	Migrations []hostRefItem `json:"migrations"`
}

type hostRefItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Last — конвейер: хост в нём единственный (убрать — некуда выкладывать).
	Last bool `json:"last,omitempty"`
}

// HostRefOptions — что убрать вместе с хостом.
type HostRefOptions struct {
	Sites      bool `json:"sites"`
	Pipelines  bool `json:"pipelines"`
	Fly        bool `json:"fly"`
	Migrations bool `json:"migrations"`
}

func (s *Server) hostRefs(ctx context.Context, host store.Host) hostRefs {
	out := hostRefs{Sites: []hostRefItem{}, Pipelines: []hostRefItem{}, Fly: []hostRefItem{}, Migrations: []hostRefItem{}}
	if sites, err := s.db.ListSites(ctx); err == nil {
		for _, st := range sites {
			if st.HostID == host.ID && len(st.Domains) > 0 {
				out.Sites = append(out.Sites, hostRefItem{ID: itoa64(st.ID), Name: st.Domains[0]})
			}
		}
	}
	if pipes, err := s.db.ListPipelines(ctx); err == nil {
		for _, pl := range pipes {
			spec, err := deploy.ParseSpec(pl.Content)
			if err != nil || spec.Compose == nil {
				continue
			}
			if slices.ContainsFunc(spec.Compose.Hosts, func(n string) bool { return strings.EqualFold(strings.TrimSpace(n), host.Name) }) {
				out.Pipelines = append(out.Pipelines, hostRefItem{ID: itoa64(pl.ID), Name: pl.Name, Last: len(spec.Compose.Hosts) == 1})
			}
		}
	}
	for _, r := range s.flyDoc(ctx).Routes {
		for _, t := range r.Targets {
			if t.HostID == host.ID {
				name := r.Label
				if name == "" {
					name = r.ID
				}
				out.Fly = append(out.Fly, hostRefItem{ID: r.ID + "/" + t.ID, Name: name})
			}
		}
	}
	for _, m := range s.loadMigrations(ctx) {
		if (m.From == host.ID || m.To == host.ID) && m.State == "moved" {
			out.Migrations = append(out.Migrations, hostRefItem{ID: m.ID, Name: m.Project + " · " + m.FromName + " → " + m.ToName})
		}
	}
	return out
}

// handleHostRefs — GET /hub/hosts/{id}/refs.
func (s *Server) handleHostRefs(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	host, err := s.db.HostByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.hostRefs(r.Context(), host))
}

// cleanHostRefs — убрать отмеченное перед удалением хоста; что не вышло —
// в ответ (хост удаляется всё равно).
func (s *Server) cleanHostRefs(ctx context.Context, host store.Host, o HostRefOptions) []string {
	var problems []string
	refs := s.hostRefs(ctx, host)
	user := auth.Username(ctx)
	if o.Sites {
		for _, it := range refs.Sites {
			if err := s.db.DeleteSite(ctx, parseID(it.ID)); err != nil {
				problems = append(problems, it.Name+": "+err.Error())
			}
		}
	}
	if o.Pipelines {
		for _, it := range refs.Pipelines {
			pl, err := s.db.PipelineByID(ctx, parseID(it.ID))
			if err != nil {
				continue
			}
			content, ok := deploy.WithoutHosts(pl.Content, []string{host.Name})
			if !ok {
				problems = append(problems, msgs.Tc(ctx, "hub.hostRefPipelineLast", pl.Name))
				continue
			}
			if err := s.db.UpdatePipelineContent(ctx, pl.ID, content, user, msgs.Tc(ctx, "hub.hostRefPipelineNote", host.Name)); err != nil {
				problems = append(problems, pl.Name+": "+err.Error())
			}
			if d := s.loadDeployed(ctx, pl.ID); d != nil {
				d.Hosts = slices.DeleteFunc(d.Hosts, func(h deployedHost) bool { return h.ID == host.ID })
				s.saveDeployed(ctx, pl.ID, *d)
			}
		}
	}
	if o.Fly && len(refs.Fly) > 0 && s.jobs != nil {
		var ops []FlyOp
		for _, it := range refs.Fly {
			route, target, _ := strings.Cut(it.ID, "/")
			ops = append(ops, FlyOp{Op: "remove", Route: route, Target: target})
		}
		if _, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindFlyOps, TitleKey: "hub.hostRefFlyJob", TitleArgs: []any{host.Name},
			Author: user, Params: FlyOpsParams{Ops: ops, Note: "host removed"}}); err != nil {
			problems = append(problems, "Traffic Fly: "+err.Error())
		}
	}
	if o.Migrations && len(refs.Migrations) > 0 {
		s.updateMigrations(ctx, func(l []migrateRecord) []migrateRecord {
			for i := range l {
				if (l[i].From == host.ID || l[i].To == host.ID) && l[i].State == "moved" {
					l[i].State = "removed"
				}
			}
			return l
		})
	}
	return problems
}

func parseID(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
