package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// Нагрузка инстансов LXD и машин libvirt. Процессор у обоих — накопленное
// время в наносекундах: процент — его прирост за прошедший между
// замерами интервал (как у Docker: 100% = одно ядро).

// cpuPercent — процент по приросту процессорного времени и часов.
func (m *MetricsCollector) cpuPercent(ctx context.Context, key string, cpuNS float64, now time.Time) (float64, bool, error) {
	dCPU, ok1, err := m.db.CounterDelta(ctx, key+":cpu", cpuNS)
	if err != nil {
		return 0, false, err
	}
	dT, ok2, err := m.db.CounterDelta(ctx, key+":clock", float64(now.UnixMilli()))
	if err != nil {
		return 0, false, err
	}
	if !ok1 || !ok2 || dT <= 0 {
		return 0, false, nil
	}
	return math.Round(dCPU/(dT*1e6)*1000) / 10, true, nil
}

// counterSamples — прирост сетевых счётчиков rx/tx.
func (m *MetricsCollector) counterSamples(ctx context.Context, ts, source, name string, rx, tx float64) ([]store.MetricSample, error) {
	var out []store.MetricSample
	for _, c := range []struct {
		metric string
		v      float64
	}{{"net_rx_bytes", rx}, {"net_tx_bytes", tx}} {
		d, ok, err := m.db.CounterDelta(ctx, source+":"+name+":"+c.metric, c.v)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, sample(ts, source, name, c.metric, d))
		}
	}
	return out, nil
}

// workloadSamples собирает четыре ряда одного объекта; в демо-режиме —
// правдоподобные значения вместо застывших счётчиков.
func (m *MetricsCollector) workloadSamples(ctx context.Context, ts string, now time.Time, source, name string, cpuNS, memBytes, rx, tx float64) ([]store.MetricSample, error) {
	if m.Simulated() {
		shape := dailyShape(now, source+name)
		if memBytes == 0 {
			memBytes = float64(256<<20) + float64(hashRange(name, 1<<30))*2
		}
		return []store.MetricSample{
			sample(ts, source, name, "cpu_pct", math.Round(shape*float64(4+hashRange(name, 60))*10)/10),
			sample(ts, source, name, "mem_bytes", memBytes*(0.6+0.5*shape)),
			sample(ts, source, name, "net_rx_bytes", shape*float64(30_000+hashRange(name, 700_000))),
			sample(ts, source, name, "net_tx_bytes", shape*float64(20_000+hashRange(name, 400_000))),
		}, nil
	}
	out, err := m.counterSamples(ctx, ts, source, name, rx, tx)
	if err != nil {
		return nil, err
	}
	if pct, ok, err := m.cpuPercent(ctx, source+":"+name, cpuNS, now); err != nil {
		return nil, err
	} else if ok {
		out = append(out, sample(ts, source, name, "cpu_pct", pct))
	}
	if memBytes > 0 {
		out = append(out, sample(ts, source, name, "mem_bytes", memBytes))
	}
	return out, nil
}

// lxdSamples — `lxc list --format json` отдаёт и состояние работающих
// инстансов: процессор, память, счётчики интерфейсов.
func (m *MetricsCollector) lxdSamples(ctx context.Context, ts string, now time.Time) ([]store.MetricSample, error) {
	res, err := m.c.Run(ctx, "lxc", "list", "--format", "json")
	if err != nil || !res.OK() {
		return nil, nil
	}
	var list []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		State  *struct {
			CPU struct {
				Usage float64 `json:"usage"`
			} `json:"cpu"`
			Memory struct {
				Usage float64 `json:"usage"`
			} `json:"memory"`
			Network map[string]struct {
				Type     string `json:"type"`
				Counters struct {
					BytesReceived float64 `json:"bytes_received"`
					BytesSent     float64 `json:"bytes_sent"`
				} `json:"counters"`
			} `json:"network"`
		} `json:"state"`
	}
	if json.Unmarshal([]byte(res.Stdout), &list) != nil {
		return nil, nil
	}
	var out []store.MetricSample
	for _, in := range list {
		if !strings.EqualFold(in.Status, "running") || in.State == nil {
			continue
		}
		var rx, tx float64
		for ifname, n := range in.State.Network {
			if ifname == "lo" || n.Type == "loopback" {
				continue
			}
			rx += n.Counters.BytesReceived
			tx += n.Counters.BytesSent
		}
		s, err := m.workloadSamples(ctx, ts, now, SourceLXD, in.Name, in.State.CPU.Usage, in.State.Memory.Usage, rx, tx)
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	return out, nil
}

// libvirtDomStats — разобранный вывод `virsh domstats`: имя → ключ → число.
func libvirtDomStats(text string) map[string]map[string]float64 {
	out := map[string]map[string]float64{}
	var cur map[string]float64
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if name, ok := strings.CutPrefix(line, "Domain:"); ok {
			name = strings.Trim(strings.TrimSpace(name), "'\"")
			cur = map[string]float64{}
			out[name] = cur
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			continue
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cur[k] = f
		}
	}
	return out
}

// libvirtSamples — работающие машины: cpu.time, память процесса qemu на
// хосте (balloon.rss, иначе выделенная balloon.current), сеть.
func (m *MetricsCollector) libvirtSamples(ctx context.Context, ts string, now time.Time) ([]store.MetricSample, error) {
	res, err := m.c.Run(ctx, "virsh", "-c", "qemu:///system", "domstats", "--list-active", "--cpu-total", "--balloon", "--interface")
	if err != nil || !res.OK() {
		return nil, nil
	}
	var out []store.MetricSample
	for name, st := range libvirtDomStats(res.Stdout) {
		mem := st["balloon.rss"]
		if mem == 0 {
			mem = st["balloon.current"]
		}
		var rx, tx float64
		for i := 0; i < int(st["net.count"]); i++ {
			p := "net." + strconv.Itoa(i) + "."
			rx += st[p+"rx.bytes"]
			tx += st[p+"tx.bytes"]
		}
		s, err := m.workloadSamples(ctx, ts, now, SourceLibvirt, name, st["cpu.time"], mem*1024, rx, tx)
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	return out, nil
}
