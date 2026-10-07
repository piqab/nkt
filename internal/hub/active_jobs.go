package hub

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Индикатор фоновых операций в шапке интерфейса: что идёт сейчас — на
// самом хабе (его задания) и на всех хостах. Без него задание хоста было
// видно только в «Заданиях» этого хоста, и уход в другой раздел или на
// хаб выглядел как обрыв операции.

// ActiveJob — идущее задание и где оно идёт.
type ActiveJob struct {
	HostID   int64     `json:"host_id"`
	HostName string    `json:"host_name"`
	Job      store.Job `json:"job"`
}

// activeJobsTTL — сколько отдавать собранный список, не опрашивая хосты
// заново: индикатор открыт во многих вкладках, хостов — десятки.
const activeJobsTTL = 6 * time.Second

// activeJobsHostTimeout — сколько ждать один хост: недоступный не должен
// задерживать индикатор.
const activeJobsHostTimeout = 10 * time.Second

type activeJobsCache struct {
	mu   sync.Mutex
	at   map[msgs.Lang]time.Time
	list map[msgs.Lang][]ActiveJob
}

var activeCache = activeJobsCache{at: map[msgs.Lang]time.Time{}, list: map[msgs.Lang][]ActiveJob{}}

// handleActiveJobs — GET /hub/jobs/active: задания в очереди и в работе на
// хабе и на хостах. Хосты — только администратору: их задания и так видит
// только он.
func (s *Server) handleActiveJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lang := msgs.LangFromRequest(r)
	u, _ := auth.UserFromContext(ctx)
	admin := u.IsAdmin()
	allow := s.scopeFilter(ctx)

	var list []ActiveJob
	if admin && allow == nil {
		activeCache.mu.Lock()
		if time.Since(activeCache.at[lang]) < activeJobsTTL {
			list = activeCache.list[lang]
		}
		activeCache.mu.Unlock()
	}
	if list == nil {
		list = s.collectActiveJobs(ctx, lang, admin, allow)
		if admin && allow == nil {
			activeCache.mu.Lock()
			activeCache.at[lang], activeCache.list[lang] = time.Now(), list
			activeCache.mu.Unlock()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": list})
}

func (s *Server) collectActiveJobs(ctx context.Context, lang msgs.Lang, admin bool, allow func(int64) bool) []ActiveJob {
	out := []ActiveJob{}
	// Задания самого хаба (и его машины) — в его же базе.
	if local, _, err := s.db.FindJobs(ctx, store.JobFilter{Statuses: []string{store.JobQueued, store.JobRunning}, Limit: 50}); err == nil {
		for _, j := range local {
			j.Title = msgs.Render(lang, j.TitleKey, j.TitleArgs, j.Title)
			j.StepName = msgs.Render(lang, j.StepKey, j.StepArgs, j.StepName)
			out = append(out, ActiveJob{HostID: LocalHostID, HostName: "localhost", Job: j})
		}
	}
	if !admin {
		return out
	}
	hosts, err := s.db.ListHosts(ctx)
	if err != nil {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, h := range hosts {
		if h.ID == LocalHostID || h.Status != store.HostStatusOnline || (allow != nil && !allow(h.ID)) {
			continue
		}
		wg.Add(1)
		go func(h store.Host) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			hctx, cancel := context.WithTimeout(ctx, activeJobsHostTimeout)
			defer cancel()
			var res struct {
				Jobs []store.Job `json:"jobs"`
			}
			code, err := s.hub.HostAPI(hctx, h.ID, http.MethodGet, "/api/jobs?status=queued,running&limit=20&lang="+string(lang), nil, &res)
			if err != nil || code != http.StatusOK {
				return
			}
			mu.Lock()
			for _, j := range res.Jobs {
				out = append(out, ActiveJob{HostID: h.ID, HostName: h.Name, Job: j})
			}
			mu.Unlock()
		}(h)
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Job.CreatedAt > out[j].Job.CreatedAt })
	return out
}
