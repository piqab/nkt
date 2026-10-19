package hub

import (
	"context"
	"strings"
	"testing"
	"time"

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
