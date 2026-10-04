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

// Стек с тем же именем на том же хосте числится и за другим конвейером —
// удаление первого его не трогает; уборка с хоста оставляет конвейер без
// этого хоста в описании.
func TestRemovalPlanSharedAndUnhost(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	mk := func(name, hosts string) store.Pipeline {
		id, err := db.CreatePipeline(ctx, store.Pipeline{Name: name, HookID: "h-" + name, Author: "admin",
			Content: "repo: https://example.com/a.git\nref: main\naction: compose\ncompose:\n  file: compose.yaml\n  project: shop\n  hosts: [" + hosts + "]\n"})
		if err != nil {
			t.Fatal(err)
		}
		p, _ := db.PipelineByID(ctx, id)
		return p
	}
	a := mk("a", "localhost")
	b := mk("b", "localhost")
	srv.saveDeployed(ctx, a.ID, deployedStack{Project: "shop", Hosts: []deployedHost{{ID: localHostID, Name: "localhost"}}})
	srv.saveDeployed(ctx, b.ID, deployedStack{Project: "shop", Hosts: []deployedHost{{ID: localHostID, Name: "localhost"}}})
	plan := srv.removalPlan(ctx, a)
	if len(plan) != 1 || plan[0].State != "shared" || plan[0].SharedWith != "b" {
		t.Fatalf("план: %+v", plan)
	}
	run := func(p PipelineRemoveParams) (store.Job, string) {
		jid, err := srv.jobs.Start(ctx, jobs.Spec{Kind: KindPipelineRemove, Queue: "deploy:t", Author: "admin", Steps: 3, Params: p})
		if err != nil {
			t.Fatal(err)
		}
		j := waitJobDone(t, db, jid)
		return j, jobLogText(t, ctx, db, jid)
	}
	j, log := run(PipelineRemoveParams{PipelineID: a.ID})
	if j.Status != store.JobSucceeded || !strings.Contains(log, "«b»") || strings.Contains(log, "удаление стека shop") {
		t.Fatalf("общий стек тронут: %+v\n%s", j, log)
	}

	// Уборка с хоста: localhost из описания уходит, конвейер — нет.
	offID, _ := db.CreateHost(ctx, "web-x", "203.0.113.7", 22, "root", "password", []byte("x"))
	c := mk("c", "localhost, web-x")
	srv.saveDeployed(ctx, c.ID, deployedStack{Project: "shop2", Hosts: []deployedHost{{ID: localHostID, Name: "localhost"}, {ID: offID, Name: "web-x"}}})
	j, log = run(PipelineRemoveParams{PipelineID: c.ID, Chosen: true, KeepPipeline: true, Items: []removalKey{{HostID: localHostID, Project: "shop2"}}})
	if j.Status != store.JobSucceeded {
		t.Fatalf("уборка: %+v\n%s", j, log)
	}
	got, err := db.PipelineByID(ctx, c.ID)
	if err != nil || !strings.Contains(got.Content, "hosts: [web-x]") {
		t.Fatalf("описание: %v %q", err, got.Content)
	}
	if d := srv.loadDeployed(ctx, c.ID); d == nil || len(d.Hosts) != 1 || d.Hosts[0].ID != offID {
		t.Fatalf("положение: %+v", d)
	}
}
