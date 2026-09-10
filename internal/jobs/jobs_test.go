package jobs

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/store"
)

func newTestManager(t *testing.T) (*Manager, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m := New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(m.Close)
	return m, db
}

// runnerFunc — исполнитель из функции.
type runnerFunc struct {
	fn        func(ctx context.Context, jc *Context) error
	resumable bool
}

func (r runnerFunc) Run(ctx context.Context, jc *Context) error { return r.fn(ctx, jc) }
func (r runnerFunc) Resumable() bool                            { return r.resumable }

// waitFor ждёт условия, а не «достаточного» сна: задания асинхронные, и
// фиксированная пауза либо тормозит тесты, либо мигает.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("не дождались: %s", what)
}

func jobStatus(t *testing.T, db *store.DB, id int64) string {
	t.Helper()
	job, err := db.JobByID(context.Background(), id)
	if err != nil {
		t.Fatalf("JobByID: %v", err)
	}
	return job.Status
}

func TestJobRunsAndLogs(t *testing.T) {
	m, db := newTestManager(t)
	m.Register("demo", runnerFunc{fn: func(_ context.Context, jc *Context) error {
		var p struct {
			Name string `json:"name"`
		}
		if err := jc.Params(&p); err != nil {
			return err
		}
		jc.Step(1, 2, "первый")
		jc.Logf("привет, %s", p.Name)
		jc.Step(2, 2, "второй")
		return nil
	}})

	id, err := m.Start(context.Background(), Spec{
		Kind: "demo", Title: "проверка", Queue: "host", Steps: 2,
		Params: map[string]string{"name": "мир"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "задание завершилось", func() bool { return jobStatus(t, db, id) == store.JobSucceeded })

	lines, err := db.JobLog(context.Background(), id, 0, 100)
	if err != nil {
		t.Fatalf("JobLog: %v", err)
	}
	if len(lines) != 1 || lines[0].Text != "привет, мир" {
		t.Fatalf("журнал = %+v", lines)
	}
	job, _ := db.JobByID(context.Background(), id)
	if job.Step != 2 || job.Steps != 2 || job.StepName != "второй" {
		t.Errorf("шаг = %d/%d %q", job.Step, job.Steps, job.StepName)
	}
	if job.StartedAt == "" || job.FinishedAt == "" {
		t.Errorf("времена не проставлены: %+v", job)
	}
}

func TestJobFailureRecorded(t *testing.T) {
	m, db := newTestManager(t)
	m.Register("bad", runnerFunc{fn: func(context.Context, *Context) error {
		return errors.New("не вышло")
	}})
	id, _ := m.Start(context.Background(), Spec{Kind: "bad", Queue: "host"})
	waitFor(t, "задание провалилось", func() bool { return jobStatus(t, db, id) == store.JobFailed })

	job, _ := db.JobByID(context.Background(), id)
	if job.Error != "не вышло" {
		t.Errorf("ошибка = %q", job.Error)
	}
}

// Одновременно на ключе очереди выполняется одно задание: два apt-get на
// одной машине разом — верный способ получить беспорядок.
func TestQueueRunsOneAtATime(t *testing.T) {
	m, db := newTestManager(t)
	var mu sync.Mutex
	var now, peak int
	release := make(chan struct{})
	m.Register("slow", runnerFunc{fn: func(ctx context.Context, _ *Context) error {
		mu.Lock()
		now++
		if now > peak {
			peak = now
		}
		mu.Unlock()
		<-release
		mu.Lock()
		now--
		mu.Unlock()
		return nil
	}})

	first, _ := m.Start(context.Background(), Spec{Kind: "slow", Queue: "host-1"})
	second, _ := m.Start(context.Background(), Spec{Kind: "slow", Queue: "host-1"})
	waitFor(t, "первое пошло", func() bool { return jobStatus(t, db, first) == store.JobRunning })
	if got := jobStatus(t, db, second); got != store.JobQueued {
		t.Fatalf("второе задание = %q, want %q", got, store.JobQueued)
	}

	// Другой ключ очереди не ждёт — это разные машины.
	other, _ := m.Start(context.Background(), Spec{Kind: "slow", Queue: "host-2"})
	waitFor(t, "задание другого ключа пошло", func() bool { return jobStatus(t, db, other) == store.JobRunning })

	close(release)
	waitFor(t, "все завершились", func() bool {
		return jobStatus(t, db, first) == store.JobSucceeded &&
			jobStatus(t, db, second) == store.JobSucceeded &&
			jobStatus(t, db, other) == store.JobSucceeded
	})
	mu.Lock()
	defer mu.Unlock()
	if peak > 2 {
		t.Errorf("одновременно выполнялось %d заданий, а ключей очереди было два", peak)
	}
}

func TestCancelRunningAndQueued(t *testing.T) {
	m, db := newTestManager(t)
	started := make(chan struct{})
	m.Register("wait", runnerFunc{fn: func(ctx context.Context, _ *Context) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}})

	running, _ := m.Start(context.Background(), Spec{Kind: "wait", Queue: "host"})
	queued, _ := m.Start(context.Background(), Spec{Kind: "wait", Queue: "host"})
	<-started

	// Ждущее отменяется, не запускаясь вовсе.
	if err := m.Cancel(context.Background(), queued); err != nil {
		t.Fatalf("Cancel(ждущее): %v", err)
	}
	if got := jobStatus(t, db, queued); got != store.JobCanceled {
		t.Errorf("ждущее = %q, want %q", got, store.JobCanceled)
	}
	if err := m.Cancel(context.Background(), running); err != nil {
		t.Fatalf("Cancel(идущее): %v", err)
	}
	waitFor(t, "идущее отменилось", func() bool { return jobStatus(t, db, running) == store.JobCanceled })

	// Завершённое отменить нельзя — это не ошибка исполнения, а
	// бессмыслица, и говорить о ней надо прямо.
	if err := m.Cancel(context.Background(), running); err == nil {
		t.Error("Cancel завершённого прошёл без ошибки")
	}
}

// Перезапуск службы: то, что не умеет продолжаться, честно помечается
// прерванным, а умеющее — продолжается с сохранённого места.
func TestRecoverAfterRestart(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Так выглядит база после падения процесса: задание числится идущим,
	// а горутины, которая его вела, больше нет.
	plain, err := db.CreateJob(ctx, store.Job{Kind: "plain", Status: store.JobRunning, Queue: "host"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	resumable, err := db.CreateJob(ctx, store.Job{
		Kind: "resumable", Status: store.JobRunning, Queue: "other",
		Resume: `{"done":2}`,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	m := New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(m.Close)
	m.Register("plain", runnerFunc{fn: func(context.Context, *Context) error { return nil }})

	var sawDone int
	m.Register("resumable", runnerFunc{resumable: true, fn: func(_ context.Context, jc *Context) error {
		var st struct {
			Done int `json:"done"`
		}
		if err := jc.LoadResume(&st); err != nil {
			return err
		}
		sawDone = st.Done
		return nil
	}})

	if err := m.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got := jobStatus(t, db, plain); got != store.JobInterrupted {
		t.Errorf("непродолжаемое = %q, want %q", got, store.JobInterrupted)
	}
	waitFor(t, "продолжаемое доделалось", func() bool { return jobStatus(t, db, resumable) == store.JobSucceeded })
	if sawDone != 2 {
		t.Errorf("продолжение началось с %d, а сохранено было 2", sawDone)
	}
}

func TestWatchReceivesLines(t *testing.T) {
	m, _ := newTestManager(t)
	gate := make(chan struct{})
	m.Register("chatty", runnerFunc{fn: func(_ context.Context, jc *Context) error {
		<-gate
		jc.Logf("строка")
		return nil
	}})
	id, _ := m.Start(context.Background(), Spec{Kind: "chatty", Queue: "host"})

	ch, unwatch := m.Watch(id)
	defer unwatch()
	close(gate)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case u, ok := <-ch:
			if !ok {
				t.Fatal("канал закрылся, а строки журнала не пришло")
			}
			if u.Line != nil && u.Line.Text == "строка" {
				return
			}
		case <-deadline:
			t.Fatal("строка журнала не пришла наблюдателю")
		}
	}
}
