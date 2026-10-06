package hub

import (
	"context"
	"net/http"
	"slices"
	"sync"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// «Запустить все» и «остановить все» в списке хостов — заданием хаба:
// раньше браузер слал по запросу на хост разом и ждал их все, и уход со
// страницы оставлял итог неизвестным. Теперь ход и итог по каждому хосту —
// в журнале задания (и в индикаторе фоновых операций).

// KindServiceAll — вид задания.
const KindServiceAll = "hub.serviceall"

// serviceAllParallel — хостов одновременно: каждый — SSH и systemctl.
const serviceAllParallel = 8

// ServiceAllParams — вход задания.
type ServiceAllParams struct {
	HostIDs []int64 `json:"host_ids"`
	Running bool    `json:"running"`
}

// handleServiceAll — POST /hub/hosts/service-all {host_ids, running}.
func (s *Server) handleServiceAll(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var p ServiceAllParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	allow := s.scopeFilter(r.Context())
	ids := []int64{}
	for _, id := range p.HostIDs {
		if id == LocalHostID || slices.Contains(ids, id) || (allow != nil && !allow(id)) {
			continue
		}
		if _, err := s.db.HostByID(r.Context(), id); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > 1000 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.installAllNoHosts"))
		return
	}
	p.HostIDs = ids
	title := "hub.serviceAllStopJob"
	if p.Running {
		title = "hub.serviceAllStartJob"
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{Kind: KindServiceAll, TitleKey: title, TitleArgs: []any{len(ids)},
		Queue: "hub-serviceall", Author: user, Steps: len(ids), Params: p})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "hub.service-all", title, "ok", map[string]any{"job_id": id, "hosts": len(ids)})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// ServiceAllRunner — исполнитель.
type ServiceAllRunner struct{ m *Manager }

// NewServiceAllRunner — исполнитель запуска/остановки nkt на хостах.
func NewServiceAllRunner(m *Manager) *ServiceAllRunner { return &ServiceAllRunner{m: m} }

// Run — по serviceAllParallel хостов; провал одного не мешает остальным.
func (r *ServiceAllRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ServiceAllParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	done, failed := 0, 0
	sem := make(chan struct{}, serviceAllParallel)
	for _, id := range p.HostIDs {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			name := ""
			if h, err := r.m.db.HostByID(ctx, id); err == nil {
				name = h.Name
			}
			err := r.m.SetServiceRunning(ctx, id, p.Running)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				jc.Log("hub.serviceAllFailed", name, msgs.Localize(jc.Lang(), err))
			} else {
				jc.Log("hub.serviceAllOK", name)
			}
			done++
			jc.StepKey(done, len(p.HostIDs), "hub.serviceAllStep", name)
		}(id)
	}
	wg.Wait()
	jc.Log("hub.serviceAllSummary", len(p.HostIDs)-failed, failed)
	if failed > 0 {
		return msgs.Errorf("hub.serviceAllSomeFailed", failed, len(p.HostIDs))
	}
	return nil
}
