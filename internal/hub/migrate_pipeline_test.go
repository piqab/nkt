package hub

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

type funcRunner func(ctx context.Context, jc *jobs.Context) error

func (f funcRunner) Run(ctx context.Context, jc *jobs.Context) error { return f(ctx, jc) }

// Перенос стека конвейера: хост в описании (новой версией) и учёт
// выкладки — на новый хост; откат — обратно; стек не конвейера и
// конвейер на группу не трогаются.
func TestMigratePipelineSwitch(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	alpha, err := db.CreateHost(ctx, "alpha", "10.0.0.1", 22, "root", store.HostAuthPassword, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	beta, _ := db.CreateHost(ctx, "beta", "10.0.0.2", 22, "root", store.HostAuthPassword, []byte("x"))
	content := "action: compose\ncompose:\n  file: c.yml\n  project: shop\n  hosts: [alpha]   # куда\n"
	pid, err := db.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: content, HookID: "hook-shop-123456", Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	srv.saveDeployed(ctx, pid, deployedStack{Project: "shop", Hosts: []deployedHost{{ID: alpha, Name: "alpha"}}})

	if pv := srv.migratePipelinePreview(ctx, alpha, beta, "shop"); pv == nil || pv.Name != "shop" || !pv.CanSwitch {
		t.Fatalf("preview: %+v", pv)
	}
	if pv := srv.migratePipelinePreview(ctx, alpha, beta, "other"); pv != nil {
		t.Fatalf("foreign stack: %+v", pv)
	}

	jdb, err := store.Open(filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer jdb.Close()
	jm := jobs.New(jdb, slog.New(slog.DiscardHandler))
	defer jm.Close()
	run := func(fn func(jc *jobs.Context)) {
		jm.Register("t.pipe", funcRunner(func(_ context.Context, jc *jobs.Context) error { fn(jc); return nil }))
		id, err := jm.Start(ctx, jobs.Spec{Kind: "t.pipe", Author: "admin", Steps: 1})
		if err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if j, _ := jdb.JobByID(ctx, id); j.Status == store.JobSucceeded {
				return
			}
		}
		t.Fatal("job did not finish")
	}

	var got *migratePipeline
	run(func(jc *jobs.Context) { got = srv.migratePipelineSwitch(ctx, jc, "admin", alpha, beta, "shop") })
	if got == nil || got.ID != pid {
		t.Fatalf("switch: %+v", got)
	}
	pl, _ := db.PipelineByID(ctx, pid)
	if !strings.Contains(pl.Content, "hosts: [beta]   # куда") {
		t.Fatalf("content: %q", pl.Content)
	}
	if d := srv.loadDeployed(ctx, pid); d == nil || d.Hosts[0].ID != beta {
		t.Fatalf("deployed: %+v", d)
	}
	if vs, _ := db.PipelineVersions(ctx, pid); len(vs) < 2 {
		t.Fatalf("versions: %d", len(vs))
	}

	run(func(jc *jobs.Context) {
		srv.migratePipelineRevert(ctx, jc, "admin", migrateRecord{From: alpha, To: beta, Pipeline: got})
	})
	pl, _ = db.PipelineByID(ctx, pid)
	if !strings.Contains(pl.Content, "hosts: [alpha]") || srv.loadDeployed(ctx, pid).Hosts[0].ID != alpha {
		t.Fatalf("revert: %q %+v", pl.Content, srv.loadDeployed(ctx, pid))
	}

	// Группа — не переключить.
	_ = db.UpdatePipelineContent(ctx, pid, "action: compose\ncompose:\n  file: c.yml\n  project: shop\n  group: prod\n", "admin", "")
	if pv := srv.migratePipelinePreview(ctx, alpha, beta, "shop"); pv == nil || pv.CanSwitch {
		t.Fatalf("group preview: %+v", pv)
	}
}
