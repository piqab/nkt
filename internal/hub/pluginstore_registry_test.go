package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/plugin"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// store с env @registry (1.17.15): секрет плагина — ключом registry
// конвейера; ключ с тем же адресом заменяется, его CA остаётся; плохой
// адрес или логин — отказ без записи.
func TestPluginStoreRegistry(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	id, err := db.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: "repo: https://example.com/x.git\nref: main\naction: compose\ncompose:\n  file: c.yaml\n  project: shop\n  hosts: [localhost]\n",
		HookID: "hook-reg-0001", HookSecret: secret, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal([]registryKey{{Host: "registry.example.com", User: "robot$old", Token: "old", CA: "PEM"}, {Host: "ghcr.io", User: "u", Token: "t"}})
	enc, _ := secretbox.Encrypt(srv.hub.key, old)
	_ = db.SetPipelineRegistries(ctx, id, enc)
	p, _ := db.PipelineByID(ctx, id)
	if _, err := srv.pluginStoreCheck(ctx, "admin", "shop", plugin.StoreRegistry); err != nil {
		t.Fatal(err)
	}
	line, err := srv.pluginStoreApply(ctx, "admin", "rgstr", p, plugin.StoreRegistry,
		&plugin.Store{Pipeline: "shop", Env: plugin.StoreRegistry, Value: "new-secret", Registry: "https://Registry.example.com/", User: "robot$ci"})
	if err != nil || !strings.Contains(line, "registry.example.com") || strings.Contains(line, "new-secret") {
		t.Fatalf("%q %v", line, err)
	}
	p, _ = db.PipelineByID(ctx, id)
	keys := srv.pipelineRegistries(p)
	if len(keys) != 2 || keys[1].Host != "registry.example.com" || keys[1].User != "robot$ci" || keys[1].Token != "new-secret" || keys[1].CA != "PEM" {
		t.Fatalf("keys: %+v", keys)
	}
	for _, bad := range []plugin.Store{
		{Pipeline: "shop", Env: plugin.StoreRegistry, Value: "x", Registry: "bad host/../", User: "u"},
		{Pipeline: "shop", Env: plugin.StoreRegistry, Value: "x", Registry: "registry.example.com", User: "a:b"},
		{Pipeline: "shop", Env: plugin.StoreRegistry, Value: "", Registry: "registry.example.com", User: "u"},
	} {
		if _, err := srv.pluginStoreApply(ctx, "admin", "rgstr", p, plugin.StoreRegistry, &bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
