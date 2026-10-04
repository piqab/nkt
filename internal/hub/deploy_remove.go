package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Удаление с хостов — заданиями хаба. Конвейер compose: его сайт, затем
// стек на каждом хосте (compose down, каталог стека), затем запись на
// хабе. Сайт: конфигурация прокси, публикация сервиса, по галочке —
// сертификат. Не удалось — запись остаётся с пометкой «удаление не
// завершено» и кнопкой «Повторить»: иначе на хосте осталось бы то, о чём
// хаб уже не знает. Уже убранное повтор считает сделанным.

// KindPipelineRemove / KindSiteRemove — задания удаления.
const (
	KindPipelineRemove = "pipeline.remove"
	KindSiteRemove     = "site.remove"
)

// RemoveOptions — что удалять, кроме самого стека.
type RemoveOptions struct {
	// Volumes — тома стека (down -v) и его каталог целиком; без них
	// каталог переносится в /srv/compose/.nkt-removed.
	Volumes bool `json:"volumes,omitempty"`
	Images  bool `json:"images,omitempty"`
	// Cert — сертификат сайта (certbot delete).
	Cert bool `json:"cert,omitempty"`
}

// pipelineRemoval — состояние удаления конвейера (pipelines.removal).
type pipelineRemoval struct {
	RemoveOptions
	JobID int64  `json:"job_id,omitempty"`
	Error string `json:"error,omitempty"`
}

// PipelineRemoveParams — вход задания удаления конвейера.
type PipelineRemoveParams struct {
	PipelineID int64 `json:"pipeline_id"`
	RemoveOptions
}

// SiteRemoveParams — вход задания удаления сайта.
type SiteRemoveParams struct {
	SiteID int64 `json:"site_id"`
	Cert   bool  `json:"cert,omitempty"`
}

// handlePipelineRemove — POST /hub/pipelines/{id}/remove {volumes, images,
// cert}: удалить конвейер compose с хостов и с хаба (задание).
func (s *Server) handlePipelineRemove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var opts RemoveOptions
	if err := decodeJSON(r, &opts); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	spec, err := deploy.ParseSpec(p.Content)
	if err != nil || spec.Action != deploy.ActionCompose {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("deploy.removeOnlyCompose"))
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	// Пока удаляется — ни вебхук, ни опрос не выкладывают его заново.
	_ = s.db.SetPipelineEnabled(ctx, p.ID, false)
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindPipelineRemove, TitleKey: "deploy.removeTitle", TitleArgs: []any{p.Name},
		Queue: fmt.Sprintf("deploy:%d", p.ID), Author: user, Steps: 3,
		Params: PipelineRemoveParams{PipelineID: p.ID, RemoveOptions: opts},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	raw, _ := json.Marshal(pipelineRemoval{RemoveOptions: opts, JobID: id})
	_ = s.db.SetPipelineRemoval(ctx, p.ID, string(raw))
	s.db.Audit(ctx, user, "pipeline.remove", p.Name, "ok", map[string]any{"job_id": id, "volumes": opts.Volumes, "images": opts.Images, "cert": opts.Cert})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// removalHosts — хосты стека для удаления: хост, которого на хабе уже
// нет, пропускается (убирать не на чем); не работающий — ошибка (стек
// там остался бы).
func (s *Server) removalHosts(ctx context.Context, jc *jobs.Context, c *deploy.ComposeSpec) ([]targetHost, error) {
	hosts, err := s.db.ListHosts(ctx)
	if err != nil {
		return nil, err
	}
	var out []targetHost
	seen := map[int64]bool{}
	add := func(t targetHost) {
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	check := func(h store.Host) error {
		if h.Status != store.HostStatusOnline {
			return msgs.Errorf("hub.hostReadyYetStatus", h.Name, h.Status)
		}
		add(targetHost{ID: h.ID, Name: h.Name, Addr: h.Addr})
		return nil
	}
	for _, n := range c.Hosts {
		if n == "localhost" && s.local != nil {
			add(targetHost{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"})
			continue
		}
		found := false
		for _, h := range hosts {
			if sameHostName(h.Name, n) {
				found = true
				if err := check(h); err != nil {
					return nil, err
				}
			}
		}
		if !found {
			jc.Log("deploy.removeHostGone", n)
		}
	}
	if c.Group != "" {
		if s.local != nil && s.hub.LocalHostGroup(ctx) == c.Group {
			add(targetHost{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"})
		}
		for _, h := range hosts {
			if h.Group == c.Group {
				if err := check(h); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// PipelineRemoveRunner — задание удаления конвейера.
type PipelineRemoveRunner struct{ s *Server }

// NewPipelineRemoveRunner — исполнитель.
func NewPipelineRemoveRunner(s *Server) *PipelineRemoveRunner { return &PipelineRemoveRunner{s: s} }

// Run: сайт конвейера → стек на каждом хосте → запись на хабе.
func (r *PipelineRemoveRunner) Run(ctx context.Context, jc *jobs.Context) (err error) {
	s := r.s
	var p PipelineRemoveParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	pl, err := s.db.PipelineByID(ctx, p.PipelineID)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			raw, _ := json.Marshal(pipelineRemoval{RemoveOptions: p.RemoveOptions, JobID: jc.Job.ID, Error: msgs.Localize(jc.Lang(), err)})
			_ = s.db.SetPipelineRemoval(context.WithoutCancel(ctx), pl.ID, string(raw))
		}
	}()
	spec, err := deploy.ParseSpec(pl.Content)
	if err != nil {
		return err
	}
	c := spec.Compose
	user := s.actingUser(ctx, jc.Job.Author, pl.Author, s.firstAdmin(ctx))

	// 1. Сайт конвейера (до стека: прокси не должен смотреть в пустоту).
	jc.StepKey(1, 3, "deploy.removeStepSite")
	sites, err := s.db.ListSites(ctx)
	if err != nil {
		return err
	}
	for _, st := range sites {
		if st.PipelineID != pl.ID {
			continue
		}
		// Стек всё равно уходит — публикацию сервиса не снимаем.
		if err := s.removeSiteFromHost(ctx, jc, user, st, false, p.Cert); err != nil {
			return err
		}
	}

	// 2. Стек на каждом хосте.
	targets, err := s.removalHosts(ctx, jc, c)
	if err != nil {
		return err
	}
	jc.StepKey(2, 3, "deploy.removeStepHosts", len(targets))
	for _, t := range targets {
		jc.Log("deploy.removeHost", t.Name, c.Project)
		var started struct {
			JobID int64 `json:"job_id"`
		}
		body := map[string]any{"project": c.Project, "volumes": p.Volumes, "images": p.Images}
		if _, err := s.hostCall(ctx, user, t.ID, "POST", "/api/compose/stacks/remove", body, &started); err != nil {
			return msgs.Errorf("deploy.removeHostFailed", t.Name, msgs.Localize(jc.Lang(), err))
		}
		if err := s.waitHostJobVia(ctx, jc, user, t.ID, started.JobID); err != nil {
			return msgs.Errorf("deploy.removeHostFailed", t.Name, msgs.Localize(jc.Lang(), err))
		}
	}
	// Где стек выложен на деле (описание могли поменять без выкладки) и
	// старые стеки, оставшиеся после переезда: доступный хост — убрать,
	// недоступный — в журнал (запись конвейера всё равно удаляется).
	extra := s.loadLeftovers(ctx, pl.ID)
	if d := s.loadDeployed(ctx, pl.ID); d != nil {
		for _, h := range d.Hosts {
			extra = append(extra, leftoverStack{HostID: h.ID, Host: h.Name, Project: d.Project})
		}
	}
	for _, l := range extra {
		if l.Project == c.Project && slices.ContainsFunc(targets, func(t targetHost) bool { return t.ID == l.HostID }) {
			continue
		}
		if ok, why := s.hostUsable(ctx, l.HostID); !ok {
			jc.Log("deploy.oldStackLeft", l.Project, l.Host, why)
			continue
		}
		jc.Log("deploy.removeHost", l.Host, l.Project)
		var started struct {
			JobID int64 `json:"job_id"`
		}
		body := map[string]any{"project": l.Project, "volumes": p.Volumes, "images": p.Images}
		if _, err := s.hostCall(ctx, user, l.HostID, "POST", "/api/compose/stacks/remove", body, &started); err == nil {
			err = s.waitHostJobVia(ctx, jc, user, l.HostID, started.JobID)
			if err != nil {
				jc.Log("deploy.oldStackLeft", l.Project, l.Host, msgs.Localize(jc.Lang(), err))
			}
		} else {
			jc.Log("deploy.oldStackLeft", l.Project, l.Host, msgs.Localize(jc.Lang(), err))
		}
	}

	// 3. Запись на хабе и её рабочий каталог (checkout).
	jc.StepKey(3, 3, "deploy.removeStepHub")
	if err := s.db.DeletePipeline(ctx, pl.ID); err != nil {
		return err
	}
	_ = os.RemoveAll(s.pipelineDir(pl.ID))
	_ = s.db.KVSet(ctx, deployedKey(pl.ID), "")
	s.saveLeftovers(ctx, pl.ID, nil)
	s.db.Audit(ctx, jc.Job.Author, "pipeline.delete", pl.Name, "ok", map[string]any{"job_id": jc.Job.ID})
	jc.Log("deploy.removed", pl.Name)
	return nil
}

// removeSiteFromHost — конфигурация прокси (и публикация, сертификат) на
// хосте сайта и запись сайта на хабе. Уже убранное — не ошибка.
func (s *Server) removeSiteFromHost(ctx context.Context, jc *jobs.Context, user string, st store.Site, unpublish, cert bool) error {
	name := st.Domains[0]
	if _, err := s.siteTarget(ctx, st.HostID); err != nil {
		// Хоста на хабе больше нет — убирать не на чем.
		jc.Log("deploy.removeSiteHostGone", name)
		return s.db.DeleteSite(ctx, st.ID)
	}
	jc.Log("deploy.removeSite", name)
	var res struct {
		Unpublished    bool   `json:"unpublished"`
		UnpublishError string `json:"unpublish_error"`
		CertDeleted    string `json:"cert_deleted"`
		CertShared     bool   `json:"cert_shared"`
		CertError      string `json:"cert_error"`
	}
	body := map[string]any{"domain": name, "unpublish": unpublish, "delete_cert": cert}
	code, err := s.hostCall(ctx, user, st.HostID, "POST", "/api/sites/remove", body, &res)
	switch {
	case code == http.StatusNotFound:
		jc.Log("deploy.removeSiteAlready", name)
	case err != nil:
		return err
	default:
		if res.Unpublished {
			jc.Log("deploy.removeSiteUnpublished", st.Service)
		}
		if res.UnpublishError != "" {
			return msgs.Errorf("deploy.removeSiteStep", name, res.UnpublishError)
		}
		if res.CertDeleted != "" {
			jc.Log("deploy.removeSiteCert", res.CertDeleted)
		}
		if res.CertShared {
			jc.Log("deploy.removeSiteCertShared")
		}
		if res.CertError != "" {
			return msgs.Errorf("deploy.removeSiteStep", name, res.CertError)
		}
	}
	return s.db.DeleteSite(ctx, st.ID)
}

// handleSiteRemove — POST /hub/sites/{id}/remove {cert}: удалить сайт с
// хоста и с хаба (задание).
func (s *Server) handleSiteRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	st, err := s.db.SiteByID(ctx, id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	var req struct {
		Cert bool `json:"cert"`
	}
	_ = decodeJSON(r, &req)
	user := auth.Username(ctx)
	jobID, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindSiteRemove, TitleKey: "hub.siteRemoveTitle", TitleArgs: []any{st.Domains[0]}, Queue: "site:" + st.Domains[0],
		Author: user, Steps: 1, Params: SiteRemoveParams{SiteID: id, Cert: req.Cert},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	_ = s.db.SetSiteState(ctx, id, store.SiteRemoving, "", jobID)
	s.db.Audit(ctx, user, "site.remove", st.Domains[0], "ok", map[string]any{"job_id": jobID, "cert": req.Cert})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

// SiteRemoveRunner — задание удаления сайта.
type SiteRemoveRunner struct{ s *Server }

// NewSiteRemoveRunner — исполнитель.
func NewSiteRemoveRunner(s *Server) *SiteRemoveRunner { return &SiteRemoveRunner{s: s} }

// Run — прокси, публикация сервиса, сертификат по галочке, запись.
func (r *SiteRemoveRunner) Run(ctx context.Context, jc *jobs.Context) (err error) {
	s := r.s
	var p SiteRemoveParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	st, err := s.db.SiteByID(ctx, p.SiteID)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = s.db.SetSiteState(context.WithoutCancel(ctx), st.ID, store.SiteRemoveFailed, msgs.Localize(jc.Lang(), err), jc.Job.ID)
		}
	}()
	jc.StepKey(1, 1, "hub.siteRemoveStep", st.Domains[0])
	if st.PipelineID > 0 {
		jc.Log("deploy.removeSiteFromPipeline")
	}
	if err := s.removeSiteFromHost(ctx, jc, jc.Job.Author, st, true, p.Cert); err != nil {
		return err
	}
	jc.Log("hub.siteRemoved", st.Domains[0])
	return nil
}
