package hub

import (
	"context"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/store"
)

func TestCertBucket(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	day := 24 * time.Hour
	for _, c := range []struct {
		notAfter string
		bucket   int
	}{
		{"", -1},
		{at(40 * day), -1},
		{at(14*day + time.Hour), 14},
		{at(8 * day), 14},
		{at(7*day + time.Hour), 7},
		{at(3 * day), 3},
		{at(5 * time.Hour), 3},
		{at(-time.Hour), 0},
		{at(-10 * day), 0},
	} {
		if b, _ := certBucket(c.notAfter, now); b != c.bucket {
			t.Errorf("%q: bucket %d, want %d", c.notAfter, b, c.bucket)
		}
	}
}

// nkt-edge: короткий обрыв не оповещается, долгий — «без связи», после
// него возврат — «снова на связи».
func TestEdgeAlerts(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	oldGrace, oldTick := edgeDownGrace, edgeAlertTick
	edgeDownGrace, edgeAlertTick = 150*time.Millisecond, 20*time.Millisecond
	defer func() { edgeDownGrace, edgeAlertTick = oldGrace, oldTick }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &edgeClient{}
	c.set(true, nil)
	go srv.edgeAlerts(ctx, EdgeSettings{ID: 1, Address: "gw.example.com:8444"}, c)
	count := func(kind string) int {
		evs, _ := db.ListHostEvents(context.Background(), 50)
		n := 0
		for _, e := range evs {
			if e.Kind == kind {
				n++
			}
		}
		return n
	}
	time.Sleep(60 * time.Millisecond)
	c.set(false, nil) // моргнул
	time.Sleep(60 * time.Millisecond)
	c.set(true, nil)
	time.Sleep(250 * time.Millisecond)
	if count(store.EventEdgeDown) != 0 || count(store.EventEdgeUp) != 0 {
		t.Fatalf("short blip alerted")
	}
	c.set(false, nil)
	time.Sleep(400 * time.Millisecond)
	if count(store.EventEdgeDown) != 1 {
		t.Fatalf("down: %d", count(store.EventEdgeDown))
	}
	c.set(true, nil)
	time.Sleep(100 * time.Millisecond)
	if count(store.EventEdgeUp) != 1 || count(store.EventEdgeDown) != 1 {
		t.Fatalf("up: %d down: %d", count(store.EventEdgeUp), count(store.EventEdgeDown))
	}
}
