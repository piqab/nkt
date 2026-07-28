package monitor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/store"
)

// JobStatus records the outcome of the last run of one background job.
type JobStatus struct {
	Name       string `json:"name"`
	LastRun    string `json:"last_run,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	LastCount  int    `json:"last_count"`
	DurationMS int64  `json:"duration_ms"`
	Interval   string `json:"interval"`
	Runs       int    `json:"runs"`
}

// Scheduler drives every periodic job: rescans, probes, usage sampling, log
// ingestion and retention.
type Scheduler struct {
	cfg     *config.Config
	db      *store.DB
	scanner *inventory.Scanner
	prober  *Prober
	metrics *MetricsCollector
	logs    *LogCollector
	log     *slog.Logger

	mu     sync.RWMutex
	status map[string]*JobStatus
}

// NewScheduler wires the background jobs together.
func NewScheduler(cfg *config.Config, db *store.DB, scanner *inventory.Scanner, log *slog.Logger) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		db:      db,
		scanner: scanner,
		prober:  NewProber(db, cfg),
		metrics: NewMetricsCollector(db, scanner.Collector(), cfg),
		logs:    NewLogCollector(db, scanner.Collector()),
		log:     log,
		status:  map[string]*JobStatus{},
	}
}

// Prober exposes the availability prober for on-demand checks.
func (s *Scheduler) Prober() *Prober { return s.prober }

// MetricsSimulated reports whether usage numbers are synthetic.
func (s *Scheduler) MetricsSimulated() bool { return s.metrics.Simulated() }

// Status returns a copy of every job's last outcome.
func (s *Scheduler) Status() []JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]JobStatus, 0, len(s.status))
	for _, st := range s.status {
		out = append(out, *st)
	}
	return out
}

func (s *Scheduler) record(name string, interval time.Duration, started time.Time, count int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status[name]
	if st == nil {
		st = &JobStatus{Name: name, Interval: interval.String()}
		s.status[name] = st
	}
	st.LastRun = store.Now()
	st.LastCount = count
	st.DurationMS = time.Since(started).Milliseconds()
	st.Runs++
	if err != nil {
		st.LastError = err.Error()
		s.log.Error("фоновая задача завершилась ошибкой", "job", name, "err", err)
	} else {
		st.LastError = ""
	}
}

// Start launches every job and returns once they are running. Jobs stop when
// ctx is cancelled; wg tracks their shutdown.
func (s *Scheduler) Start(ctx context.Context, wg *sync.WaitGroup) {
	// The first scan must finish before probing, since it creates the targets.
	started := time.Now()
	_, err := s.scanner.Scan(ctx)
	s.record("inventory", s.cfg.InventoryInterval, started, 1, err)

	if s.cfg.IsFixtures() && s.cfg.DemoBackfill {
		started = time.Now()
		n, err := BackfillDemoHistory(ctx, s.db, 14)
		if n > 0 || err != nil {
			s.record("demo-backfill", 0, started, n, err)
			s.log.Info("в режиме снапшота засеяна синтетическая история",
				"rows", n, "note", "данные вымышленные, помечены как simulated")
		}
	}

	if !s.cfg.SchedulerEnabled {
		s.log.Info("планировщик отключён (NKT_SCHEDULER_ENABLED=false)")
		return
	}

	s.every(ctx, wg, "inventory", s.cfg.InventoryInterval, func(ctx context.Context) (int, error) {
		_, err := s.scanner.Scan(ctx)
		return 1, err
	})
	s.every(ctx, wg, "probes", s.cfg.ProbeInterval, s.prober.RunOnce)
	s.every(ctx, wg, "metrics", s.cfg.MetricsInterval, s.metrics.RunOnce)
	s.every(ctx, wg, "logs", s.cfg.LogScanInterval, func(ctx context.Context) (int, error) {
		sources := DiscoverSources(s.scanner.Latest(), s.cfg.NginxAccessLogs, s.cfg.HAProxyAccessLog)
		return s.logs.RunOnce(ctx, sources)
	})
	s.every(ctx, wg, "retention", 6*time.Hour, func(ctx context.Context) (int, error) {
		res, err := s.db.Purge(ctx, s.cfg.Retention)
		return int(res.Probes + res.Metrics + res.Sessions + res.Snapshots), err
	})
}

// every runs job on a fixed interval until ctx is done, executing it once
// immediately so a fresh start is not blind for a whole period.
func (s *Scheduler) every(ctx context.Context, wg *sync.WaitGroup, name string,
	interval time.Duration, job func(context.Context) (int, error)) {

	if interval <= 0 {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		run := func() {
			started := time.Now()
			n, err := job(ctx)
			s.record(name, interval, started, n, err)
		}
		run()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
