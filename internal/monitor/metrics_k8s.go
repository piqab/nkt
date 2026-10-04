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

// SourceK8s — ряды подов Kubernetes (субъект — «namespace/под»);
// SourceK8sNode — узлов кластера, включая рабочие (субъект — имя узла).
const (
	SourceK8s     = "k8s"
	SourceK8sNode = "k8s_node"
)

// k8sKubectl — начало команды kubectl на control plane; не он — nil.
func k8sKubectl(ctx context.Context, c collect.Collector) []string {
	switch {
	case c.Exists("/etc/systemd/system/k3s.service") && collect.Which(ctx, c, "k3s"):
		return []string{"k3s", "kubectl"}
	case c.Exists("/etc/kubernetes/admin.conf") && collect.Which(ctx, c, "kubectl"):
		return []string{"kubectl", "--kubeconfig", "/etc/kubernetes/admin.conf"}
	}
	return nil
}

// k8sSamples — процессор (доля ядра, как у контейнеров: 100% = ядро) и
// память подов из kubectl top (нужен metrics-server; нет — рядов нет).
func (m *MetricsCollector) k8sSamples(ctx context.Context, ts string, now time.Time) ([]store.MetricSample, error) {
	base := k8sKubectl(ctx, m.c)
	if base == nil {
		return nil, nil
	}
	argv := append(append([]string{}, base...), "top", "pods", "-A", "--no-headers")
	var samples []store.MetricSample
	out, err := m.c.Run(ctx, argv[0], argv[1:]...)
	if err != nil || !out.OK() {
		out.Stdout = ""
	}
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
	// Узлы кластера — все, и рабочие тоже: их процессор и память.
	nodeArgv := append(append([]string{}, base...), "top", "nodes", "--no-headers")
	nodes, err := m.c.Run(ctx, nodeArgv[0], nodeArgv[1:]...)
	if err == nil && nodes.OK() {
		for _, n := range ParseTopNodes(nodes.Stdout) {
			cpu, mem := n.cpuPct, n.memBytes
			if m.Simulated() {
				s := dailyShape(now, SourceK8sNode+n.name)
				cpu = math.Round(cpu*(0.5+s)*10) / 10
				mem = mem * (0.8 + 0.3*s)
			}
			samples = append(samples, sample(ts, SourceK8sNode, n.name, "cpu_pct", cpu), sample(ts, SourceK8sNode, n.name, "mem_bytes", mem),
				// Доли от ёмкости узла — для полосок в «Мониторинге» хаба.
				sample(ts, SourceK8sNode, n.name, "cpu_share_pct", n.cpuShare), sample(ts, SourceK8sNode, n.name, "mem_share_pct", n.memShare))
		}
	}
	return samples, nil
}

// ParseTopNodes — строки «узел 250m 6% 1200Mi 31%» kubectl top nodes:
// процессор в долях ядра (100% = ядро, как у подов) и память в байтах.
// Узел без метрик («<unknown>») пропускается.
func ParseTopNodes(text string) []topPod {
	var out []topPod
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		cpu, ok1 := parseMillicores(f[1])
		mem, ok2 := parseQuantity(f[3])
		if !ok1 || !ok2 {
			continue
		}
		n := topPod{name: f[0], cpuPct: math.Round(cpu/10*10) / 10, memBytes: mem}
		if len(f) >= 5 {
			n.cpuShare, _ = strconv.ParseFloat(strings.TrimSuffix(f[2], "%"), 64)
			n.memShare, _ = strconv.ParseFloat(strings.TrimSuffix(f[4], "%"), 64)
		}
		out = append(out, n)
	}
	return out
}

type topPod struct {
	name             string
	cpuPct, memBytes float64
	// cpuShare, memShare — у узла: доля от его ёмкости, %.
	cpuShare, memShare float64
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
