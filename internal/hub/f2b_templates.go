package hub

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
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
	HubAddr     string `json:"hub_addr,omitempty"`
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
	// Hub — защита хаба от бана: protected — уже есть, will_add — будет
	// добавлена первым файлом; пусто — не нужна (машина хаба).
	Hub     string `json:"hub,omitempty"`
	HubAddr string `json:"hub_addr,omitempty"`
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

// KindF2BTemplateCheck — задание хаба: проверка шаблона на хостах.
const KindF2BTemplateCheck = "fail2ban.template_check"

// F2BQueue — очередь заданий fail2ban хаба: проверки, применения шаблонов
// и баны идут по одному, не перекрывая друг друга на хостах.
const F2BQueue = "fail2ban"

// F2BTplCheckParams — параметры проверки.
type F2BTplCheckParams struct {
	Name    string             `json:"name"`
	Builtin bool               `json:"builtin"`
	Custom  *fail2ban.Template `json:"custom,omitempty"`
	HostIDs []int64            `json:"host_ids"`
}

// f2bCheckResult — итог задания проверки (для окна).
type f2bCheckResult struct {
	author  string
	hosts   []f2bTplHost
	token   string
	expires time.Time
}

var (
	f2bChecksMu sync.Mutex
	f2bChecks   = map[int64]*f2bCheckResult{}
)

// handleF2BTemplateCheck — POST /hub/fail2ban/templates/check
// {name, builtin, host_ids}: задание проверки (пробный прогон на каждом
// хосте с проверкой конфигурации fail2ban).
func (s *Server) handleF2BTemplateCheck(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var req F2BTplCheckParams
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
	if allow := s.scopeFilter(ctx); allow != nil {
		for _, id := range req.HostIDs {
			if !allow(id) {
				writeErr(w, r, http.StatusForbidden, msgs.Errorf("auth.tokenHostDenied"))
				return
			}
		}
	}
	user := auth.Username(ctx)
	// Свой шаблон — один на все хосты, с хаба: текст берётся сейчас и
	// уходит в задание.
	req.Custom = nil
	if !req.Builtin {
		var list f2bTemplates
		if _, err := s.localAPI(ctx, user, http.MethodGet, "/api/fail2ban/templates", nil, &list); err != nil {
			writeErr(w, r, http.StatusBadGateway, err)
			return
		}
		for i := range list.Custom {
			if list.Custom[i].Name == req.Name {
				req.Custom = &list.Custom[i]
			}
		}
		if req.Custom == nil {
			writeErr(w, r, http.StatusNotFound, msgs.Errorf("f2b.templateNotFound", req.Name))
			return
		}
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindF2BTemplateCheck, Queue: F2BQueue, TitleKey: "hub.f2bTplCheckJob", TitleArgs: []any{req.Name, len(req.HostIDs)},
		Author: user, Steps: len(req.HostIDs), Params: req,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// handleF2BTemplateCheckResult — GET /hub/fail2ban/templates/check/{job}:
// итог проверки, когда задание закончилось.
func (s *Server) handleF2BTemplateCheckResult(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "job"), 10, 64)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	f2bChecksMu.Lock()
	res := f2bChecks[id]
	f2bChecksMu.Unlock()
	if res == nil {
		job, err := s.db.JobByID(r.Context(), id)
		if err != nil || job.Kind != KindF2BTemplateCheck {
			writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.f2bTplCheckFirst"))
			return
		}
		done := job.Status == store.JobSucceeded || job.Status == store.JobFailed || job.Status == store.JobCanceled
		writeJSON(w, http.StatusOK, map[string]any{"done": done, "status": job.Status})
		return
	}
	if res.author != auth.Username(r.Context()) {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("hub.f2bTplCheckFirst"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"done": true, "hosts": res.hosts, "token": res.token})
}

// F2BTemplateCheckRunner — задание проверки.
type F2BTemplateCheckRunner struct{ s *Server }

// NewF2BTemplateCheckRunner — исполнитель проверки.
func NewF2BTemplateCheckRunner(s *Server) *F2BTemplateCheckRunner {
	return &F2BTemplateCheckRunner{s: s}
}

// Run — по три хоста сразу; итог — в памяти хаба для окна (годен
// f2bTplCheckTTL).
func (r *F2BTemplateCheckRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p F2BTplCheckParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	s := r.s
	lang := jc.Lang()
	states, _, err := s.f2bHosts(ctx)
	if err != nil {
		return err
	}
	byID := map[int64]F2BHostState{}
	for _, st := range states {
		byID[st.ID] = st
	}
	results := make([]f2bTplHost, len(p.HostIDs))
	reqs := make([]*f2bTplReq, len(p.HostIDs))
	var mu sync.Mutex
	done := 0
	logResult := func(res *f2bTplHost) {
		mu.Lock()
		defer mu.Unlock()
		done++
		jc.StepKey(done, len(p.HostIDs), "hub.f2bStep", res.Name)
		jc.Log("hub.f2bTplCheckHost", res.Name, msgs.T(lang, "hub.f2bTplStatus."+res.Status), res.Reason)
	}
	sem := make(chan struct{}, f2bTplParallel)
	var wg sync.WaitGroup
	for i, id := range p.HostIDs {
		res := &results[i]
		st, ok := byID[id]
		if !ok {
			*res = f2bTplHost{ID: id, Name: "#" + strconv.FormatInt(id, 10), Status: "skip", Reason: msgs.T(lang, "hub.f2bTplNoHost")}
			logResult(res)
			continue
		}
		*res = f2bTplHost{ID: id, Name: st.Name}
		if st.Known && !st.Installed {
			res.Status, res.Reason = "skip", msgs.T(lang, "hub.f2bTplNotInstalled")
			logResult(res)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			reqs[i] = s.f2bTplCheckHost(ctx, lang, jc.Job.Author, id, p.Name, p.Custom, &results[i])
			logResult(&results[i])
		}(i, id)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	out := &f2bCheckResult{author: jc.Job.Author, hosts: results, expires: time.Now().Add(f2bTplCheckTTL)}
	plan := &f2bTplPlan{author: jc.Job.Author, name: p.Name, expires: out.expires}
	for i, rq := range reqs {
		if rq != nil && results[i].Status == "changes" {
			plan.hosts = append(plan.hosts, F2BTplHostReq{ID: results[i].ID, Name: results[i].Name, Req: *rq})
		}
	}
	now := time.Now()
	f2bPlansMu.Lock()
	for k, old := range f2bPlans {
		if now.After(old.expires) {
			delete(f2bPlans, k)
		}
	}
	if len(plan.hosts) > 0 {
		out.token = randomHex(16)
		f2bPlans[out.token] = plan
	}
	f2bPlansMu.Unlock()
	f2bChecksMu.Lock()
	for k, old := range f2bChecks {
		if now.After(old.expires) {
			delete(f2bChecks, k)
		}
	}
	f2bChecks[jc.Job.ID] = out
	f2bChecksMu.Unlock()
	jc.Log("hub.f2bTplCheckSummary", len(plan.hosts), len(p.HostIDs))
	return nil
}

// f2bTplCheckHost — проверка одного хоста; запрос применения или nil.
func (s *Server) f2bTplCheckHost(ctx context.Context, lang msgs.Lang, user string, id int64, name string, custom *fail2ban.Template, res *f2bTplHost) *f2bTplReq {
	fail := func(code int, err error) *f2bTplReq {
		// Старый nkt: нет такого API или не знает новых полей (hub_addr) —
		// обновить, а не «ошибка».
		old := code == http.StatusNotFound || code == http.StatusMethodNotAllowed ||
			(code == http.StatusBadRequest && strings.Contains(err.Error(), "unknown field"))
		if old {
			res.Status, res.Reason = "skip", msgs.T(lang, "hub.f2bTplHostOld")
		} else {
			res.Status, res.Reason = "error", msgs.Localize(lang, err)
		}
		return nil
	}
	rq := f2bTplReq{Name: name}
	// Адрес хаба глазами хоста — до джейла он ляжет в ignoreip. Машине
	// хаба защита не нужна (хаб к ней по SSH не ходит); хост, адреса хаба
	// у которого не узнать (только туннель), джейл вслепую не получает.
	if id != localHostID {
		addr, err := s.hub.hubAddrSeenBy(ctx, id)
		if err != nil {
			res.Status, res.Reason = "skip", msgs.T(lang, "hub.f2bTplNoHubAddr", msgs.Localize(lang, err))
			return nil
		}
		rq.HubAddr = addr.String()
	}
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
			res.Status, res.Reason = "skip", msgs.T(lang, "hub.f2bTplHostOld")
			return nil
		case !tpl.Available:
			res.Status, res.Reason = "skip", msgs.T(lang, "hub.f2bTplNoService", tpl.Service)
			return nil
		}
		rq.Jail, rq.Filter = tpl.Jail, tpl.Filter
	}
	dry := rq
	dry.DryRun = true
	var preview struct {
		Files        []f2bFile            `json:"files"`
		Test         *fail2ban.ConfigTest `json:"test"`
		HubProtected bool                 `json:"hub_protected"`
	}
	if code, err := s.hostCall(ctx, user, id, http.MethodPost, "/api/fail2ban/templates/apply", dry, &preview); err != nil {
		return fail(code, err)
	}
	// Без проверки конфигурации (nkt старше) — не применять: так и
	// ломались хосты с чужим сломанным джейлом.
	if preview.Test == nil {
		res.Status, res.Reason = "skip", msgs.T(lang, "hub.f2bTplHostOld")
		return nil
	}
	res.Files = preview.Files
	res.HubAddr = rq.HubAddr
	if rq.HubAddr != "" {
		res.Hub = "will_add"
		if preview.HubProtected {
			res.Hub = "protected"
		}
	}
	if !preview.Test.OK {
		key := "hub.f2bTplTestFailed"
		if preview.Test.Preexisting {
			key = "hub.f2bTplBrokenBefore"
		}
		res.Status, res.Reason = "error", msgs.T(lang, key, preview.Test.Output)
		return nil
	}
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
		Kind: KindF2BTemplate, Queue: F2BQueue, TitleKey: "hub.f2bTplJob", TitleArgs: []any{plan.name, len(plan.hosts)},
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
			var resp struct {
				HubUnbanned []string `json:"hub_unbanned"`
			}
			code, err := r.s.hostCall(ctx, jc.Job.Author, h.ID, http.MethodPost, "/api/fail2ban/templates/apply", rq, &resp)
			mu.Lock()
			defer mu.Unlock()
			done++
			jc.StepKey(done, len(p.Hosts), "hub.f2bStep", h.Name)
			switch {
			case err == nil:
				touched = append(touched, h.ID)
				jc.Log("hub.f2bTplHostDone", h.Name)
				if len(resp.HubUnbanned) > 0 {
					jc.Log("hub.f2bTplHubUnbanned", h.Name, rq.HubAddr, strings.Join(resp.HubUnbanned, ", "))
				}
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
