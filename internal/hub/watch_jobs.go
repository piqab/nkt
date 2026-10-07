package hub

import (
	"context"
	"net/http"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Сканирование уязвимостей управляемого хоста и «Собрать сейчас» в
// мониторинге с ?job=1 — ещё и заданиями хаба: сама работа та же (фоновая,
// с ходом на своих страницах), а задание следит за ней и пишет ход в
// журнал — так она видна в «Заданиях» хаба и в индикаторе фоновых
// операций, а не только на своей странице.

const (
	// KindHostVulnScan — вид задания «скан уязвимостей хоста».
	KindHostVulnScan = "hub.vulnscan"
	// KindMonCollect — вид задания «собрать мониторинг».
	KindMonCollect = "hub.moncollect"
)

// HostVulnScanParams — какой хост.
type HostVulnScanParams struct {
	HostID int64  `json:"host_id"`
	Name   string `json:"name"`
}

// startWatchJob заводит задание и отвечает job_id; job_scope «hub» —
// задание хаба, а не хоста, на странице которого нажата кнопка: его
// журнал — по /hosts/local.
func (s *Server) startWatchJob(w http.ResponseWriter, r *http.Request, spec jobs.Spec) bool {
	if s.jobs == nil {
		return false
	}
	spec.Author = auth.Username(r.Context())
	spec.Steps = 1
	id, err := s.jobs.Start(r.Context(), spec)
	if err != nil {
		return false
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "started", "job_id": id, "job_scope": "hub"})
	return true
}

// HostVulnScanRunner следит за сканом хоста.
type HostVulnScanRunner struct{ m *Manager }

// NewHostVulnScanRunner — исполнитель.
func NewHostVulnScanRunner(m *Manager) *HostVulnScanRunner { return &HostVulnScanRunner{m: m} }

// Run переносит ход скана в журнал до его конца.
func (r *HostVulnScanRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p HostVulnScanParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	jc.StepKey(1, 1, "hub.vulnScanJob", p.Name)
	state := r.m.vulnStateFor(p.HostID)
	return watchState(ctx, jc, func() (bool, string, string) {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.scanning, state.progress, state.lastErr
	})
}

// MonCollectRunner собирает мониторинг (или ждёт уже идущего сбора).
type MonCollectRunner struct{ s *Server }

// NewMonCollectRunner — исполнитель.
func NewMonCollectRunner(s *Server) *MonCollectRunner { return &MonCollectRunner{s: s} }

// Run собирает мониторинг со всех хостов в сети.
func (r *MonCollectRunner) Run(ctx context.Context, jc *jobs.Context) error {
	jc.StepKey(1, 1, "hub.monCollectJob")
	r.s.mon.mu.Lock()
	busy := r.s.mon.collecting
	r.s.mon.mu.Unlock()
	if busy {
		jc.Log("hub.monCollectAlreadyRunning")
		return watchState(ctx, jc, func() (bool, string, string) {
			r.s.mon.mu.Lock()
			defer r.s.mon.mu.Unlock()
			return r.s.mon.collecting, "", ""
		})
	}
	r.s.collectMonitoring(ctx)
	jc.Log("hub.monCollectDone")
	return nil
}

// watchState переносит ход фоновой работы (running, progress, lastErr) в
// журнал задания, пока она не кончится; её ошибка — ошибка задания.
func watchState(ctx context.Context, jc *jobs.Context, state func() (bool, string, string)) error {
	last := ""
	for {
		running, progress, lastErr := state()
		if progress != "" && progress != last {
			last = progress
			jc.Logf("%s", progress)
		}
		if !running {
			if lastErr != "" {
				return msgs.Errorf("hub.watchFailed", lastErr)
			}
			jc.Log("hub.dbRefreshDoneGeneric")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
