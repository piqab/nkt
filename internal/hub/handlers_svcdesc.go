package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/svcdesc"
)

// Описания сервисов на хабе («Профили» → «Сервисы»): библиотека YAML с
// историей; на хосты она уходит уровнем «профиль» (каталог данных nkt,
// services.d/profile) заданием «Доставить на хосты». Встроенные
// описания — только для чтения: их можно перекрыть своим с тем же именем.

const maxSvcDescBytes = 128 << 10

// KindSvcDescDeliver — задание хаба: описания на хосты.
const KindSvcDescDeliver = "svcdesc.deliver"

// svcDescCheck — описание разобрано: имя и что в нём есть.
type svcDescCheck struct {
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	Engine    string `json:"engine,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
	Sources   int    `json:"sources"`
	Endpoints int    `json:"endpoints"`
	Upstreams int    `json:"upstreams"`
	Rules     int    `json:"rules"`
	// RootCommands — команды-источники с as_root (идут от root).
	RootCommands []string `json:"root_commands,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func checkSvcDesc(lang msgs.Lang, content string) (svcDescCheck, *svcdesc.Descriptor) {
	if len(content) > maxSvcDescBytes {
		return svcDescCheck{Error: msgs.T(lang, "svcdesc.tooBig", maxSvcDescBytes>>10)}, nil
	}
	d, err := svcdesc.Parse([]byte(content))
	if err != nil {
		return svcDescCheck{Error: msgs.Localize(lang, err)}, nil
	}
	c := svcDescCheck{Name: d.Name, Title: d.Title, Engine: d.Engine, Disabled: d.Disabled, Sources: len(d.Sources),
		Endpoints: len(d.Extract.Endpoints), Upstreams: len(d.Extract.Upstreams), Rules: len(d.Rules)}
	for _, s := range d.Sources {
		if s.AsRoot && len(s.Command) > 0 {
			c.RootCommands = append(c.RootCommands, strings.Join(s.Command, " "))
		}
	}
	return c, d
}

// svcDescBuiltin — встроенное описание (текст из nkt).
type svcDescBuiltin struct {
	svcDescCheck
	File    string `json:"file"`
	Content string `json:"content"`
}

// handleSvcDescList — GET /hub/svcdescs: свои (без текста) и встроенные.
func (s *Server) handleSvcDescList(w http.ResponseWriter, r *http.Request) {
	lang := msgs.FromContext(r.Context())
	own, err := s.db.AllSvcDescs(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	type ownRow struct {
		store.SvcDesc
		Check svcDescCheck `json:"check"`
	}
	rows := make([]ownRow, 0, len(own))
	for _, d := range own {
		c, _ := checkSvcDesc(lang, d.Content)
		d.Content = ""
		rows = append(rows, ownRow{SvcDesc: d, Check: c})
	}
	builtin := []svcDescBuiltin{}
	for _, b := range svcdesc.Builtin() {
		c, _ := checkSvcDesc(lang, b.Content)
		builtin = append(builtin, svcDescBuiltin{svcDescCheck: c, File: b.File, Content: b.Content})
	}
	writeJSON(w, http.StatusOK, map[string]any{"own": rows, "builtin": builtin})
}

func (s *Server) svcDescByIDParam(r *http.Request) (store.SvcDesc, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return store.SvcDesc{}, store.ErrNotFound
	}
	return s.db.SvcDescByID(r.Context(), id)
}

func (s *Server) handleSvcDescGet(w http.ResponseWriter, r *http.Request) {
	d, err := s.svcDescByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleSvcDescExport(w http.ResponseWriter, r *http.Request) {
	d, err := s.svcDescByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	// Имя проверено svcdesc (латиница, цифры, . _ -).
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.Name+`.yaml"`)
	_, _ = w.Write([]byte(d.Content))
}

// handleSvcDescCheck — POST /hub/svcdescs/check {content}: разбор и
// проверка без сохранения.
func (s *Server) handleSvcDescCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	c, _ := checkSvcDesc(msgs.FromContext(r.Context()), req.Content)
	writeJSON(w, http.StatusOK, c)
}

type svcDescRequest struct {
	Content string `json:"content"`
	Note    string `json:"note"`
}

// svcDescSave — общая часть создания и правки: имя — из самого описания.
func (s *Server) svcDescSave(w http.ResponseWriter, r *http.Request, existing *store.SvcDesc) {
	var req svcDescRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	c, d := checkSvcDesc(msgs.FromContext(ctx), req.Content)
	if d == nil {
		writeError(w, http.StatusBadRequest, c.Error)
		return
	}
	if other, err := s.db.SvcDescByName(ctx, d.Name); err == nil && (existing == nil || other.ID != existing.ID) {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("svcdesc.nameTaken", d.Name))
		return
	}
	user := auth.Username(ctx)
	note := strings.TrimSpace(req.Note)
	if existing == nil {
		id, err := s.db.CreateSvcDesc(ctx, store.SvcDesc{Name: d.Name, Content: req.Content, Note: note, Author: user})
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		s.db.Audit(ctx, user, "svcdesc.create", d.Name, "ok", map[string]any{"root_commands": c.RootCommands})
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": d.Name})
		return
	}
	if err := s.db.UpdateSvcDesc(ctx, store.SvcDesc{ID: existing.ID, Name: d.Name, Content: req.Content, Note: note, Author: user}); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, user, "svcdesc.update", d.Name, "ok", map[string]any{"was": existing.Name, "root_commands": c.RootCommands})
	writeJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "name": d.Name})
}

func (s *Server) handleSvcDescCreate(w http.ResponseWriter, r *http.Request) {
	s.svcDescSave(w, r, nil)
}

func (s *Server) handleSvcDescUpdate(w http.ResponseWriter, r *http.Request) {
	d, err := s.svcDescByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	s.svcDescSave(w, r, &d)
}

func (s *Server) handleSvcDescDelete(w http.ResponseWriter, r *http.Request) {
	d, err := s.svcDescByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.db.DeleteSvcDesc(r.Context(), d.ID); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "svcdesc.delete", d.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSvcDescVersions(w http.ResponseWriter, r *http.Request) {
	d, err := s.svcDescByIDParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	list, err := s.db.SvcDescVersions(r.Context(), d.ID, 50)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list})
}

func (s *Server) handleSvcDescVersion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "version"), 10, 64)
	if err != nil {
		fail(w, r, store.ErrNotFound)
		return
	}
	v, err := s.db.SvcDescVersion(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleSvcDescEval — POST /hub/svcdescs/eval {tree, each, when, expr}:
// конструктор путей — путь, условие и поле над образцом с хоста.
func (s *Server) handleSvcDescEval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tree json.RawMessage `json:"tree"`
		Each string          `json:"each"`
		When string          `json:"when"`
		Expr string          `json:"expr"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, svcdesc.Eval(req.Tree, req.Each, req.When, req.Expr, msgs.FromContext(r.Context())))
}

// SvcDescDeliverParams — задание доставки: только номера хостов, тексты
// описаний берутся из базы при запуске.
type SvcDescDeliverParams struct {
	HostIDs []int64 `json:"host_ids"`
}

// handleSvcDescDeliver — POST /hub/svcdescs/deliver {host_ids}: задание.
func (s *Server) handleSvcDescDeliver(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var p SvcDescDeliverParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(p.HostIDs) == 0 || len(p.HostIDs) > 500 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("svcdesc.noHosts"))
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindSvcDescDeliver, TitleKey: "svcdesc.deliverJob", TitleArgs: []any{len(p.HostIDs)},
		Author: user, Params: p, Steps: len(p.HostIDs)})
	if err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, user, "svcdesc.deliver", "", "ok", map[string]any{"hosts": p.HostIDs, "job_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// SvcDescDeliverRunner — исполнитель доставки.
type SvcDescDeliverRunner struct{ s *Server }

// NewSvcDescDeliverRunner — исполнитель.
func NewSvcDescDeliverRunner(s *Server) *SvcDescDeliverRunner { return &SvcDescDeliverRunner{s: s} }

// Run — набор описаний хаба на каждый хост целиком (по три сразу);
// неудача на одном хосте не останавливает остальные.
func (r *SvcDescDeliverRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p SvcDescDeliverParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	all, err := r.s.db.AllSvcDescs(ctx)
	if err != nil {
		return err
	}
	items := make([]svcdesc.Named, 0, len(all))
	for _, d := range all {
		items = append(items, svcdesc.Named{Name: d.Name, Content: d.Content})
	}
	jc.Log("svcdesc.deliverStart", len(items), len(p.HostIDs))
	names := map[int64]string{}
	if hosts, err := r.s.db.ListHosts(ctx); err == nil {
		for _, h := range hosts {
			names[h.ID] = h.Name
		}
	}
	names[localHostID] = "localhost"
	var mu sync.Mutex
	done, failed := 0, 0
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for _, id := range p.HostIDs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			code, err := r.s.hostCall(ctx, jc.Job.Author, id, http.MethodPut, "/api/svcdesc/profile",
				map[string]any{"descriptions": items}, nil)
			mu.Lock()
			defer mu.Unlock()
			done++
			name := names[id]
			if name == "" {
				name = strconv.FormatInt(id, 10)
			}
			jc.StepKey(done, len(p.HostIDs), "svcdesc.deliverStep", name)
			switch {
			case err == nil:
				jc.Log("svcdesc.deliverHostDone", name, len(items))
			case code == http.StatusNotFound || code == http.StatusMethodNotAllowed:
				failed++
				jc.Log("svcdesc.deliverHostOld", name)
			default:
				failed++
				jc.Log("svcdesc.deliverHostFailed", name, msgs.Localize(jc.Lang(), err))
			}
		}(id)
	}
	wg.Wait()
	if failed > 0 {
		return msgs.Errorf("svcdesc.deliverFailed", failed, len(p.HostIDs))
	}
	return nil
}
