package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

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
	// Items — с каких хостов убрать стек (из плана, только «уберётся»);
	// nil — все такие (как раньше, у заданий старых версий).
	Items []removalKey `json:"items,omitempty"`
	// Chosen — хосты выбраны в окне по плану (Items может быть и пустым:
	// «ничего не убирать»); без выбора — всё, и недоступный хост — ошибка.
	Chosen bool `json:"chosen,omitempty"`
	// KeepPipeline — убрать стек только с этих хостов: конвейер остаётся,
	// хосты уходят из его описания.
	KeepPipeline bool `json:"keep_pipeline,omitempty"`
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
	var req struct {
		RemoveOptions
		Items        []removalKey `json:"items"`
		KeepPipeline bool         `json:"keep_pipeline"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	opts := req.RemoveOptions
	spec, err := deploy.ParseSpec(p.Content)
	if err != nil || spec.Action != deploy.ActionCompose {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("deploy.removeOnlyCompose"))
		return
	}
	ctx := r.Context()
	// Выбранное — только из плана и только «уберётся»: общий чужой стек
	// снести нельзя и прямым запросом.
	plan := s.removalPlan(ctx, p)
	for _, k := range req.Items {
		if !slices.ContainsFunc(plan, func(it removalItem) bool {
			return it.HostID == k.HostID && it.Project == k.Project && it.State == "remove"
		}) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("deploy.removeItemNotAllowed", k.Project))
			return
		}
	}
	if req.KeepPipeline {
		if len(req.Items) == 0 {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("deploy.unhostNothing"))
			return
		}
		var names []string
		for _, k := range req.Items {
			for _, it := range plan {
				if it.HostID == k.HostID && it.Project == k.Project {
					names = append(names, it.Host)
				}
			}
		}
		if _, ok := deploy.WithoutHosts(p.Content, names); !ok {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("deploy.unhostImpossible"))
			return
		}
	}
	user := auth.Username(ctx)
	title := "deploy.removeTitle"
	if req.KeepPipeline {
		title = "deploy.unhostTitle"
	} else {
		// Пока удаляется — ни вебхук, ни опрос не выкладывают его заново.
		_ = s.db.SetPipelineEnabled(ctx, p.ID, false)
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindPipelineRemove, TitleKey: title, TitleArgs: []any{p.Name},
		Queue: fmt.Sprintf("deploy:%d", p.ID), Author: user, Steps: 3,
		Params: PipelineRemoveParams{PipelineID: p.ID, RemoveOptions: opts, Items: req.Items, Chosen: req.Items != nil, KeepPipeline: req.KeepPipeline},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if !req.KeepPipeline {
		raw, _ := json.Marshal(pipelineRemoval{RemoveOptions: opts, JobID: id})
		_ = s.db.SetPipelineRemoval(ctx, p.ID, string(raw))
	}
	s.db.Audit(ctx, user, "pipeline.remove", p.Name, "ok", map[string]any{"job_id": id, "volumes": opts.Volumes, "images": opts.Images, "cert": opts.Cert})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
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
		if err != nil && !p.KeepPipeline {
			raw, _ := json.Marshal(pipelineRemoval{RemoveOptions: p.RemoveOptions, JobID: jc.Job.ID, Error: msgs.Localize(jc.Lang(), err)})
			_ = s.db.SetPipelineRemoval(context.WithoutCancel(ctx), pl.ID, string(raw))
		}
	}()
	spec, err := deploy.ParseSpec(pl.Content)
	if err != nil {
		return err
	}
	// Хосты описания, которых на хабе уже нет, — убирать не на чем.
	if spec.Compose != nil && s.loadDeployed(ctx, pl.ID) == nil {
		hosts, _ := s.db.ListHosts(ctx)
		for _, n := range spec.Compose.Hosts {
			if n == "localhost" && s.local != nil {
				continue
			}
			if !slices.ContainsFunc(hosts, func(h store.Host) bool { return sameHostName(h.Name, n) }) {
				jc.Log("deploy.removeHostGone", n)
			}
		}
	}
	user := s.actingUser(ctx, jc.Job.Author, pl.Author, s.firstAdmin(ctx))

	// План: где стек этого конвейера на деле и что с ним делать. Убирается
	// только выбранное (nil — всё, что можно убрать); общий с другим
	// конвейером стек и недоступные хосты — остаются, с объяснением.
	plan := s.removalPlan(ctx, pl)
	chosen := func(it removalItem) bool {
		if it.State != "remove" {
			return false
		}
		if !p.Chosen {
			return true
		}
		return slices.ContainsFunc(p.Items, func(k removalKey) bool { return k.HostID == it.HostID && k.Project == it.Project })
	}
	// Без выбора по плану (старое задание, API) — как раньше: недоступный
	// хост — удаление не завершено, иначе стек там остался бы сиротой.
	if !p.Chosen && !p.KeepPipeline {
		for _, it := range plan {
			if it.State == "unreachable" {
				return msgs.Errorf("deploy.removeHostFailed", it.Host, it.Reason)
			}
		}
	}
	var targets []removalItem
	for _, it := range plan {
		switch {
		case chosen(it):
			targets = append(targets, it)
		case it.State == "shared":
			jc.Log("deploy.removeShared", it.Project, it.Host, it.SharedWith)
		case it.State == "unreachable" || it.State == "gone":
			jc.Log("deploy.oldStackLeft", it.Project, it.Host, it.Reason)
		case !p.KeepPipeline:
			jc.Log("deploy.removeNotChosen", it.Project, it.Host)
		}
	}

	// 1. Сайт конвейера (до стека: прокси не должен смотреть в пустоту) —
	// при удалении конвейера всегда, при уборке с хостов — если сайт на
	// одном из них.
	jc.StepKey(1, 3, "deploy.removeStepSite")
	sites, err := s.db.ListSites(ctx)
	if err != nil {
		return err
	}
	for _, st := range sites {
		if st.PipelineID != pl.ID {
			continue
		}
		if p.KeepPipeline && !slices.ContainsFunc(targets, func(it removalItem) bool { return it.HostID == st.HostID }) {
			continue
		}
		// Стек всё равно уходит — публикацию сервиса не снимаем.
		if err := s.removeSiteFromHost(ctx, jc, user, st, false, p.Cert); err != nil {
			return err
		}
	}

	// 2. Стек на выбранных хостах.
	jc.StepKey(2, 3, "deploy.removeStepHosts", len(targets))
	for _, it := range targets {
		jc.Log("deploy.removeHost", it.Host, it.Project)
		var started struct {
			JobID int64 `json:"job_id"`
		}
		body := map[string]any{"project": it.Project, "volumes": p.Volumes, "images": p.Images}
		if _, err := s.hostCall(ctx, user, it.HostID, "POST", "/api/compose/stacks/remove", body, &started); err != nil {
			return msgs.Errorf("deploy.removeHostFailed", it.Host, msgs.Localize(jc.Lang(), err))
		}
		if err := s.waitHostJobVia(ctx, jc, user, it.HostID, started.JobID); err != nil {
			return msgs.Errorf("deploy.removeHostFailed", it.Host, msgs.Localize(jc.Lang(), err))
		}
	}

	// Убранное больше не числится ни выложенным, ни оставшимся.
	removed := func(id int64, project string) bool {
		return slices.ContainsFunc(targets, func(it removalItem) bool { return it.HostID == id && it.Project == project })
	}
	if d := s.loadDeployed(ctx, pl.ID); d != nil {
		d.Hosts = slices.DeleteFunc(d.Hosts, func(h deployedHost) bool { return removed(h.ID, d.Project) })
		s.saveDeployed(ctx, pl.ID, *d)
	}
	s.saveLeftovers(ctx, pl.ID, slices.DeleteFunc(s.loadLeftovers(ctx, pl.ID), func(l leftoverStack) bool { return removed(l.HostID, l.Project) }))

	// 3. Конвейер: остаётся без этих хостов в описании — или удаляется.
	jc.StepKey(3, 3, "deploy.removeStepHub")
	if p.KeepPipeline {
		var names []string
		for _, it := range targets {
			names = append(names, it.Host)
		}
		content, ok := deploy.WithoutHosts(pl.Content, names)
		if !ok {
			return msgs.Errorf("deploy.unhostImpossible")
		}
		if err := s.db.UpdatePipelineContent(ctx, pl.ID, content, jc.Job.Author, msgs.T(jc.Lang(), "deploy.unhostNote", strings.Join(names, ", "))); err != nil {
			return err
		}
		jc.Log("deploy.unhostDone", strings.Join(names, ", "))
		return nil
	}
	for _, it := range plan {
		if !chosen(it) && it.State == "remove" {
			jc.Log("deploy.removeOrphan", it.Project, it.Host)
		}
	}
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
