package hub

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// «Установить nkt на хосты» (раздел «О системе», рядом с «удалить
// везде»): обычная установка на каждом выбранном хосте — заданием хаба
// по три хоста; задание ждёт итог каждой установки и пересказывает его.

// KindInstallAll — задание хаба: установка nkt на выбранные хосты.
const KindInstallAll = "hub.installall"

// installAllParallel — сколько установок идёт сразу.
const installAllParallel = 3

// installAllWait — предел ожидания одной установки.
const installAllWait = 45 * time.Minute

// InstallAllParams — параметры задания.
type InstallAllParams struct {
	HostIDs []int64 `json:"host_ids"`
}

// handleInstallAll — POST /hub/install-all {host_ids}.
func (s *Server) handleInstallAll(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var p InstallAllParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if len(p.HostIDs) == 0 || len(p.HostIDs) > 500 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.installAllNoHosts"))
		return
	}
	ctx := r.Context()
	allow := s.scopeFilter(ctx)
	seen := map[int64]bool{}
	for _, id := range p.HostIDs {
		if id == localHostID || seen[id] || (allow != nil && !allow(id)) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.installAllNoHosts"))
			return
		}
		seen[id] = true
		if _, err := s.db.HostByID(ctx, id); err != nil {
			fail(w, r, err)
			return
		}
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindInstallAll, TitleKey: "hub.installAllJob", TitleArgs: []any{len(p.HostIDs)},
		Author: auth.Username(ctx), Steps: len(p.HostIDs), Params: p,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// InstallAllRunner — задание установки на хосты.
type InstallAllRunner struct{ m *Manager }

// NewInstallAllRunner — исполнитель.
func NewInstallAllRunner(m *Manager) *InstallAllRunner { return &InstallAllRunner{m: m} }

// Run — по три хоста; неудача на одном не останавливает остальные.
func (r *InstallAllRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p InstallAllParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	var mu sync.Mutex
	done, failed := 0, 0
	step := func(name, key string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		done++
		jc.StepKey(done, len(p.HostIDs), "hub.installAllStep", name)
		jc.Log(key, append([]any{name}, args...)...)
	}
	sem := make(chan struct{}, installAllParallel)
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
			host, err := r.m.db.HostByID(ctx, id)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				step("#"+strconv.FormatInt(id, 10), "hub.installAllGone")
				return
			}
			jobID, err := r.m.StartInstall(ctx, id, false, nil)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				var foreign *ForeignInstallError
				if errors.As(err, &foreign) {
					step(host.Name, "hub.installAllForeign", foreign.Detail)
				} else {
					step(host.Name, "hub.installAllFailed", msgs.Localize(jc.Lang(), err))
				}
				return
			}
			status, errText := r.waitJob(ctx, jobID)
			if status == store.JobSucceeded {
				step(host.Name, "hub.installAllDone", jobID)
				return
			}
			mu.Lock()
			failed++
			mu.Unlock()
			step(host.Name, "hub.installAllJobFailed", jobID, errText)
		}(id)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.m.db.Audit(ctx, jc.Job.Author, "hub.install_all", strconv.Itoa(len(p.HostIDs)), auditOK(failed == 0), nil)
	if failed > 0 {
		return msgs.Errorf("hub.installAllFailedCount", failed, len(p.HostIDs))
	}
	return nil
}

// waitJob — ждать задание установки: итог и текст ошибки.
func (r *InstallAllRunner) waitJob(ctx context.Context, id int64) (string, string) {
	deadline := time.Now().Add(installAllWait)
	for time.Now().Before(deadline) {
		j, err := r.m.db.JobByID(ctx, id)
		if err == nil && (j.Status == store.JobSucceeded || j.Status == store.JobFailed || j.Status == store.JobCanceled) {
			return j.Status, j.Error
		}
		select {
		case <-ctx.Done():
			return store.JobCanceled, ctx.Err().Error()
		case <-time.After(2 * time.Second):
		}
	}
	return store.JobFailed, msgs.T(msgs.DefaultLang, "hub.installAllTimeout")
}
