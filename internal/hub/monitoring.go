package hub

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// «Мониторинг» хаба: раз в час хаб забирает с каждого хоста (и с своей
// машины) почасовые сводки за недостающие часы (/api/monitor/summary) и
// хранит их у себя дольше, чем хосты: часы — NKT_HUB_HISTORY_HOURLY
// (90 дней), дни — NKT_HUB_HISTORY_DAILY (год). Раздел показывает
// историю отсюда — в том числе хостов, которые сейчас недоступны.

// monCollectEvery — как часто забирать сводки.
const monCollectEvery = time.Hour

// monHostSpan — сколько хост держит сам (дальше назад спрашивать
// бессмысленно) и сколько отдаёт за раз.
const (
	monHostSpan  = 30 * 24 * time.Hour
	monChunkSpan = 7 * 24 * time.Hour
)

// monParallel — сколько хостов опрашивается сразу.
const monParallel = 3

// monHostState — как прошёл последний сбор с хоста (для раздела).
type monHostState struct {
	At    time.Time
	OK    bool
	Old   bool // nkt без /monitor/summary
	Error string
}

type monState struct {
	mu         sync.Mutex
	hosts      map[int64]monHostState
	collecting bool
	lastRun    time.Time
	insights   []Insight
	analyzed   time.Time
}

// StartMonitoring — сбор раз в час (первый — через пару минут после
// запуска, чтобы хаб успел опросить хосты).
func (s *Server) StartMonitoring(ctx context.Context) {
	go func() {
		t := time.NewTimer(2 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			s.collectMonitoring(ctx)
			t.Reset(monCollectEvery)
		}
	}()
}

// collectMonitoring — один проход по всем хостам.
func (s *Server) collectMonitoring(ctx context.Context) {
	s.mon.mu.Lock()
	if s.mon.collecting {
		s.mon.mu.Unlock()
		return
	}
	s.mon.collecting = true
	if s.mon.hosts == nil {
		s.mon.hosts = map[int64]monHostState{}
	}
	s.mon.mu.Unlock()
	defer func() {
		s.mon.mu.Lock()
		s.mon.collecting = false
		s.mon.lastRun = time.Now()
		s.mon.mu.Unlock()
	}()

	ids := []int64{}
	if s.local != nil {
		ids = append(ids, localHostID)
	}
	if hosts, err := s.db.ListHosts(ctx); err == nil {
		for _, h := range hosts {
			if h.Status == store.HostStatusOnline {
				ids = append(ids, h.ID)
			}
		}
	}
	sem := make(chan struct{}, monParallel)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			st := s.collectHost(ctx, id)
			s.mon.mu.Lock()
			s.mon.hosts[id] = st
			s.mon.mu.Unlock()
		}(id)
	}
	wg.Wait()

	now := time.Now().UTC()
	hourly, daily := 90*24*time.Hour, 365*24*time.Hour
	if s.cfg != nil && s.cfg.HubHistoryHourly > 0 {
		hourly = s.cfg.HubHistoryHourly
	}
	if s.cfg != nil && s.cfg.HubHistoryDaily > 0 {
		daily = s.cfg.HubHistoryDaily
	}
	if err := s.db.MonPurge(ctx, now.Add(-hourly).Format("2006-01-02T15"), now.Add(-daily).Format("2006-01-02")); err != nil && s.log != nil {
		s.log.Warn("monitoring history purge failed", "err", err)
	}
	s.analyzeMonitoring(ctx)
}

// summaryResp — ответ /api/monitor/summary хоста.
type summaryResp struct {
	Series []struct {
		Source  string  `json:"source"`
		Subject string  `json:"subject"`
		Metric  string  `json:"metric"`
		Points  [][]any `json:"points"`
	} `json:"series"`
	Targets []struct {
		Key     string  `json:"key"`
		Label   string  `json:"label"`
		Kind    string  `json:"kind"`
		Host    string  `json:"host"`
		Port    int     `json:"port"`
		Service string  `json:"service"`
		Enabled bool    `json:"enabled"`
		Points  [][]any `json:"points"`
	} `json:"targets"`
	// K8s — состав кластера, если хост — control plane.
	K8s *monK8s `json:"k8s,omitempty"`
}

// collectHost — недостающие полные часы хоста кусками по неделе.
func (s *Server) collectHost(ctx context.Context, id int64) monHostState {
	now := time.Now().UTC().Truncate(time.Hour)
	since := now.Add(-monHostSpan)
	if last, err := s.db.MonLastHour(ctx, id); err == nil && last != "" {
		if t, err := time.Parse("2006-01-02T15", last); err == nil && t.Add(time.Hour).After(since) {
			// Последний час мог быть неполным — забирается заново.
			since = t
		}
	}
	st := monHostState{At: time.Now()}
	days := map[string]bool{}
	var k8s *monK8s
	for from := since; from.Before(now); from = from.Add(monChunkSpan) {
		to := from.Add(monChunkSpan)
		if to.After(now) {
			to = now
		}
		var resp summaryResp
		path := "/api/monitor/summary?since=" + url.QueryEscape(from.Format(time.RFC3339)) + "&until=" + url.QueryEscape(to.Format(time.RFC3339))
		code, err := s.hostCall(ctx, "", id, http.MethodGet, path, nil, &resp)
		if err != nil {
			if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
				st.Old = true
			}
			st.Error = msgs.Localize(msgs.DefaultLang, err)
			return st
		}
		if resp.K8s != nil {
			k8s = resp.K8s
		}
		var rows []store.MonRow
		for _, sr := range resp.Series {
			for _, p := range sr.Points {
				if r, ok := monPoint(sr.Source, sr.Subject, sr.Metric, p); ok {
					rows = append(rows, r)
					days[r.At[:10]] = true
				}
			}
		}
		var targets []store.MonTarget
		for _, t := range resp.Targets {
			targets = append(targets, store.MonTarget{Key: t.Key, Label: t.Label, Kind: t.Kind, Host: t.Host, Port: t.Port, Service: t.Service, Enabled: t.Enabled})
			for _, p := range t.Points {
				if len(p) < 4 {
					continue
				}
				hour, _ := p[0].(string)
				ok, _ := p[1].(float64)
				total, _ := p[2].(float64)
				lat, _ := p[3].(float64)
				if len(hour) != 13 {
					continue
				}
				rows = append(rows,
					store.MonRow{Source: "probe", Subject: t.Key, Metric: "ok", At: hour, Avg: ok, Max: ok, Sum: ok},
					store.MonRow{Source: "probe", Subject: t.Key, Metric: "total", At: hour, Avg: total, Max: total, Sum: total},
					store.MonRow{Source: "probe", Subject: t.Key, Metric: "latency_ms", At: hour, Avg: lat, Max: lat, Sum: lat})
				days[hour[:10]] = true
			}
		}
		if err := s.db.MonUpsertHourly(ctx, id, rows, false); err != nil {
			st.Error = err.Error()
			return st
		}
		if len(targets) > 0 {
			_ = s.db.MonSetTargets(ctx, id, targets)
		}
	}
	s.saveMonK8s(ctx, id, k8s)
	list := make([]string, 0, len(days))
	for d := range days {
		list = append(list, d)
	}
	sort.Strings(list)
	if err := s.db.MonRollupDaily(ctx, id, list); err != nil {
		st.Error = err.Error()
		return st
	}
	st.OK = true
	return st
}

// monPoint — точка ряда [час, среднее, пик, сумма].
func monPoint(source, subject, metric string, p []any) (store.MonRow, bool) {
	if len(p) < 4 {
		return store.MonRow{}, false
	}
	hour, _ := p[0].(string)
	avg, ok1 := p[1].(float64)
	mx, ok2 := p[2].(float64)
	sum, ok3 := p[3].(float64)
	if len(hour) != 13 || !ok1 || !ok2 || !ok3 || strings.TrimSpace(source) == "" {
		return store.MonRow{}, false
	}
	return store.MonRow{Source: source, Subject: subject, Metric: metric, At: hour, Avg: avg, Max: mx, Sum: sum}, true
}

// overviewOf — последний опрос хоста (доступен ли).
func (m *Manager) overviewOf(hostID int64) (hostOverview, bool) {
	m.overviewMu.Lock()
	defer m.overviewMu.Unlock()
	ov, ok := m.overview[hostID]
	return ov, ok
}
