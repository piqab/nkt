package hub

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/api"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

func TestMigrateProblems(t *testing.T) {
	p := migratePlanJSON{Project: "shop", Arch: "amd64", Refuse: []string{"migrate.refuseHostNet:web"}}
	p.Volumes = append(p.Volumes, struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}{"shop_db", 3 << 30})
	c := migrateCheckJSON{Docker: true, Arch: "arm64", BusyPorts: []string{"0.0.0.0:80->80/tcp"}, ProjectExists: true, FreeBytes: 4 << 30}
	var keys []string
	for _, pr := range migrateProblems(p, c) {
		keys = append(keys, pr[0])
	}
	got := strings.Join(keys, ",")
	for _, want := range []string{"migrate.refuseHostNet", "migrate.archMismatch", "migrate.portsBusy", "migrate.projectExists", "migrate.noSpace"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	if len(migrateProblems(migratePlanJSON{Arch: "amd64"}, migrateCheckJSON{Docker: true, Arch: "amd64", FreeBytes: 100 << 30})) != 0 {
		t.Fatal("clean plan has problems")
	}
}

// Напоминание: старый стек остановлен неделю — одно событие, второй раз
// не повторяется; откаченный — не напоминает.
func TestMigrationReminder(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	old := time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	srv.updateMigrations(ctx, func(l []migrateRecord) []migrateRecord {
		return append(l,
			migrateRecord{ID: "a", TS: old, From: 5, FromName: "web-1", ToName: "web-2", Project: "shop", State: "moved"},
			migrateRecord{ID: "b", TS: old, From: 5, FromName: "web-1", ToName: "web-2", Project: "blog", State: "rolled_back"},
			migrateRecord{ID: "c", TS: store.Now(), From: 5, FromName: "web-1", ToName: "web-2", Project: "new", State: "moved"})
	})
	srv.remindMigrations(ctx, time.Now())
	srv.remindMigrations(ctx, time.Now())
	evs, _ := db.ListHostEvents(ctx, 50)
	n := 0
	for _, e := range evs {
		if e.Kind == store.EventMigrationLeftover {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("reminders: %d", n)
	}
}

type waitInnerRunner struct {
	queue string
	jm    *jobs.Manager
	db    *store.DB
}

func (r waitInnerRunner) Run(ctx context.Context, jc *jobs.Context) error {
	id, err := r.jm.Start(ctx, jobs.Spec{Kind: "t.inner", Queue: r.queue, Author: "admin", Steps: 1})
	if err != nil {
		return err
	}
	for {
		j, _ := r.db.JobByID(ctx, id)
		if j.Status == store.JobSucceeded {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

type noopRunner struct{}

func (noopRunner) Run(context.Context, *jobs.Context) error { return nil }

// На машине хаба менеджер заданий общий: задание переноса хаба ждёт шаг
// на «хосте» — в своей очереди он идёт, в той же очереди висел бы вечно
// (ошибка 1.13.44 при переносе на машину хаба).
func TestMigrateQueuesNoDeadlock(t *testing.T) {
	if migrateHubQueue == api.MigrateHostQueue {
		t.Fatal("hub and host migration queues are the same")
	}
	run := func(outerQueue string) bool {
		db, err := store.Open(filepath.Join(t.TempDir(), "j.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		jm := jobs.New(db, slog.New(slog.DiscardHandler))
		defer jm.Close()
		jm.Register("t.outer", waitInnerRunner{queue: api.MigrateHostQueue, jm: jm, db: db})
		jm.Register("t.inner", noopRunner{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		id, err := jm.Start(ctx, jobs.Spec{Kind: "t.outer", Queue: outerQueue, Author: "admin", Steps: 1})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if j, _ := db.JobByID(ctx, id); j.Status == store.JobSucceeded {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = jm.Cancel(context.Background(), id)
		return false
	}
	if !run(migrateHubQueue) {
		t.Fatal("hub migration job blocked by its own host step")
	}
	// Проверка самой проверки: в одной очереди — блокировка.
	if run(api.MigrateHostQueue) {
		t.Fatal("same-queue case did not block — the test does not catch the bug")
	}
}
