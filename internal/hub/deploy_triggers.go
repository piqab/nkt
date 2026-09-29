package hub

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Кто запускает выкладки кроме кнопки: вебхук (напрямую к хабу или через
// nkt-edge), опрос репозитория и слежение за тегами образа в registry.

// handleHook — POST /hub/hooks/{hook}: без сессии, доступ — подписью.
// Ответ нарочно скупой: кто подбирает адреса, не узнаёт, какой из них
// настоящий (и неизвестный адрес, и неверная подпись — 401).
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	s.serveHook(w, r, clientIP(r))
}

// clientIP — адрес отправителя для журнала.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) serveHook(w http.ResponseWriter, r *http.Request, from string) {
	ctx := r.Context()
	hookID := chi.URLParam(r, "hook")
	body, err := io.ReadAll(io.LimitReader(r.Body, deploy.MaxHookBody+1))
	if err != nil || len(body) > deploy.MaxHookBody {
		writeError(w, http.StatusRequestEntityTooLarge, "too large")
		return
	}
	reject := func(reason string) {
		s.db.Audit(ctx, "hook", "pipeline.hook.rejected", hookID, "error", map[string]any{"from": from, "reason": reason})
		writeError(w, http.StatusUnauthorized, "unauthorized")
	}
	pl, err := s.db.PipelineByHook(ctx, hookID)
	if err != nil {
		reject("unknown hook")
		return
	}
	secret, err := secretbox.Decrypt(s.hub.key, pl.HookSecret)
	if err != nil {
		reject("secret")
		return
	}
	ev, err := deploy.VerifyHook(r.Header, body, string(secret), time.Now())
	if err != nil {
		reject(msgs.Localize(msgs.EN, err))
		return
	}
	fresh, err := s.db.RememberDelivery(ctx, ev.Delivery)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if !fresh {
		reject("replayed delivery")
		return
	}
	if ev.Ping {
		writeJSON(w, http.StatusOK, map[string]string{"status": "pong"})
		return
	}
	if !pl.Enabled {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "pipeline disabled"})
		return
	}
	spec, err := deploy.ParseSpec(pl.Content)
	if err != nil {
		writeErr(w, r, http.StatusConflict, err)
		return
	}
	dec := deploy.Decide(spec, ev)
	if !dec.Deploy {
		reason := msgs.T(msgs.EN, dec.Reason)
		s.db.Audit(ctx, "hook", "pipeline.hook.ignored", pl.Name, "ok", map[string]any{"from": from, "ref": ev.Ref, "reason": reason})
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": reason})
		return
	}
	d, err := s.startDeployment(ctx, pl, store.Deployment{Ref: dec.Ref, Commit: dec.Commit, Tag: dec.Tag, Trigger: "webhook", Author: ev.Provider}, false)
	s.db.Audit(ctx, "hook", "pipeline.hook.deploy", pl.Name, auditOutcome(err), map[string]any{"from": from, "provider": ev.Provider, "ref": dec.Ref, "commit": dec.Commit, "tag": dec.Tag, "job_id": d.JobID})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "deploying", "deployment_id": d.ID})
}

// pipelineWatch — когда что проверялось в последний раз.
type pipelineWatch struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (w *pipelineWatch) due(key string, every time.Duration, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		w.last = map[string]time.Time{}
	}
	if now.Sub(w.last[key]) < every {
		return false
	}
	w.last[key] = now
	return true
}

// StartPipelineWatch — фоновое слежение за конвейерами с poll и registry.
// Конвейер, который ещё ни разу не выкладывался, сам не запускается —
// первую выкладку делают кнопкой (иначе новый конвейер выкладывался бы
// сразу после сохранения описания).
func (s *Server) StartPipelineWatch(ctx context.Context) {
	w := &pipelineWatch{}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.checkPipelines(ctx, w, time.Now())
			}
		}
	}()
}

func (s *Server) checkPipelines(ctx context.Context, w *pipelineWatch, now time.Time) {
	list, err := s.db.ListPipelines(ctx)
	if err != nil {
		return
	}
	for _, pl := range list {
		if !pl.Enabled || pl.LastCommit == "" {
			continue
		}
		spec, err := deploy.ParseSpec(pl.Content)
		if err != nil {
			continue
		}
		if spec.Poll != "" && spec.Ref != "" {
			every, _ := time.ParseDuration(spec.Poll)
			if w.due("poll:"+pl.HookID, every, now) {
				s.pollRepo(ctx, pl, spec)
			}
		}
		if spec.Registry != "" {
			every := 5 * time.Minute
			if spec.RegistryPoll != "" {
				every, _ = time.ParseDuration(spec.RegistryPoll)
			}
			if w.due("registry:"+pl.HookID, every, now) {
				s.pollRegistry(ctx, pl, spec)
			}
		}
	}
}

// pollRepo — новый коммит в ветке: выкладка.
func (s *Server) pollRepo(ctx context.Context, pl store.Pipeline, spec deploy.Spec) {
	g := deploy.Git{Dir: s.pipelineDir(pl.ID), Cred: s.pipelineCred(pl)}
	refs, err := g.Remote(ctx, spec.Repo)
	if err != nil {
		return
	}
	commit := refs["refs/heads/"+spec.Ref]
	if commit == "" || commit == pl.LastCommit || commit == pl.FailedCommit {
		// Упавший коммит не повторяется — ждём нового или кнопки.
		return
	}
	_, _ = s.startDeployment(ctx, pl, store.Deployment{Ref: spec.Ref, Commit: commit, Tag: pl.LastTag, Trigger: "poll", Author: "poll"}, true)
}

// pollRegistry — новый тег образа: выкладка ветки с этим тегом.
func (s *Server) pollRegistry(ctx context.Context, pl store.Pipeline, spec deploy.Spec) {
	cred := ""
	if len(pl.RegistryCred) > 0 {
		if raw, err := secretbox.Decrypt(s.hub.key, pl.RegistryCred); err == nil {
			cred = string(raw)
		}
	}
	tags, err := deploy.ListTags(ctx, spec.Registry, cred)
	if err != nil {
		return
	}
	newest := deploy.NewestTag(tags, spec.RegistryTags)
	if newest == "" || newest == pl.LastTag || newest == pl.FailedTag || (pl.LastTag != "" && deploy.CompareVersions(newest, pl.LastTag) <= 0) {
		return
	}
	_, _ = s.startDeployment(ctx, pl, store.Deployment{Ref: spec.Ref, Tag: newest, Trigger: "registry", Author: "registry"}, true)
}
