package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

type cleanupTestRunner struct {
	fn func(context.Context, *jobs.Context) error
}

func (r cleanupTestRunner) Run(ctx context.Context, jc *jobs.Context) error { return r.fn(ctx, jc) }

// Стек переехал: с доступного прежнего хоста он убирается, с
// недоступного — остаётся и виден у конвейера; «Забыть» снимает пометку.
func TestCleanupMovedStacks(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	offID, err := db.CreateHost(ctx, "old-host", "203.0.113.9", 22, "root", "password", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	plID, err := db.CreatePipeline(ctx, store.Pipeline{Name: "app", Content: "repo: https://example.com/a.git\nref: main\naction: compose\ncompose:\n  file: c.yml\n  project: app\n  hosts: [web-9]\n"})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := db.PipelineByID(ctx, plID)
	prev := &deployedStack{Project: "app", Hosts: []deployedHost{{ID: localHostID, Name: "localhost"}, {ID: offID, Name: "old-host"}}}
	srv.jobs.Register("cleanup-test", cleanupTestRunner{fn: func(ctx context.Context, jc *jobs.Context) error {
		srv.cleanupMovedStacks(ctx, jc, "admin", pl, "app", prev, []targetHost{{ID: 12345, Name: "web-9"}})
		return nil
	}})
	id, err := srv.jobs.Start(ctx, jobs.Spec{Kind: "cleanup-test", Title: "t", Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	var job store.Job
	for i := 0; i < 300; i++ {
		job, _ = db.JobByID(ctx, id)
		if job.Done() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != store.JobSucceeded {
		t.Fatalf("задание: %s %s", job.Status, job.Error)
	}
	left := srv.loadLeftovers(ctx, plID)
	if len(left) != 1 || left[0].HostID != offID || left[0].Project != "app" {
		t.Fatalf("оставшиеся: %+v", left)
	}
	lines, _ := db.JobLog(ctx, id, 0, 100)
	var log []string
	for _, l := range lines {
		log = append(log, l.Text)
	}
	if !strings.Contains(strings.Join(log, "\n"), "localhost") {
		t.Errorf("с локального хоста стек не убирался: %v", log)
	}

	// «Забыть» — пометка снимается, на хосте ничего не делается.
	req := httptest.NewRequest(http.MethodPost, "/hub/pipelines/"+itoa64(plID)+"/leftovers/remove",
		strings.NewReader(`{"host_id":`+itoa64(offID)+`,"project":"app","forget":true}`)).
		WithContext(auth.WithUser(ctx, store.User{Username: "admin", Role: store.RoleAdmin}))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", itoa64(plID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()
	srv.handlePipelineLeftoverRemove(w, req)
	if w.Code != http.StatusOK || len(srv.loadLeftovers(ctx, plID)) != 0 {
		t.Fatalf("забыть: %d %s, осталось %+v", w.Code, w.Body.String(), srv.loadLeftovers(ctx, plID))
	}
}
