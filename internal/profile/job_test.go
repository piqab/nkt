package profile

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// fakeApplier запоминает, что применяли, и умеет падать на заданном шаге.
type fakeApplier struct {
	applied  []string
	failAt   int
	attempts int
}

func (f *fakeApplier) Apply(_ context.Context, c Change) (string, error) {
	f.attempts++
	if f.failAt > 0 && f.attempts == f.failAt {
		return "", errors.New("не вышло")
	}
	f.applied = append(f.applied, c.Action+":"+c.Target)
	return "сделано", nil
}

func newJobManager(t *testing.T) (*jobs.Manager, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(m.Close)
	return m, db
}

func waitStatus(t *testing.T, db *store.DB, id int64, want string) store.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := db.JobByID(context.Background(), id)
		if err == nil && job.Status == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := db.JobByID(context.Background(), id)
	t.Fatalf("задание в состоянии %q, ожидалось %q (ошибка: %s)", job.Status, want, job.Error)
	return store.Job{}
}

func TestApplyJobRunsEveryChange(t *testing.T) {
	m, db := newJobManager(t)
	applier := &fakeApplier{}
	m.Register(KindApply, NewApplyRunner(func(string) Applier { return applier }))

	id, err := m.Start(context.Background(), jobs.Spec{
		Kind: KindApply, Title: "профиль web", Queue: "host",
		Params: ApplyParams{Name: "web", Changes: []Change{
			{Action: ActionInstallPackage, Target: "nginx"},
			{Action: ActionEnableService, Target: "nginx"},
		}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	job := waitStatus(t, db, id, store.JobSucceeded)
	if job.Step != 2 || job.Steps != 2 {
		t.Errorf("шаги = %d/%d", job.Step, job.Steps)
	}
	if len(applier.applied) != 2 {
		t.Errorf("применено %q", applier.applied)
	}
}

// Ошибка останавливает применение: следующие пункты обычно зависят от
// предыдущих, и список одинаковых отказов хуже одного понятного.
func TestApplyJobStopsAtFirstError(t *testing.T) {
	m, db := newJobManager(t)
	applier := &fakeApplier{failAt: 2}
	m.Register(KindApply, NewApplyRunner(func(string) Applier { return applier }))

	id, _ := m.Start(context.Background(), jobs.Spec{
		Kind: KindApply, Queue: "host",
		Params: ApplyParams{Changes: []Change{
			{Action: ActionInstallPackage, Target: "nginx"},
			{Action: ActionInstallPackage, Target: "fail2ban"},
			{Action: ActionInstallPackage, Target: "btop"},
		}},
	})
	job := waitStatus(t, db, id, store.JobFailed)
	if len(applier.applied) != 1 {
		t.Errorf("после ошибки применено %q, ожидался только первый пункт", applier.applied)
	}
	if job.Error == "" {
		t.Error("причина не записана")
	}
}

// Перезапуск службы посреди применения: продолжение начинается со
// следующего пункта, а не с начала — иначе половина плана применилась бы
// дважды.
func TestApplyJobResumesFromSavedPoint(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Так выглядит база после падения процесса на середине: два пункта из
	// трёх сделаны.
	params := `{"changes":[{"action":"package.install","target":"a"},` +
		`{"action":"package.install","target":"b"},{"action":"package.install","target":"c"}]}`
	id, err := db.CreateJob(ctx, store.Job{
		Kind: KindApply, Status: store.JobRunning, Queue: "host",
		Params: params, Resume: `{"done":2}`,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	m := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(m.Close)
	applier := &fakeApplier{}
	m.Register(KindApply, NewApplyRunner(func(string) Applier { return applier }))
	if err := m.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	waitStatus(t, db, id, store.JobSucceeded)

	if len(applier.applied) != 1 || applier.applied[0] != "package.install:c" {
		t.Errorf("после продолжения применено %q, ожидался только последний пункт", applier.applied)
	}
}
