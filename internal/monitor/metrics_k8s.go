package monitor

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/store"
)

// SourceK8s — ряды подов Kubernetes (субъект — «namespace/под»).
const SourceK8s = "k8s"

// k8sTopArgv — kubectl top pods на control plane; не он — nil.
func k8sTopArgv(ctx context.Context, c collect.Collector) []string {
	switch {
	case c.Exists("/etc/systemd/system/k3s.service") && collect.Which(ctx, c, "k3s"):
		return []string{"k3s", "kubectl", "top", "pods", "-A", "--no-headers"}
	case c.Exists("/etc/kubernetes/admin.conf") && collect.Which(ctx, c, "kubectl"):
		return []string{"kubectl", "--kubeconfig", "/etc/kubernetes/admin.conf", "top", "pods", "-A", "--no-headers"}
	}
	return nil
}

// k8sSamples — процессор (доля ядра, как у контейнеров: 100% = ядро) и
// память подов из kubectl top (нужен metrics-server; нет — рядов нет).
func (m *MetricsCollector) k8sSamples(ctx context.Context, ts string, now time.Time) ([]store.MetricSample, error) {
	argv := k8sTopArgv(ctx, m.c)
	if argv == nil {
		return nil, nil
	}
	out, err := m.c.Run(ctx, argv[0], argv[1:]...)
	if err != nil || !out.OK() {
		return nil, nil
	}
	var samples []store.MetricSample
	for _, p := range ParseTopPods(out.Stdout) {
		cpu, mem := p.cpuPct, p.memBytes
		if m.Simulated() {
			// Снимок fixtures не меняется — форма суток, как у остальных.
			s := dailyShape(now, SourceK8s+p.name)
			cpu = math.Round(cpu*(0.5+s)*10) / 10
			mem = mem * (0.8 + 0.3*s)
		}
		samples = append(samples, sample(ts, SourceK8s, p.name, "cpu_pct", cpu), sample(ts, SourceK8s, p.name, "mem_bytes", mem))
	}
	return samples, nil
}

type topPod struct {
	name             string
	cpuPct, memBytes float64
}

// ParseTopPods — строки «ns под 48m 182Mi» в проценты ядра и байты.
func ParseTopPods(text string) []topPod {
	var out []topPod
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		cpu, ok1 := parseMillicores(f[2])
		mem, ok2 := parseQuantity(f[3])
		if !ok1 || !ok2 {
			continue
		}
		out = append(out, topPod{name: f[0] + "/" + f[1], cpuPct: math.Round(cpu/10*10) / 10, memBytes: mem})
	}
	return out
}

// parseMillicores — «48m» → 48, «2» (ядра) → 2000.
func parseMillicores(s string) (float64, bool) {
	if v, ok := strings.CutSuffix(s, "m"); ok {
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	f, err := strconv.ParseFloat(s, 64)
	return f * 1000, err == nil
}

// parseQuantity — «182Mi», «1Gi», «512Ki», «100M» → байты.
func parseQuantity(s string) (float64, bool) {
	units := []struct {
		suffix string
		mul    float64
	}{{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"k", 1e3}, {"M", 1e6}, {"G", 1e9}}
	for _, u := range units {
		if v, ok := strings.CutSuffix(s, u.suffix); ok {
			f, err := strconv.ParseFloat(v, 64)
			return f * u.mul, err == nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}
