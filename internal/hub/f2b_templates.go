package hub

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Шаблон fail2ban на несколько хостов: сначала обязательная проверка
// (пробный прогон на каждом хосте, дифф файлов), затем задание хаба
// применяет ровно проверенное. Стандартный шаблон берётся у каждого
// хоста свой (sshd на journald и на файле журнала — разный текст), свой
// шаблон — с хаба.

// KindF2BTemplate — задание хаба: шаблон fail2ban на хосты.
const KindF2BTemplate = "fail2ban.template"

// f2bTplCheckTTL — сколько проверка годна для применения.
const f2bTplCheckTTL = 30 * time.Minute

// f2bTplParallel — сколько хостов проверяется и применяется сразу.
const f2bTplParallel = 3

// f2bTplReq — запрос применения шаблона к API хоста (как у окна хоста).
type f2bTplReq struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Jail        string `json:"jail"`
	Filter      string `json:"filter,omitempty"`
	Note        string `json:"note,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

type f2bFile struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
	Exists bool   `json:"exists"`
}

// f2bTplHost — итог проверки хоста: changes — будут изменения, same —
// всё уже так, skip — пропущен (причина), error — проверка не прошла.
type f2bTplHost struct {
	ID     int64     `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Reason string    `json:"reason,omitempty"`
	Files  []f2bFile `json:"files,omitempty"`
}

// f2bTplPlan — проверенное: что применять на каком хосте.
type f2bTplPlan struct {
	author  string
	name    string
	expires time.Time
	hosts   []F2BTplHostReq
}

// F2BTplHostReq — хост задания и его запрос.
type F2BTplHostReq struct {
	ID   int64     `json:"id"`
	Name string    `json:"name"`
	Req  f2bTplReq `json:"req"`
}

// F2BTplParams — параметры задания.
type F2BTplParams struct {
	Template string          `json:"template"`
	Hosts    []F2BTplHostReq `json:"hosts"`
}

var (
	f2bPlansMu sync.Mutex
	f2bPlans   = map[string]*f2bTplPlan{}
)

type f2bTemplates struct {
	Builtin []fail2ban.Template `json:"builtin"`
	Custom  []fail2ban.Template `json:"custom"`
}

// handleF2BTemplateCheck — POST /hub/fail2ban/templates/check
// {name, builtin, host_ids}: пробный прогон на каждом хосте.
func (s *Server) handleF2BTemplateCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string  `json:"name"`
		Builtin bool    `json:"builtin"`
		HostIDs []int64 `json:"host_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	if !fail2ban.ValidTemplateName(req.Name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("f2b.badTemplateName", req.Name))
		return
	}
	if len(req.HostIDs) == 0 || len(req.HostIDs) > 500 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.f2bNoHosts"))
		return
	}
	states, _, err := s.f2bHosts(ctx)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	// f2bHosts уже без хостов вне пределов токена и не в сети.
	byID := map[int64]F2BHostState{}
	for _, st := range states {
		byID[st.ID] = st
	}
	user := auth.Username(ctx)
	// Свой шаблон — один на все хосты, с хаба.
	var custom *fail2ban.Template
	if !req.Builtin {
		var list f2bTemplates
		if _, err := s.localAPI(ctx, user, http.MethodGet, "/api/fail2ban/templates", nil, &list); err != nil {
			writeErr(w, r, http.StatusBadGateway, err)
			return
		}
		for i := range list.Custom {
			if list.Custom[i].Name == req.Name {
				custom = &list.Custom[i]
			}
		}
		if custom == nil {
			writeErr(w, r, http.StatusNotFound, msgs.Errorf("f2b.templateNotFound", req.Name))
			return
		}
	}

	results := make([]f2bTplHost, len(req.HostIDs))
	reqs := make([]*f2bTplReq, len(req.HostIDs))
	sem := make(chan struct{}, f2bTplParallel)
	var wg sync.WaitGroup
	for i, id := range req.HostIDs {
		st, ok := byID[id]
		if !ok {
			results[i] = f2bTplHost{ID: id, Status: "skip", Reason: msgs.Tc(ctx, "hub.f2bTplNoHost")}
			continue
		}
		res := &results[i]
		*res = f2bTplHost{ID: id, Name: st.Name}
		if st.Known && !st.Installed {
			res.Status, res.Reason = "skip", msgs.Tc(ctx, "hub.f2bTplNotInstalled")
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			reqs[i] = s.f2bTplCheckHost(ctx, user, id, req.Name, custom, res)
		}(i, id)
	}
	wg.Wait()

	plan := &f2bTplPlan{author: user, name: req.Name, expires: time.Now().Add(f2bTplCheckTTL)}
	for i, rq := range reqs {
		if rq != nil && results[i].Status == "changes" {
			plan.hosts = append(plan.hosts, F2BTplHostReq{ID: results[i].ID, Name: results[i].Name, Req: *rq})
		}
	}
	out := map[string]any{"hosts": results}
	if len(plan.hosts) > 0 {
		token := randomHex(16)
		f2bPlansMu.Lock()
		for k, p := range f2bPlans {
			if time.Now().After(p.expires) {
				delete(f2bPlans, k)
			}
		}
		f2bPlans[token] = plan
		f2bPlansMu.Unlock()
		out["token"] = token
	}
	writeJSON(w, http.StatusOK, out)
}

// f2bTplCheckHost — проверка одного хоста; запрос применения или nil.
func (s *Server) f2bTplCheckHost(ctx context.Context, user string, id int64, name string, custom *fail2ban.Template, res *f2bTplHost) *f2bTplReq {
	fail := func(code int, err error) *f2bTplReq {
		if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
			res.Status, res.Reason = "skip", msgs.Tc(ctx, "hub.f2bTplHostOld")
		} else {
			res.Status, res.Reason = "error", msgs.Localize(msgs.FromContext(ctx), err)
		}
		return nil
	}
	rq := f2bTplReq{Name: name}
	if custom != nil {
		rq.Description, rq.Jail, rq.Filter = custom.Description, custom.Jail, custom.Filter
	} else {
		var list f2bTemplates
		if code, err := s.hostCall(ctx, user, id, http.MethodGet, "/api/fail2ban/templates", nil, &list); err != nil {
			return fail(code, err)
		}
		var tpl *fail2ban.Template
		for i := range list.Builtin {
			if list.Builtin[i].Name == name {
				tpl = &list.Builtin[i]
			}
		}
		switch {
		case tpl == nil:
			res.Status, res.Reason = "skip", msgs.Tc(ctx, "hub.f2bTplHostOld")
			return nil
		case !tpl.Available:
			res.Status, res.Reason = "skip", msgs.Tc(ctx, "hub.f2bTplNoService", tpl.Service)
			return nil
		}
		rq.Jail, rq.Filter = tpl.Jail, tpl.Filter
	}
	dry := rq
	dry.DryRun = true
	var preview struct {
		Files []f2bFile `json:"files"`
	}
	if code, err := s.hostCall(ctx, user, id, http.MethodPost, "/api/fail2ban/templates/apply", dry, &preview); err != nil {
		return fail(code, err)
	}
	res.Files = preview.Files
	res.Status = "same"
	for _, f := range preview.Files {
		if f.Before != f.After {
			res.Status = "changes"
		}
	}
	return &rq
}

// handleF2BTemplateApply — POST /hub/fail2ban/templates/apply {token}:
// задание по проверенному (без проверки — отказ).
func (s *Server) handleF2BTemplateApply(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	f2bPlansMu.Lock()
	plan := f2bPlans[req.Token]
	if plan != nil && plan.author == user && time.Now().Before(plan.expires) {
		delete(f2bPlans, req.Token)
	} else {
		plan = nil
	}
	f2bPlansMu.Unlock()
	if plan == nil {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.f2bTplCheckFirst"))
		return
	}
	p := F2BTplParams{Template: plan.name, Hosts: plan.hosts}
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindF2BTemplate, TitleKey: "hub.f2bTplJob", TitleArgs: []any{plan.name, len(plan.hosts)},
		Author: user, Steps: len(plan.hosts), Params: p,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// F2BTemplateRunner — задание «шаблон на хосты».
type F2BTemplateRunner struct{ s *Server }

// NewF2BTemplateRunner — исполнитель.
func NewF2BTemplateRunner(s *Server) *F2BTemplateRunner { return &F2BTemplateRunner{s: s} }

// Run — по три хоста сразу; неудача на одном хосте не останавливает
// остальные. Откат при ошибке проверки конфигурации делает сам хост.
func (r *F2BTemplateRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p F2BTplParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	var mu sync.Mutex
	done, failed := 0, 0
	var touched []int64
	sem := make(chan struct{}, f2bTplParallel)
	var wg sync.WaitGroup
	for _, h := range p.Hosts {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(h F2BTplHostReq) {
			defer wg.Done()
			defer func() { <-sem }()
			rq := h.Req
			rq.DryRun = false
			if rq.Note == "" {
				rq.Note = msgs.T(jc.Lang(), "hub.f2bTplNote", p.Template)
			}
			code, err := r.s.hostCall(ctx, jc.Job.Author, h.ID, http.MethodPost, "/api/fail2ban/templates/apply", rq, nil)
			mu.Lock()
			defer mu.Unlock()
			done++
			jc.StepKey(done, len(p.Hosts), "hub.f2bStep", h.Name)
			switch {
			case err == nil:
				touched = append(touched, h.ID)
				jc.Log("hub.f2bTplHostDone", h.Name)
			case code == http.StatusConflict:
				failed++
				jc.Log("hub.f2bHostNoFail2ban", h.Name)
			default:
				failed++
				jc.Log("hub.f2bHostFailed", h.Name, msgs.Localize(jc.Lang(), err))
			}
		}(h)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.s.refreshF2B(ctx, touched)
	names := make([]string, 0, len(p.Hosts))
	for _, h := range p.Hosts {
		names = append(names, h.Name)
	}
	sort.Strings(names)
	r.s.db.Audit(ctx, jc.Job.Author, "fail2ban.template_fleet", p.Template+" → "+strings.Join(names, ", "), auditOK(failed == 0), nil)
	if failed > 0 {
		return msgs.Errorf("hub.f2bFailedCount", failed, len(p.Hosts))
	}
	return nil
}
