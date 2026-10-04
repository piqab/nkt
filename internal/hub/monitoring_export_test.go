package hub

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// История «Мониторинга» в экспорте и импорте: по имени хоста; есть уже —
// по умолчанию пропуск, «заменить» дополняет (имеющиеся часы не трогает).
func TestMonitoringExportImport(t *testing.T) {
	ctx := context.Background()
	src, srcDB := newTestManager(t)
	_ = src
	id, err := srcDB.CreateHost(ctx, "web-1", "10.0.0.1", 22, "root", store.HostAuthPassword, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	rows := []store.MonRow{
		{Source: "host", Subject: "host", Metric: "cpu_pct", At: "2026-10-01T05", Avg: 10, Max: 20, Sum: 600},
		{Source: "host", Subject: "host", Metric: "cpu_pct", At: "2026-10-01T06", Avg: 30, Max: 40, Sum: 1800},
	}
	_ = srcDB.MonUpsertHourly(ctx, id, rows, false)
	_ = srcDB.MonRollupDaily(ctx, id, []string{"2026-10-01"})
	_ = srcDB.MonSetTargets(ctx, id, []store.MonTarget{{Key: "k", Label: "L", Kind: "tcp", Host: "h", Port: 1, Enabled: true}})
	exp, err := srcDB.ExportMonitoring(ctx)
	if err != nil || len(exp) != 1 || len(exp[0].Hourly) != 2 || len(exp[0].Daily) != 1 || exp[0].Host != "web-1" {
		t.Fatalf("export: %+v %v", exp, err)
	}

	dst, dstDB := newTestManager(t)
	did, _ := dstDB.CreateHost(ctx, "web-1", "10.0.0.1", 22, "root", store.HostAuthPassword, []byte("x"))
	plan, err := dst.ImportPlan(ctx, store.HubExport{Monitoring: exp})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plan {
		if s.Section == store.SectionMonitoring && (len(s.Items) != 1 || s.Items[0].Conflict) {
			t.Fatalf("plan: %+v", s)
		}
	}
	var rep store.ImportReport
	dst.importMonitoring(ctx, exp, nil, &rep)
	if c := rep.Sections[store.SectionMonitoring]; c == nil || c.Added != 1 {
		t.Fatalf("add: %+v", rep)
	}
	got, _ := dstDB.MonSeries(ctx, store.MonSeriesQuery{HostID: did, From: "0", To: "9"})
	if len(got) != 2 {
		t.Fatalf("rows: %+v", got)
	}
	// Своя история изменила час — импорт без выбора не трогает, с
	// «заменить» дополняет, но свой час остаётся.
	_ = dstDB.MonUpsertHourly(ctx, did, []store.MonRow{{Source: "host", Subject: "host", Metric: "cpu_pct", At: "2026-10-01T05", Avg: 99, Max: 99, Sum: 99}}, false)
	exp[0].Hourly = append(exp[0].Hourly, store.MonRow{Source: "host", Subject: "host", Metric: "cpu_pct", At: "2026-10-01T07", Avg: 5, Max: 5, Sum: 5})
	rep = store.ImportReport{}
	dst.importMonitoring(ctx, exp, nil, &rep)
	if rep.Sections[store.SectionMonitoring].Skipped != 1 {
		t.Fatalf("skip: %+v", rep)
	}
	rep = store.ImportReport{}
	dst.importMonitoring(ctx, exp, store.ImportResolutions{store.SectionMonitoring: {"web-1": "replace"}}, &rep)
	got, _ = dstDB.MonSeries(ctx, store.MonSeriesQuery{HostID: did, From: "0", To: "9"})
	if len(got) != 3 || got[0].Avg != 99 {
		t.Fatalf("merge: %+v", got)
	}
	// Хоста нет — пропуск с ошибкой.
	rep = store.ImportReport{}
	dst.importMonitoring(ctx, []store.MonHostExport{{Host: "nope", Hourly: rows}}, nil, &rep)
	if len(rep.Errors) != 1 {
		t.Fatalf("missing host: %+v", rep)
	}
}
