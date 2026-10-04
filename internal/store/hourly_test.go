package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMetricAndProbeHourly(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	err = db.InsertMetrics(ctx, []MetricSample{
		{TS: "2026-10-04T05:01:00Z", Source: "host", Subject: "host", Metric: "cpu_pct", Value: 10},
		{TS: "2026-10-04T05:31:00Z", Source: "host", Subject: "host", Metric: "cpu_pct", Value: 30},
		{TS: "2026-10-04T06:01:00Z", Source: "host", Subject: "host", Metric: "cpu_pct", Value: 50},
		{TS: "2026-10-04T05:10:00Z", Source: "iptables", Subject: "22/tcp", Metric: "bytes", Value: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.MetricHourly(ctx, "2026-10-04T05:00:00Z", "2026-10-04T06:00:00Z", []string{"host"})
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if g := got[0]; g.Hour != "2026-10-04T05" || g.Avg != 20 || g.Max != 30 || g.Sum != 40 || g.N != 2 {
		t.Fatalf("hour: %+v", g)
	}
	id, err := db.UpsertTarget(ctx, Target{Key: "k", Label: "l", Kind: "tcp", Host: "h", Port: 1, Source: "manual", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.InsertProbeResults(ctx, []ProbeResult{
		{TargetID: id, TS: "2026-10-04T05:02:00Z", OK: true, LatencyMS: 10},
		{TargetID: id, TS: "2026-10-04T05:03:00Z", OK: false, LatencyMS: 5000},
		{TargetID: id, TS: "2026-10-04T05:04:00Z", OK: true, LatencyMS: 30},
	})
	hp, err := db.ProbeHourly(ctx, "2026-10-04T05:00:00Z", "2026-10-04T06:00:00Z")
	if err != nil || len(hp) != 1 || hp[0].OK != 2 || hp[0].Total != 3 || hp[0].LatencyMS != 20 {
		t.Fatalf("probes: %+v %v", hp, err)
	}
}
