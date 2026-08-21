package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/store"
)

// TestUsageTotal covers the reference-ceiling figure the frontend draws as
// a dashed line on the CPU/memory usage charts (see Usage.tsx) — computed
// from the latest scan's HostCapacity (fixtures/host/proc/{meminfo,cpuinfo},
// 4 cores / ~15.6 GiB, see parse.HostCapacityTest), not queried fresh.
func TestUsageTotal(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Mode: config.ModeFixtures, FixturesRoot: root}
	c := collect.NewFixtures(root)
	scanner := inventory.New(cfg, c, nil)
	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	s := &Server{cfg: cfg, scanner: scanner}

	t.Run("cpu_pct: cores * 100", func(t *testing.T) {
		total := s.usageTotal(store.MetricQuery{Source: "docker", Metric: "cpu_pct"})
		if total == nil {
			t.Fatal("usageTotal() = nil, want a value for docker/cpu_pct")
		}
		if want := float64(4 * 100); *total != want {
			t.Errorf("usageTotal() = %v, want %v", *total, want)
		}
	})

	t.Run("mem_bytes: total installed memory", func(t *testing.T) {
		total := s.usageTotal(store.MetricQuery{Source: "docker", Metric: "mem_bytes"})
		if total == nil {
			t.Fatal("usageTotal() = nil, want a value for docker/mem_bytes")
		}
		if want := float64(16386420 * 1024); *total != want {
			t.Errorf("usageTotal() = %v, want %v", *total, want)
		}
	})

	t.Run("a metric with no natural ceiling: nil", func(t *testing.T) {
		if total := s.usageTotal(store.MetricQuery{Source: "nginx_log", Metric: "requests"}); total != nil {
			t.Errorf("usageTotal() = %v, want nil for nginx_log/requests", *total)
		}
	})

	t.Run("net_rx_bytes on docker source: nil (no meaningful host-wide network ceiling)", func(t *testing.T) {
		if total := s.usageTotal(store.MetricQuery{Source: "docker", Metric: "net_rx_bytes"}); total != nil {
			t.Errorf("usageTotal() = %v, want nil for docker/net_rx_bytes", *total)
		}
	})

	t.Run("no scan yet: nil, does not panic", func(t *testing.T) {
		empty := &Server{cfg: cfg, scanner: inventory.New(cfg, c, nil)}
		if total := empty.usageTotal(store.MetricQuery{Source: "docker", Metric: "cpu_pct"}); total != nil {
			t.Errorf("usageTotal() = %v, want nil before any scan has run", *total)
		}
	})

	t.Run("nil scanner: nil, does not panic", func(t *testing.T) {
		bare := &Server{cfg: cfg}
		if total := bare.usageTotal(store.MetricQuery{Source: "docker", Metric: "cpu_pct"}); total != nil {
			t.Errorf("usageTotal() = %v, want nil with no scanner at all", *total)
		}
	})
}
