package monitor

import (
	"context"
	"time"

	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/ups"
)

// SourceUPS — ряды ИБП: subject — имя ИБП в NUT.
const SourceUPS = "ups"

// upsSamples — заряд, нагрузка, время на батарее, входное напряжение и
// «от батареи» (1/0) каждого ИБП NUT. Нет NUT — ничего.
func (m *MetricsCollector) upsSamples(ctx context.Context, ts string, _ time.Time) ([]store.MetricSample, error) {
	var out []store.MetricSample
	for _, u := range ups.Status(ctx, m.c) {
		if u.Error != "" {
			continue
		}
		if u.HasCharge {
			out = append(out, sample(ts, SourceUPS, u.Name, "charge_pct", u.ChargePct))
		}
		out = append(out, sample(ts, SourceUPS, u.Name, "load_pct", u.LoadPct))
		if u.HasRuntime {
			out = append(out, sample(ts, SourceUPS, u.Name, "runtime_s", float64(u.RuntimeS)))
		}
		out = append(out, sample(ts, SourceUPS, u.Name, "input_v", u.InputV))
		onBattery := 0.0
		if u.OnBattery {
			onBattery = 1
		}
		out = append(out, sample(ts, SourceUPS, u.Name, "on_battery", onBattery))
	}
	return out, nil
}
