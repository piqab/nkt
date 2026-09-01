package monitor

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// demoBackfillKey marks the database as already seeded.
const demoBackfillKey = "demo:backfilled"

// BackfillDemoHistory seeds synthetic availability and usage history so that the
// weekly schedules are meaningful the first time the dashboard is opened.
//
// This runs only in fixtures mode: a canned host snapshot has no real history,
// and a set of empty charts explains nothing. Every value produced here is
// invented, which the API reports as `"simulated": true` and the UI states on
// screen. It runs at most once per database.
func BackfillDemoHistory(ctx context.Context, db *store.DB, days int) (int, error) {
	if done, _, err := db.KVGet(ctx, demoBackfillKey); err != nil {
		return 0, err
	} else if done != "" {
		return 0, nil
	}

	targets, err := db.ListTargets(ctx, false)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil // nothing to backfill yet; the next scan will populate targets
	}

	// A fixed seed keeps the demo reproducible across restarts.
	rng := rand.New(rand.NewSource(20260728))
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Duration(days) * 24 * time.Hour)

	var (
		probes  []store.ProbeResult
		samples []store.MetricSample
	)

	// Two targets are deliberately unreliable so outage detection has material.
	flaky := map[int64]float64{}
	for i, t := range targets {
		switch {
		case t.Host == "10.10.0.5" || t.Host == "10.10.0.6":
			flaky[t.ID] = 1.0 // unreachable remote backend
		case i%5 == 3:
			flaky[t.ID] = 0.06 // occasional blips
		}
	}

	for ts := start; ts.Before(now); ts = ts.Add(10 * time.Minute) {
		shapeBase := dailyShape(ts, "probe")
		for _, t := range targets {
			failRate := flaky[t.ID]
			// A nightly maintenance window on one service, to give the
			// availability heatmap a recognisable pattern.
			if ts.Hour() == 3 && t.Service == "haproxy" && ts.Weekday() == time.Sunday {
				failRate = 1.0
			}
			ok := rng.Float64() >= failRate
			latency := 3 + shapeBase*float64(6+hashRange(t.Key, 40)) + rng.Float64()*8
			pr := store.ProbeResult{
				TargetID:  t.ID,
				TS:        store.FormatTime(ts),
				OK:        ok,
				LatencyMS: math.Round(latency*100) / 100,
			}
			if ok {
				if t.Kind != "tcp" {
					pr.StatusCode = 200
				}
			} else {
				pr.LatencyMS = float64(rng.Intn(50)) + 5000
				pr.Error = "connect: connection refused"
			}
			probes = append(probes, pr)
		}

		// Hourly usage samples for firewall ports and containers.
		if ts.Minute() == 0 {
			hourShape := dailyShape(ts, "usage")
			for _, subject := range []string{"22/tcp", "80/tcp", "443/tcp", "8443/tcp"} {
				packets := math.Round(float64(200+hashRange(subject, 5000)) * hourShape * (0.8 + rng.Float64()*0.4))
				samples = append(samples,
					sample(store.FormatTime(ts), SourceIptables, subject, "packets", packets),
					sample(store.FormatTime(ts), SourceIptables, subject, "bytes", packets*float64(300+hashRange(subject, 800))),
				)
			}
			for _, name := range []string{"acme-app", "acme-api", "acme-redis", "acme-postgres", "acme-grafana"} {
				s := dailyShape(ts, name)
				samples = append(samples,
					sample(store.FormatTime(ts), SourceDocker, name, "cpu_pct",
						math.Round(s*float64(5+hashRange(name, 25))*10)/10),
					sample(store.FormatTime(ts), SourceDocker, name, "mem_bytes",
						float64(64<<20+hashRange(name, 400<<20))*(0.7+0.4*s)),
					sample(store.FormatTime(ts), SourceDocker, name, "net_rx_bytes",
						math.Round(s*float64(30_000+hashRange(name, 700_000)))),
					sample(store.FormatTime(ts), SourceDocker, name, "net_tx_bytes",
						math.Round(s*float64(20_000+hashRange(name+":tx", 500_000)))),
				)
			}
		}
	}

	if err := db.InsertProbeResults(ctx, probes); err != nil {
		return 0, fmt.Errorf("демо-история проб: %w", err)
	}
	if err := db.InsertMetrics(ctx, samples); err != nil {
		return 0, fmt.Errorf("демо-история метрик: %w", err)
	}
	if err := db.KVSet(ctx, demoBackfillKey, store.Now()); err != nil {
		return 0, err
	}
	return len(probes) + len(samples), nil
}
