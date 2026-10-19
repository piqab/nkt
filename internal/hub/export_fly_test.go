package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/store"
)

// Traffic Fly, состояние выкладок и учёт переносов переезжают в другой хаб:
// номера хостов и edge там другие — пересчитываются по именам и адресам;
// цель на хосте, которого нет, пропускается с сообщением.
func TestExportImportFlyDeployedMigrations(t *testing.T) {
	m1, db1 := newTestManager(t)
	m2, db2 := newTestManager(t)
	m1.cfg.DataDir, m2.cfg.DataDir = t.TempDir(), t.TempDir()
	ctx := context.Background()

	a1, _ := m1.AddHost(ctx, "alpha", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	b1, _ := m1.AddHost(ctx, "beta", "10.0.0.2", 22, "root", store.HostAuthPassword, "pw", false)
	gone, _ := m1.AddHost(ctx, "gone", "10.0.0.3", 22, "root", store.HostAuthPassword, "pw", false)
	// В хабе-получателе номера сдвинуты.
	_, _ = m2.AddHost(ctx, "other", "10.0.1.9", 22, "root", store.HostAuthPassword, "pw", false)
	_, _ = putEdgeDB(ctx, db2, EdgeSettings{Enabled: true, Address: "other.example.com:8444", Roles: []string{EdgeRoleHooks}})
	gw1, err := putEdgeDB(ctx, db1, EdgeSettings{Enabled: true, Address: "gw.example.com:8444", Roles: []string{EdgeRoleFly}})
	if err != nil {
		t.Fatal(err)
	}

	doc := FlyDoc{
		Revision: 7,
		Gateways: []FlyGateway{{EdgeID: gw1.ID, Index: 3, ListenPort: 51900}},
		Routes: []FlyRoute{{ID: "web", Label: "web", Gateway: gw1.ID, Kind: "tcp", Port: 443, Algorithm: "failover", Check: fly.Check{},
			Targets: []FlyTarget{
				{ID: "t1", HostID: a1, Kind: "container", Ref: "web-1", Port: 8080},
				{ID: "t2", HostID: b1, Kind: "container", Ref: "web-1", Port: 8080, Drain: true},
				{ID: "t3", Label: "lost", HostID: gone, Kind: "host", Port: 8080},
			}}},
	}
	doc.normalize()
	doc.Alloc.Hosts[flyHostKey(gw1.ID, a1)] = 7
	raw, _ := json.Marshal(doc)
	_ = db1.KVSet(ctx, flyDocKey, string(raw))
	rawV, _ := json.Marshal([]FlyVersion{{N: 7, TS: "2026-10-01T00:00:00Z", User: "admin", Doc: doc}})
	_ = db1.KVSet(ctx, flyVersionsKey, string(rawV))

	pid, err := db1.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: "kind: compose\n", HookID: "hook-shop-123456", Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	dep, _ := json.Marshal(deployedStack{Project: "shop", Hosts: []deployedHost{{ID: a1, Name: "alpha"}, {ID: gone, Name: "gone"}}})
	_ = db1.KVSet(ctx, deployedKey(pid), string(dep))
	left, _ := json.Marshal([]leftoverStack{{HostID: b1, Host: "beta", Project: "shop-old", At: "2026-10-02T00:00:00Z"}})
	_ = db1.KVSet(ctx, leftoversKey(pid), string(left))

	recs, _ := json.Marshal([]migrateRecord{
		{ID: "m1", TS: "2026-10-03T10:00:00Z", From: a1, FromName: "alpha", To: b1, ToName: "beta", Project: "db", Kind: "compose", Containers: []string{"db-1"}, State: "moved"},
		{ID: "m2", TS: "2026-10-04T10:00:00Z", From: a1, FromName: "alpha", To: gone, ToName: "gone", Project: "x", Kind: "compose", Containers: []string{"x-1"}, State: "removed"},
	})
	_ = db1.KVSet(ctx, migrationsKey, string(recs))

	export, err := m1.ExportHub(ctx, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if export.Pipelines[0].Deployed == nil || len(export.Pipelines[0].Leftovers) != 1 || len(export.Fly) == 0 || len(export.Migrations) == 0 {
		t.Fatalf("export: %+v fly=%d migrations=%d", export.Pipelines[0], len(export.Fly), len(export.Migrations))
	}
	rawE, _ := json.Marshal(export)
	decoded, err := store.DecodeHubExport(rawE)
	if err != nil {
		t.Fatal(err)
	}
	var keep []store.HostExport
	for _, h := range decoded.Hosts {
		if h.Name != "gone" {
			keep = append(keep, h)
		}
	}
	decoded.Hosts = keep

	plan, _ := m2.ImportPlan(ctx, decoded)
	got := map[string]int{}
	for _, ps := range plan {
		got[ps.Section] = len(ps.Items)
	}
	if got[store.SectionFly] != 1 || got[store.SectionMigrations] != 2 {
		t.Fatalf("plan: %v", got)
	}

	rep := m2.ImportHosts(ctx, decoded, nil)
	errs := strings.Join(rep.Errors, "\n")
	for _, want := range []string{"lost", "gone"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("errors lack %q: %v", want, rep.Errors)
		}
	}
	_, byName := m2.hostNames(ctx)
	a2, b2 := byName["alpha"], byName["beta"]
	if a2 == a1 {
		t.Fatalf("ids not shifted: %d", a2)
	}
	var gw2 int64
	for _, e := range loadEdges(ctx, db2) {
		if e.Address == "gw.example.com:8444" {
			gw2 = e.ID
		}
	}

	d2, vs2 := m2.loadFly(ctx)
	if len(d2.Routes) != 1 || d2.Routes[0].Gateway != gw2 || len(d2.Routes[0].Targets) != 2 {
		t.Fatalf("fly doc: %+v", d2)
	}
	if d2.Routes[0].Targets[0].HostID != a2 || d2.Routes[0].Targets[1].HostID != b2 || !d2.Routes[0].Targets[1].Drain {
		t.Fatalf("targets: %+v", d2.Routes[0].Targets)
	}
	if len(d2.Gateways) != 1 || d2.Gateways[0].Index != 3 || d2.Gateways[0].ListenPort != 51900 {
		t.Fatalf("gateways: %+v", d2.Gateways)
	}
	if d2.Alloc.Hosts[flyHostKey(gw2, a2)] != 7 {
		t.Fatalf("alloc: %+v", d2.Alloc)
	}
	if d2.Revision != 8 || len(vs2) != 2 {
		t.Fatalf("revision %d versions %d", d2.Revision, len(vs2))
	}

	pipes, _ := db2.ListPipelines(ctx)
	rawD, _, _ := db2.KVGet(ctx, deployedKey(pipes[0].ID))
	var d deployedStack
	_ = json.Unmarshal([]byte(rawD), &d)
	if d.Project != "shop" || len(d.Hosts) != 1 || d.Hosts[0].ID != a2 {
		t.Fatalf("deployed: %+v", d)
	}
	rawL, _, _ := db2.KVGet(ctx, leftoversKey(pipes[0].ID))
	var ls []leftoverStack
	_ = json.Unmarshal([]byte(rawL), &ls)
	if len(ls) != 1 || ls[0].HostID != b2 || ls[0].Project != "shop-old" {
		t.Fatalf("leftovers: %+v", ls)
	}

	ms := m2.loadMigrationsKV(ctx)
	if len(ms) != 1 || ms[0].ID != "m1" || ms[0].From != a2 || ms[0].To != b2 {
		t.Fatalf("migrations: %+v", ms)
	}

	// Повторно: всё уже есть — пропуск; «заменить» маршрут — замена.
	rep = m2.ImportHosts(ctx, decoded, nil)
	if c := rep.Count(store.SectionFly); c.Skipped != 1 || c.Added != 0 {
		t.Fatalf("fly again: %+v", c)
	}
	if c := rep.Count(store.SectionMigrations); c.Skipped != 1 {
		t.Fatalf("migrations again: %+v", c)
	}
	rep = m2.ImportHosts(ctx, decoded, store.ImportResolutions{store.SectionFly: {"web": "replace"}})
	if c := rep.Count(store.SectionFly); c.Replaced != 1 {
		t.Fatalf("fly replace: %+v", c)
	}
	if d3, _ := m2.loadFly(ctx); d3.Revision != 9 || len(d3.Routes) != 1 {
		t.Fatalf("after replace: %+v", d3)
	}
}
