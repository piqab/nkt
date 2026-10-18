package hub

import (
	"context"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Карта: у машины хаба — контейнеры фикстур; конвейер compose — со своим
// стеком и хостами.
func TestHostsMap(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	content := "repo: https://github.com/org/app.git\nref: main\naction: compose\ncompose:\n  file: d.yml\n  project: shop\n  hosts: [localhost]\n"
	if _, err := db.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: content, HookID: "hook-map-1", HookSecret: secret, Author: "admin"}); err != nil {
		t.Fatal(err)
	}
	code, out := callJSON(t, srv.handleHostsMap, "GET", "", nil)
	if code != 200 {
		t.Fatalf("map: %d %v", code, out)
	}
	hosts, _ := out["hosts"].(map[string]any)
	local, _ := hosts["-1"].(map[string]any)
	items, _ := local["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("no local items: %v", out["hosts"])
	}
	first, _ := items[0].(map[string]any)
	if first["kind"] == nil || first["name"] == nil {
		t.Fatalf("item: %v", first)
	}
	pipes, _ := out["pipelines"].([]any)
	if len(pipes) != 1 || !strings.Contains(string(mustJSON(pipes[0])), `"project":"shop"`) || !strings.Contains(string(mustJSON(pipes[0])), `"hosts":[-1]`) {
		t.Fatalf("pipelines: %s", mustJSON(pipes))
	}
	if _, ok := out["routes"].([]any); !ok {
		t.Fatalf("routes: %v", out["routes"])
	}
}
