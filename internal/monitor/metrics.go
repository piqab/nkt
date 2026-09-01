package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/parse"
	"github.com/piqab/nkt/internal/store"
)

// Metric source names.
const (
	SourceIptables = "iptables"
	SourceDocker   = "docker"
)

// MetricsCollector samples resource usage: firewall counters and container stats.
type MetricsCollector struct {
	db  *store.DB
	c   collect.Collector
	cfg *config.Config
}

// NewMetricsCollector builds the usage collector.
func NewMetricsCollector(db *store.DB, c collect.Collector, cfg *config.Config) *MetricsCollector {
	return &MetricsCollector{db: db, c: c, cfg: cfg}
}

// RunOnce takes one sample of every usage series and stores it.
func (m *MetricsCollector) RunOnce(ctx context.Context) (int, error) {
	now := time.Now()
	ts := store.FormatTime(now)
	var samples []store.MetricSample

	fw, err := m.firewallSamples(ctx, ts, now)
	if err != nil {
		return 0, err
	}
	samples = append(samples, fw...)

	docker, err := m.dockerSamples(ctx, ts, now)
	if err != nil {
		return 0, err
	}
	samples = append(samples, docker...)

	if err := m.db.InsertMetrics(ctx, samples); err != nil {
		return 0, fmt.Errorf("сохранение метрик: %w", err)
	}
	return len(samples), nil
}

// Simulated reports whether the collected numbers are synthetic. In fixtures
// mode the snapshot never changes, so counters would produce a flat zero line;
// the collector shapes plausible values instead and says so.
func (m *MetricsCollector) Simulated() bool { return m.cfg.IsFixtures() }

// --------------------------------------------------------------- firewall

func (m *MetricsCollector) firewallSamples(ctx context.Context, ts string, now time.Time) ([]store.MetricSample, error) {
	res := parse.Firewall(ctx, m.c)
	if !res.Status.Available {
		return nil, nil
	}

	var out []store.MetricSample
	for _, r := range res.State.Rules {
		if r.Backend == "ufw" || r.Backend == "ufw6" {
			continue // ufw rules are a view over iptables; counting them twice would double the totals
		}
		subject := firewallSubject(r)
		if subject == "" {
			continue
		}
		key := "ipt:" + firewallCounterKey(r)

		packets, bytes := float64(r.Packets), float64(r.Bytes)
		if m.Simulated() {
			packets, bytes = m.simulateTraffic(subject, now)
			out = append(out,
				store.MetricSample{TS: ts, Source: SourceIptables, Subject: subject, Metric: "packets", Value: packets},
				store.MetricSample{TS: ts, Source: SourceIptables, Subject: subject, Metric: "bytes", Value: bytes},
			)
			continue
		}

		if delta, ok, err := m.db.CounterDelta(ctx, key+":packets", packets); err != nil {
			return nil, err
		} else if ok {
			out = append(out, store.MetricSample{
				TS: ts, Source: SourceIptables, Subject: subject, Metric: "packets", Value: delta,
			})
		}
		if delta, ok, err := m.db.CounterDelta(ctx, key+":bytes", bytes); err != nil {
			return nil, err
		} else if ok {
			out = append(out, store.MetricSample{
				TS: ts, Source: SourceIptables, Subject: subject, Metric: "bytes", Value: delta,
			})
		}
	}
	return out, nil
}

// firewallSubject names a rule by what it lets through, which is what an
// operator wants on a chart.
func firewallSubject(r model.FirewallRule) string {
	if len(r.Ports) == 0 {
		return ""
	}
	proto := r.Protocol
	if proto == "" {
		proto = "tcp"
	}
	if len(r.Ports) == 1 {
		return fmt.Sprintf("%d/%s", r.Ports[0], proto)
	}
	return fmt.Sprintf("%s/%s", r.PortSpec, proto)
}

// firewallCounterKey is stable across rule reordering, so a rule that moves in
// the chain keeps its counter history.
func firewallCounterKey(r model.FirewallRule) string {
	return strings.Join([]string{
		r.Backend, r.Table, r.Chain, r.Action, r.Protocol, r.PortSpec, r.Source, r.DNATTo,
	}, "|")
}

// --------------------------------------------------------------- containers

type dockerStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			Cache uint64 `json:"cache"`
		} `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

func (m *MetricsCollector) dockerSamples(ctx context.Context, ts string, now time.Time) ([]store.MetricSample, error) {
	raw, code, err := m.c.DockerAPI(ctx, "GET", "/containers/json", nil)
	if err != nil || code != 200 {
		return nil, nil // docker is optional; its absence is reported by the scan, not here
	}
	var list []struct {
		Names []string `json:"Names"`
		State string   `json:"State"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil
	}

	var out []store.MetricSample
	for _, item := range list {
		if item.State != "running" || len(item.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(item.Names[0], "/")

		statsRaw, statsCode, err := m.c.DockerAPI(ctx, "GET",
			"/containers/"+name+"/stats?stream=false&one-shot=true", nil)
		if err != nil || statsCode != 200 {
			continue
		}
		var st dockerStats
		if err := json.Unmarshal(statsRaw, &st); err != nil {
			continue
		}

		cpuPct := 0.0
		cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage) - float64(st.PreCPUStats.CPUUsage.TotalUsage)
		sysDelta := float64(st.CPUStats.SystemUsage) - float64(st.PreCPUStats.SystemUsage)
		cpus := st.CPUStats.OnlineCPUs
		if cpus == 0 {
			cpus = 1
		}
		if cpuDelta > 0 && sysDelta > 0 {
			cpuPct = cpuDelta / sysDelta * float64(cpus) * 100
		}

		memUsage := float64(st.MemoryStats.Usage)
		if st.MemoryStats.Stats.Cache < st.MemoryStats.Usage {
			memUsage -= float64(st.MemoryStats.Stats.Cache)
		}

		var rx, tx float64
		for _, n := range st.Networks {
			rx += float64(n.RxBytes)
			tx += float64(n.TxBytes)
		}

		if m.Simulated() {
			shape := dailyShape(now, name)
			cpuPct = math.Round(shape*float64(6+hashRange(name, 20))*10) / 10
			memUsage = memUsage * (0.6 + 0.5*shape)
			out = append(out,
				sample(ts, SourceDocker, name, "net_rx_bytes", shape*float64(40_000+hashRange(name, 900_000))),
				sample(ts, SourceDocker, name, "net_tx_bytes", shape*float64(25_000+hashRange(name, 600_000))),
			)
		} else {
			if delta, ok, err := m.db.CounterDelta(ctx, "docker:"+name+":rx", rx); err != nil {
				return nil, err
			} else if ok {
				out = append(out, sample(ts, SourceDocker, name, "net_rx_bytes", delta))
			}
			if delta, ok, err := m.db.CounterDelta(ctx, "docker:"+name+":tx", tx); err != nil {
				return nil, err
			} else if ok {
				out = append(out, sample(ts, SourceDocker, name, "net_tx_bytes", delta))
			}
		}

		out = append(out,
			sample(ts, SourceDocker, name, "cpu_pct", cpuPct),
			sample(ts, SourceDocker, name, "mem_bytes", memUsage),
		)
	}
	return out, nil
}

func sample(ts, source, subject, metric string, value float64) store.MetricSample {
	return store.MetricSample{TS: ts, Source: source, Subject: subject, Metric: metric, Value: value}
}

// --------------------------------------------------------------- simulation

// simulateTraffic produces a plausible per-interval firewall counter for the
// snapshot mode, where the real counters are frozen.
func (m *MetricsCollector) simulateTraffic(subject string, now time.Time) (packets, bytes float64) {
	shape := dailyShape(now, subject)
	base := float64(50 + hashRange(subject, 4000))
	packets = math.Round(base * shape)
	bytes = math.Round(packets * float64(400+hashRange(subject+":size", 900)))
	return packets, bytes
}

// dailyShape returns a 0.05…1 multiplier following a working-day curve, with a
// per-subject phase offset so different series do not move in lockstep.
func dailyShape(now time.Time, subject string) float64 {
	hour := float64(now.Hour()) + float64(now.Minute())/60
	phase := float64(hashRange(subject, 40)) / 20 // 0…2 hours of offset
	// Peak around 14:00, trough around 04:00.
	wave := math.Cos((hour - 14 + phase) / 24 * 2 * math.Pi)
	shape := 0.5 + 0.45*wave
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		shape *= 0.45
	}
	if shape < 0.05 {
		shape = 0.05
	}
	return shape
}

func hashRange(s string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	if n <= 0 {
		return 0
	}
	return int(h.Sum32() % uint32(n))
}
