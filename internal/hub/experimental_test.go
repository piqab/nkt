package hub

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// По умолчанию всё скрыто; кластеры — показаны, если уже заведены.
func TestExperimentalDefaults(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()
	if e := srv.experimental(ctx); e.Map || e.Clusters {
		t.Fatalf("defaults: %+v", e)
	}
	if _, err := db.CreateCluster(ctx, store.Cluster{Name: "k1"}); err != nil {
		t.Skipf("cluster fixture: %v", err)
	}
	if e := srv.experimental(ctx); !e.Clusters || e.Map {
		t.Fatalf("with a cluster: %+v", e)
	}
	_ = db.KVSet(ctx, experimentalKey, `{"map":true,"clusters":false}`)
	if e := srv.experimental(ctx); !e.Map || e.Clusters {
		t.Fatalf("saved: %+v", e)
	}
}
