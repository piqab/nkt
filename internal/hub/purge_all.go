package hub

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// «Удалить nkt со всех хостов» («О системе» → «Опасная зона»): задание
// хаба чистит выбранные хосты (как удаление одного хоста) и убирает из
// хаба те, где очистка прошла. Перед ним обязателен полный экспорт (с
// ключом) — проверяется и здесь, а не только в окне.

// KindPurgeAll — вид задания.
const KindPurgeAll = "hub.purgeall"

// purgeAllExportWindow — насколько свежим должен быть экспорт.
const purgeAllExportWindow = 30 * time.Minute

// maxParallelPurges — хостов одновременно (как у «Обновить всё»).
const maxParallelPurges = 3

// PurgeAllParams — вход задания.
type PurgeAllParams struct {
	HostIDs []int64      `json:"host_ids"`
	Purge   PurgeOptions `json:"purge"`
}

var (
	fullExportMu sync.Mutex
	fullExportAt time.Time
)

func (m *Manager) noteFullExport() {
	fullExportMu.Lock()
	fullExportAt = time.Now()
	fullExportMu.Unlock()
}

func recentFullExport() bool {
	fullExportMu.Lock()
	defer fullExportMu.Unlock()
	return !fullExportAt.IsZero() && time.Since(fullExportAt) < purgeAllExportWindow
}

// handlePurgeAll — POST /hub/purge-all {host_ids, purge}.
func (s *Server) handlePurgeAll(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var p PurgeAllParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !recentFullExport() {
		writeErr(w, r, http.StatusPreconditionRequired, msgs.Errorf("hub.purgeAllNeedsExport"))
		return
	}
	p.Purge.VM, p.Purge.VMDisks = false, false
	ids := []int64{}
	for _, id := range p.HostIDs {
		if id != LocalHostID && !slices.Contains(ids, id) {
			if _, err := s.db.HostByID(r.Context(), id); err == nil {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 || len(ids) > 1000 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.purgeAllNoHosts"))
		return
	}
	p.HostIDs = ids
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{Kind: KindPurgeAll, TitleKey: "hub.purgeAllTitle", TitleArgs: []any{len(ids)},
		Author: user, Steps: len(ids), Params: p})
	s.db.Audit(r.Context(), user, "hub.purge-all", strconv.Itoa(len(ids)), auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// PurgeAllRunner выполняет KindPurgeAll.
type PurgeAllRunner struct{ m *Manager }

// NewPurgeAllRunner — исполнитель.
func NewPurgeAllRunner(m *Manager) *PurgeAllRunner { return &PurgeAllRunner{m: m} }

// Run — хосты параллельно (не больше трёх); провал одного не мешает
// остальным; в конце — сколько убрано и сколько осталось.
func (r *PurgeAllRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p PurgeAllParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	var mu sync.Mutex
	done, failed := 0, 0
	sem := make(chan struct{}, maxParallelPurges)
	var wg sync.WaitGroup
	for _, id := range p.HostIDs {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			host, err := r.m.db.HostByID(ctx, id)
			if err != nil {
				return
			}
			res := r.m.PurgeHost(ctx, id, p.Purge)
			mu.Lock()
			defer mu.Unlock()
			for _, st := range res.Steps {
				jc.Logf("%s: %s", host.Name, st)
			}
			if res.OK && res.Error == "" {
				r.m.CloseHost(id)
				if err := r.m.db.DeleteHost(context.WithoutCancel(ctx), id); err != nil {
					failed++
					jc.Log("hub.purgeAllKeepErr", host.Name, err)
				} else {
					done++
					jc.Log("hub.purgeAllRemoved", host.Name)
				}
			} else {
				failed++
				jc.Log("hub.purgeAllFailed", host.Name, res.Error)
			}
			jc.StepKey(done+failed, len(p.HostIDs), "hub.purgeAllStep", host.Name)
		}(id)
	}
	wg.Wait()
	jc.Log("hub.purgeAllSummary", done, failed)
	if failed > 0 {
		return msgs.Errorf("hub.purgeAllSomeFailed", failed)
	}
	return nil
}

// KindHostPurge — удаление одного хоста с очисткой (форма удаления хоста):
// цепочка SSH-шагов, а с машиной — ещё и её выключение и удаление дисков.
// Заданием, а не внутри запроса: ход виден в журнале, а закрытое окно или
// оборванный запрос не бросают очистку на полпути.
const KindHostPurge = "hub.hostpurge"

// HostPurgeParams — вход задания.
type HostPurgeParams struct {
	HostID int64        `json:"host_id"`
	Name   string       `json:"name"`
	Purge  PurgeOptions `json:"purge"`
}

// HostPurgeRunner выполняет KindHostPurge.
type HostPurgeRunner struct{ m *Manager }

// NewHostPurgeRunner — исполнитель.
func NewHostPurgeRunner(m *Manager) *HostPurgeRunner { return &HostPurgeRunner{m: m} }

// Run чистит хост и убирает его запись — запись в любом случае, как и при
// удалении без задания: погашенный сервер не должен висеть в списке
// вечно. Что не удалось убрать с хоста — ошибка задания.
func (r *HostPurgeRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p HostPurgeParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	jc.StepKey(1, 2, "hub.hostPurgeStepClean", p.Name)
	res := r.m.PurgeHost(ctx, p.HostID, p.Purge)
	for _, st := range res.Steps {
		jc.Logf("%s", st)
	}
	jc.StepKey(2, 2, "hub.hostPurgeStepRemove", p.Name)
	r.m.CloseHost(p.HostID)
	if err := r.m.db.DeleteHost(context.WithoutCancel(ctx), p.HostID); err != nil {
		return err
	}
	jc.Log("hub.purgeAllRemoved", p.Name)
	if !res.OK || res.Error != "" {
		return msgs.Errorf("hub.hostPurgeIncomplete", p.Name, res.Error)
	}
	return nil
}
