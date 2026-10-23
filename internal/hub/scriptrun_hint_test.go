package hub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// up упал на registry без ключа — к ошибке подсказка: стек конвейера —
// deploy КОНВЕЙЕР start, прочий — docker stack ПУТЬ start; прочие ошибки
// — как есть.
func TestRegistryHint(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	pid, _ := db.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: "action: compose\n", HookID: "hook-hint-1", Author: "admin"})
	srv.saveDeployed(ctx, pid, deployedStack{Project: "shop", Hosts: []deployedHost{{ID: 7, Name: "reg1"}}})
	r := &ScriptRunner{s: srv}
	h := store.Host{ID: 7, Name: "reg1"}
	unauth := errors.New("compose.up: Error response from daemon: error from registry: unauthorized")
	if err := r.registryHint(ctx, h, "shop", "/srv/compose/shop/compose.yaml", unauth); err == nil || !strings.Contains(err.Error(), "deploy shop start") {
		t.Fatalf("pipeline stack: %v", err)
	}
	if err := r.registryHint(ctx, h, "blog", "/srv/compose/blog/compose.yaml", unauth); err == nil || !strings.Contains(err.Error(), "docker stack /srv/compose/blog/compose.yaml start") {
		t.Fatalf("other stack: %v", err)
	}
	other := errors.New("port is already allocated")
	if err := r.registryHint(ctx, h, "shop", "/srv/compose/shop/compose.yaml", other); err != other {
		t.Fatalf("other error: %v", err)
	}
	// «Проверить» знает стеки выкладок.
	if refs := srv.refsNowCtx(ctx); refs.PipelineStacks["reg1|shop"] != "shop" {
		t.Fatalf("refs: %+v", refs.PipelineStacks)
	}
}
