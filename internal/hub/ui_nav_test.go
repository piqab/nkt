package hub

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// Раскладка меню: порядок и скрытие с историей; у меню хаба скрывать
// нельзя, «Обзор» хоста скрыть нельзя; раскладки едут в экспорте хаба.
func TestNavLayout(t *testing.T) {
	m1, _ := newTestManager(t)
	m2, db2 := newTestManager(t)
	m1.cfg.DataDir, m2.cfg.DataDir = t.TempDir(), t.TempDir()
	ctx := context.Background()

	if l, hist := m1.NavLayoutFor(ctx, "host"); len(l.Order) != 0 || len(hist) != 0 {
		t.Fatalf("default: %+v %+v", l, hist)
	}
	if _, err := m1.SaveNavLayout(ctx, "host", "admin", NavLayout{Order: []string{"/firewall", "/", "/firewall"}, Hidden: []string{"/terminal"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m1.SaveNavLayout(ctx, "host", "admin", NavLayout{Order: []string{"/", "/firewall"}}); err != nil {
		t.Fatal(err)
	}
	l, hist := m1.NavLayoutFor(ctx, "host")
	if !slices.Equal(l.Order, []string{"/", "/firewall"}) || len(hist) != 2 || !slices.Equal(hist[0].Order, []string{"/firewall", "/"}) || !slices.Equal(hist[0].Hidden, []string{"/terminal"}) {
		t.Fatalf("layout %+v history %+v", l, hist)
	}
	for name, bad := range map[string]struct {
		kind string
		l    NavLayout
	}{
		"hub hide":      {"hub", NavLayout{Order: []string{"hosts"}, Hidden: []string{"jobs"}}},
		"overview hide": {"host", NavLayout{Hidden: []string{"/"}}},
		"bad key":       {"host", NavLayout{Order: []string{"/x y"}}},
		"bad kind":      {"vm", NavLayout{}},
	} {
		if _, err := m1.SaveNavLayout(ctx, bad.kind, "admin", bad.l); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := m1.SaveNavLayout(ctx, "hub", "admin", NavLayout{Order: []string{"deploy", "hosts"}}); err != nil {
		t.Fatal(err)
	}

	export, err := m1.ExportHub(ctx, false, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(export)
	decoded, _ := store.DecodeHubExport(raw)
	m2.ImportHosts(ctx, decoded, nil)
	if v, ok, _ := db2.KVGet(ctx, "ui.nav.hub"); !ok || v != `{"order":["deploy","hosts"],"hidden":[]}` {
		t.Fatalf("hub layout not imported: %q", v)
	}
	if l, hist := m2.NavLayoutFor(ctx, "host"); !slices.Equal(l.Order, []string{"/", "/firewall"}) || len(hist) != 2 {
		t.Fatalf("host layout not imported: %+v %+v", l, hist)
	}
}
