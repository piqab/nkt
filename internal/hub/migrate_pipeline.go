package hub

import (
	"context"
	"slices"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Перенос стека, который выложил конвейер. Конвейер помнит хост в
// описании (compose.hosts) и в учёте выкладки: без переключения
// следующая выкладка подняла бы стек на старом хосте — две копии с
// разными данными. Переключение: хост в описании (новой версией),
// учёт выкладки и сайт конвейера (прокси и сертификат — на новом хосте,
// со старого конфигурация прокси убирается). Откат переноса — обратно.

// migratePipeline — конвейер в записи переноса (для отката).
type migratePipeline struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// migratePipelineJSON — конвейер стека в плане переноса.
type migratePipelineJSON struct {
	Name string `json:"name"`
	// Domains — сайт конвейера (переедет на новый хост).
	Domains []string `json:"domains,omitempty"`
	// CanSwitch — хост в описании можно заменить (не группа, нового там нет).
	CanSwitch bool `json:"can_switch"`
}

// migratePipelineOf — конвейер, чей стек project выложен на host (по
// учёту выкладок).
func (s *Server) migratePipelineOf(ctx context.Context, host int64, project string) (store.Pipeline, bool) {
	list, err := s.db.ListPipelines(ctx)
	if err != nil {
		return store.Pipeline{}, false
	}
	for _, pl := range list {
		d := s.loadDeployed(ctx, pl.ID)
		if d != nil && d.Project == project && slices.ContainsFunc(d.Hosts, func(h deployedHost) bool { return h.ID == host }) {
			return pl, true
		}
	}
	return store.Pipeline{}, false
}

// migratePipelinePreview — конвейер стека для окна переноса (nil — стек
// выложен не конвейером).
func (s *Server) migratePipelinePreview(ctx context.Context, from, to int64, project string) *migratePipelineJSON {
	pl, ok := s.migratePipelineOf(ctx, from, project)
	if !ok {
		return nil
	}
	out := &migratePipelineJSON{Name: pl.Name}
	_, out.CanSwitch = deploy.ReplaceHost(pl.Content, s.hostName(ctx, from), s.hostName(ctx, to))
	if spec, err := deploy.ParseSpec(pl.Content); err == nil && spec.Compose != nil && spec.Compose.Site != nil {
		out.Domains = spec.Compose.Site.Domains
	}
	return out
}

// migratePipelineSwitch — переключить конвейер стека с хоста from на to;
// nil — стек не конвейера или переключить нельзя (причина — в журнале).
// Ошибка сайта перенос не валит: стек уже работает, сайт — «Повторить» в
// «Сайтах» или следующая выкладка.
func (s *Server) migratePipelineSwitch(ctx context.Context, jc *jobs.Context, user string, from, to int64, project string) *migratePipeline {
	pl, ok := s.migratePipelineOf(ctx, from, project)
	if !ok {
		return nil
	}
	if !s.migratePipelineMove(ctx, jc, user, pl, from, to) {
		return nil
	}
	return &migratePipeline{ID: pl.ID, Name: pl.Name}
}

// migratePipelineRevert — откат переноса: конвейер обратно на старый хост.
func (s *Server) migratePipelineRevert(ctx context.Context, jc *jobs.Context, user string, rec migrateRecord) {
	if rec.Pipeline == nil {
		return
	}
	pl, err := s.db.PipelineByID(ctx, rec.Pipeline.ID)
	if err != nil {
		jc.Log("migrate.pipelineGone", rec.Pipeline.Name)
		return
	}
	s.migratePipelineMove(ctx, jc, user, pl, rec.To, rec.From)
}

// migratePipelineMove — хост в описании, учёт выкладки, сайт.
func (s *Server) migratePipelineMove(ctx context.Context, jc *jobs.Context, user string, pl store.Pipeline, from, to int64) bool {
	fromName, toName := s.hostName(ctx, from), s.hostName(ctx, to)
	content, ok := deploy.ReplaceHost(pl.Content, fromName, toName)
	if !ok {
		jc.Log("migrate.pipelineNoSwitch", pl.Name, fromName)
		return false
	}
	if err := s.db.UpdatePipelineContent(ctx, pl.ID, content, user, msgs.T(jc.Lang(), "migrate.pipelineNote", fromName, toName)); err != nil {
		jc.Log("migrate.pipelineFailed", pl.Name, msgs.Localize(jc.Lang(), err))
		return false
	}
	pl.Content = content
	if d := s.loadDeployed(ctx, pl.ID); d != nil {
		for i := range d.Hosts {
			if d.Hosts[i].ID == from {
				d.Hosts[i] = deployedHost{ID: to, Name: toName}
			}
		}
		s.saveDeployed(ctx, pl.ID, *d)
	}
	jc.Log("migrate.pipelineSwitched", pl.Name, fromName, toName)
	spec, err := deploy.ParseSpec(content)
	if err != nil || spec.Compose == nil || spec.Compose.Site == nil || len(spec.Compose.Hosts) != 1 {
		return true
	}
	t, err := s.siteTarget(ctx, to)
	if err == nil {
		err = s.pipelineSite(ctx, jc, user, pl, spec.Compose, t)
	}
	if err != nil {
		jc.Log("migrate.pipelineSiteFailed", spec.Compose.Site.Domains[0], toName, msgs.Localize(jc.Lang(), err))
	}
	return true
}
