package hub

import (
	"context"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

func TestHostRefsCleanup(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	gone, _ := db.CreateHost(ctx, "gone", "10.0.0.1", 22, "root", store.HostAuthPassword, []byte("x"))
	_, _ = db.CreateHost(ctx, "other", "10.0.0.2", 22, "root", store.HostAuthPassword, []byte("x"))
	host, _ := db.HostByID(ctx, gone)
	if _, err := db.SaveSite(ctx, store.Site{Domains: []string{"shop.example.com"}, HostID: gone, Proxy: "nginx", Upstream: "127.0.0.1:81"}); err != nil {
		t.Fatal(err)
	}
	two, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "two", Content: "repo: https://example.com/x.git\nref: main\naction: compose\ncompose:\n  file: c.yml\n  project: a\n  hosts: [gone, other]\n", HookID: "hook-two-1234567"})
	_, _ = db.CreatePipeline(ctx, store.Pipeline{Name: "solo", Content: "repo: https://example.com/x.git\nref: main\naction: compose\ncompose:\n  file: c.yml\n  project: b\n  hosts: [gone]\n", HookID: "hook-solo-123456"})

	refs := srv.hostRefs(ctx, host)
	if len(refs.Sites) != 1 || len(refs.Pipelines) != 2 {
		t.Fatalf("refs: %+v", refs)
	}
	problems := srv.cleanHostRefs(ctx, host, HostRefOptions{Sites: true, Pipelines: true})
	if len(problems) != 1 || !strings.Contains(problems[0], "solo") {
		t.Fatalf("problems: %v", problems)
	}
	if sites, _ := db.ListSites(ctx); len(sites) != 0 {
		t.Fatalf("sites left: %+v", sites)
	}
	pl, _ := db.PipelineByID(ctx, two)
	if !strings.Contains(pl.Content, "hosts: [other]") {
		t.Fatalf("pipeline: %q", pl.Content)
	}
}
