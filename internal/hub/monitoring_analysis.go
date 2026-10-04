package hub

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/piqab/nkt/internal/forecast"
	"github.com/piqab/nkt/internal/monitor"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Прогнозы и подсказки «Мониторинга» — по истории хаба, раз в час после
// сбора. Прогнозы — по тренду (forecast.Fit), подсказки — что с этим
// делать и куда идти; хаб сам ничего не меняет. Важное (диск скоро
// заполнится, память на пределе, похоже на утечку, упала доступность) —
// ещё и оповещение «прогноз», один раз, пока ситуация не изменится.

// MonSettings — пороги.
type MonSettings struct {
	DiskWarnDays  float64 `json:"disk_warn_days"`
	DiskCritDays  float64 `json:"disk_crit_days"`
	MemPct        float64 `json:"mem_pct"`
	CPUPct        float64 `json:"cpu_pct"`
	LeakDays      float64 `json:"leak_days"`
	LeakGrowthPct float64 `json:"leak_growth_pct"`
	AvailDropPP   float64 `json:"avail_drop_pp"`
}

func defaultMonSettings() MonSettings {
	return MonSettings{DiskWarnDays: 7, DiskCritDays: 2, MemPct: 90, CPUPct: 85, LeakDays: 3, LeakGrowthPct: 20, AvailDropPP: 2}
}

const (
	monSettingsKV = "mon.settings"
	monAlertedKV  = "mon.alerted"
)

// MonitoringSettings — пороги (сохранённые поверх умолчаний).
func (s *Server) MonitoringSettings(ctx context.Context) MonSettings {
	set := defaultMonSettings()
	if raw, ok, err := s.db.KVGet(ctx, monSettingsKV); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &set)
	}
	return set
}

// Insight — прогноз или подсказка.
type Insight struct {
	Kind     string  `json:"kind"`
	Severity string  `json:"severity"` // critical | warning | info
	Tab      string  `json:"tab"`      // load | availability
	HostID   int64   `json:"host_id"`
	Host     string  `json:"host"`
	Source   string  `json:"source,omitempty"`
	Subject  string  `json:"subject,omitempty"`
	Value    float64 `json:"value,omitempty"`
	// Link — раздел хоста, где это решается (disks, images, containers,
	// availability, services).
	Link string `json:"link,omitempty"`
	Key  string `json:"-"`
	Args []any  `json:"-"`
	// Text — на языке читающего (заполняет API).
	Text string `json:"text"`
	// Alert — становится оповещением.
	Alert bool `json:"-"`
	// QuietWindow — [день недели 0–6, час] UTC (у подсказки окна).
	QuietWindow []int `json:"quiet_window,omitempty"`
}

var workloadSources = []string{monitor.SourceDocker, monitor.SourcePodman, monitor.SourceLXD, monitor.SourceLibvirt, monitor.SourceK8s}

// monHosts — хосты с историей: id → имя (машина хаба — localhost).
func (s *Server) monHosts(ctx context.Context) []store.Host {
	out := []store.Host{}
	if s.local != nil {
		out = append(out, store.Host{ID: localHostID, Name: "localhost"})
	}
	if hosts, err := s.db.ListHosts(ctx); err == nil {
		out = append(out, hosts...)
	}
	return out
}

func hourTime(h string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02T15", h)
	return t, err == nil
}

func toPoints(rows []store.MonRow, base time.Time, val func(store.MonRow) float64) []forecast.Point {
	out := make([]forecast.Point, 0, len(rows))
	for _, r := range rows {
		if t, ok := hourTime(r.At); ok {
			out = append(out, forecast.Point{T: t.Sub(base).Hours() / 24, V: val(r)})
		}
	}
	return out
}

// analyzeMonitoring — пересчёт прогнозов и подсказок, оповещения.
func (s *Server) analyzeMonitoring(ctx context.Context) {
	set := s.MonitoringSettings(ctx)
	now := time.Now().UTC().Truncate(time.Hour)
	base := now.Add(-30 * 24 * time.Hour)
	h := func(t time.Time) string { return t.Format("2006-01-02T15") }
	var out []Insight
	weekCPU := map[[2]int][]float64{}
	type memFree struct {
		host  store.Host
		free  float64
		total float64
	}
	var free []memFree
	type heavy struct {
		host     store.Host
		subject  string
		source   string
		mem      float64
		memPct   float64
		memTotal float64
	}
	var busy []heavy

	for _, host := range s.monHosts(ctx) {
		// Диски: тренд заполнения за 14 дней.
		disk, _ := s.db.MonSeries(ctx, store.MonSeriesQuery{HostID: host.ID, Source: monitor.SourceDisk, From: h(now.Add(-14 * 24 * time.Hour)), To: h(now.Add(time.Hour))})
		byMount := map[string][]store.MonRow{}
		size := map[string]float64{}
		for _, r := range disk {
			switch r.Metric {
			case "used_bytes":
				byMount[r.Subject] = append(byMount[r.Subject], r)
			case "size_bytes":
				size[r.Subject] = r.Max
			}
		}
		for mount, rows := range byMount {
			sz := size[mount]
			if sz <= 0 {
				continue
			}
			tr, ok := forecast.Fit(toPoints(rows, base, func(r store.MonRow) float64 { return r.Max }))
			if !ok || tr.Span < 2 || tr.Monotonic < 0.55 {
				continue
			}
			days, ok := tr.DaysUntil(sz)
			if !ok || days > 60 {
				continue
			}
			sev, alert := "info", false
			switch {
			case days <= set.DiskCritDays:
				sev, alert = "critical", true
			case days <= set.DiskWarnDays:
				sev, alert = "warning", true
			}
			out = append(out, Insight{Kind: "disk_full", Severity: sev, Tab: "load", HostID: host.ID, Host: host.Name, Subject: mount,
				Value: math.Round(days*10) / 10, Link: "disks", Alert: alert,
				Key: "mon.diskFull", Args: []any{host.Name, mount, math.Round(days*10) / 10, math.Round(tr.Slope/(1<<30)*100) / 100}})
		}

		// Память и CPU хоста: последние сутки и неделя к неделе.
		hostRows, _ := s.db.MonSeries(ctx, store.MonSeriesQuery{HostID: host.ID, Source: monitor.SourceHost, From: h(now.Add(-14 * 24 * time.Hour)), To: h(now.Add(time.Hour))})
		var memUsed, memTotal, cpu []store.MonRow
		for _, r := range hostRows {
			switch r.Metric {
			case "mem_used_bytes":
				memUsed = append(memUsed, r)
			case "mem_total_bytes":
				memTotal = append(memTotal, r)
			case "cpu_pct":
				cpu = append(cpu, r)
				if t, ok := hourTime(r.At); ok {
					k := [2]int{int(t.Weekday()), t.Hour()}
					weekCPU[k] = append(weekCPU[k], r.Avg)
				}
			}
		}
		total := 0.0
		if n := len(memTotal); n > 0 {
			total = memTotal[n-1].Max
		}
		if total > 0 && len(memUsed) > 0 {
			day := lastHours(memUsed, now, 24)
			pct := avgOf(day, func(r store.MonRow) float64 { return r.Avg }) / total * 100
			allHigh := len(day) >= 20
			for _, r := range day {
				if r.Avg/total*100 < set.MemPct {
					allHigh = false
				}
			}
			if allHigh {
				out = append(out, Insight{Kind: "mem_high", Severity: "warning", Tab: "load", HostID: host.ID, Host: host.Name,
					Value: math.Round(pct), Link: "containers", Alert: true, Key: "mon.memHigh", Args: []any{host.Name, math.Round(pct), set.MemPct}})
			}
			if grow := weekGrowth(memUsed, now); grow >= 15 {
				out = append(out, Insight{Kind: "mem_growth", Severity: "info", Tab: "load", HostID: host.ID, Host: host.Name,
					Value: math.Round(grow), Key: "mon.memGrowth", Args: []any{host.Name, math.Round(grow)}})
			}
			last := memUsed[len(memUsed)-1].Avg
			free = append(free, memFree{host: host, free: total - last, total: total})
		}
		if len(cpu) > 0 {
			day := lastHours(cpu, now, 24)
			if len(day) >= 20 && avgOf(day, func(r store.MonRow) float64 { return r.Avg }) >= set.CPUPct {
				v := math.Round(avgOf(day, func(r store.MonRow) float64 { return r.Avg }))
				out = append(out, Insight{Kind: "cpu_high", Severity: "warning", Tab: "load", HostID: host.ID, Host: host.Name,
					Value: v, Link: "containers", Key: "mon.cpuHigh", Args: []any{host.Name, v}})
			}
			if grow := weekGrowth(cpu, now); grow >= 25 {
				out = append(out, Insight{Kind: "cpu_growth", Severity: "info", Tab: "load", HostID: host.ID, Host: host.Name,
					Value: math.Round(grow), Key: "mon.cpuGrowth", Args: []any{host.Name, math.Round(grow)}})
			}
		}

		// Утечки: память нагрузки растёт без остановки LeakDays дней.
		for _, src := range workloadSources {
			rows, _ := s.db.MonSeries(ctx, store.MonSeriesQuery{HostID: host.ID, Source: src, Metric: "mem_bytes",
				From: h(now.Add(-time.Duration(set.LeakDays*24) * time.Hour)), To: h(now.Add(time.Hour))})
			bySub := map[string][]store.MonRow{}
			for _, r := range rows {
				bySub[r.Subject] = append(bySub[r.Subject], r)
			}
			for sub, rs := range bySub {
				if len(rs) < int(set.LeakDays*24*0.6) {
					continue
				}
				tr, ok := forecast.Fit(toPoints(rs, base, func(r store.MonRow) float64 { return r.Avg }))
				if !ok || tr.Slope <= 0 || tr.Monotonic < 0.7 {
					continue
				}
				first := rs[0].Avg
				if first <= 0 {
					continue
				}
				growth := tr.Slope * tr.Span / first * 100
				if growth < set.LeakGrowthPct {
					continue
				}
				out = append(out, Insight{Kind: "mem_leak", Severity: "warning", Tab: "load", HostID: host.ID, Host: host.Name,
					Source: src, Subject: sub, Value: math.Round(growth), Link: "containers", Alert: true,
					Key: "mon.memLeak", Args: []any{host.Name, sub, src, math.Round(growth), math.Round(tr.Slope / (1 << 20))}})
			}
			if total > 0 {
				// Самая тяжёлая нагрузка хоста — кандидат на перенос.
				for sub, rs := range bySub {
					if len(rs) == 0 {
						continue
					}
					m := rs[len(rs)-1].Avg
					busy = append(busy, heavy{host: host, subject: sub, source: src, mem: m, memPct: m / total * 100, memTotal: total})
				}
			}
		}

		// Доступность целей: сутки против недели, задержка.
		probes, _ := s.db.MonSeries(ctx, store.MonSeriesQuery{HostID: host.ID, Source: "probe", From: h(now.Add(-7 * 24 * time.Hour)), To: h(now.Add(time.Hour))})
		type acc struct{ ok24, tot24, ok7, tot7, lat24, lat7, n24, n7 float64 }
		per := map[string]*acc{}
		dayAgo := h(now.Add(-24 * time.Hour))
		for _, r := range probes {
			a := per[r.Subject]
			if a == nil {
				a = &acc{}
				per[r.Subject] = a
			}
			recent := r.At >= dayAgo
			switch r.Metric {
			case "ok":
				a.ok7 += r.Sum
				if recent {
					a.ok24 += r.Sum
				}
			case "total":
				a.tot7 += r.Sum
				if recent {
					a.tot24 += r.Sum
				}
			case "latency_ms":
				if r.Avg > 0 {
					if recent {
						a.lat24 += r.Avg
						a.n24++
					} else {
						a.lat7 += r.Avg
						a.n7++
					}
				}
			}
		}
		labels := map[string]string{}
		if ts, err := s.db.MonTargets(ctx, host.ID); err == nil {
			for _, t := range ts {
				labels[t.Key] = t.Label
			}
		}
		for key, a := range per {
			label := labels[key]
			if label == "" {
				label = key
			}
			if a.tot24 >= 10 && a.tot7 > a.tot24 {
				u24 := a.ok24 / a.tot24 * 100
				uPrev := (a.ok7 - a.ok24) / (a.tot7 - a.tot24) * 100
				if u24 < 99 && u24 < uPrev-set.AvailDropPP {
					out = append(out, Insight{Kind: "avail_drop", Severity: "warning", Tab: "availability", HostID: host.ID, Host: host.Name,
						Subject: label, Value: math.Round(u24*10) / 10, Link: "availability", Alert: true,
						Key: "mon.availDrop", Args: []any{host.Name, label, math.Round(u24*10) / 10, math.Round(uPrev*10) / 10}})
				}
			}
			if a.n24 >= 6 && a.n7 >= 24 {
				l24, l7 := a.lat24/a.n24, a.lat7/a.n7
				if l24 > 50 && l24 > 2*l7 {
					out = append(out, Insight{Kind: "latency_up", Severity: "info", Tab: "availability", HostID: host.ID, Host: host.Name,
						Subject: label, Value: math.Round(l24), Link: "availability", Key: "mon.latencyUp", Args: []any{host.Name, label, math.Round(l24), math.Round(l7)}})
				}
			}
		}
	}

	// Перенос: хост на пределе по памяти и хост с запасом.
	sort.Slice(free, func(i, j int) bool { return free[i].free > free[j].free })
	for _, b := range busy {
		if b.memTotal <= 0 {
			continue
		}
		pressured := false
		for _, in := range out {
			if in.Kind == "mem_high" && in.HostID == b.host.ID {
				pressured = true
			}
		}
		if !pressured || len(free) == 0 || free[0].host.ID == b.host.ID || free[0].free < b.mem*1.5 {
			continue
		}
		out = append(out, Insight{Kind: "rebalance", Severity: "info", Tab: "load", HostID: b.host.ID, Host: b.host.Name, Source: b.source, Subject: b.subject,
			Link: "containers", Key: "mon.rebalance", Args: []any{b.subject, b.host.Name, math.Round(b.mem/(1<<30)*10) / 10, free[0].host.Name, math.Round(free[0].free/(1<<30)*10) / 10}})
	}
	// Тихое окно недели: два часа подряд с наименьшей нагрузкой CPU по
	// всем хостам (UTC; интерфейс переводит в своё время).
	if len(weekCPU) >= 7*24*2/3 {
		best, bestV := [2]int{}, math.Inf(1)
		for d := 0; d < 7; d++ {
			for hr := 0; hr < 24; hr++ {
				a := meanF(weekCPU[[2]int{d, hr}])
				nd, nh := d, hr+1
				if nh == 24 {
					nd, nh = (d+1)%7, 0
				}
				b := meanF(weekCPU[[2]int{nd, nh}])
				if v := a + b; !math.IsNaN(v) && v < bestV {
					best, bestV = [2]int{d, hr}, v
				}
			}
		}
		if !math.IsInf(bestV, 1) {
			out = append(out, Insight{Kind: "quiet_window", Severity: "info", Tab: "load", QuietWindow: []int{best[0], best[1]},
				Value: math.Round(bestV/2*10) / 10, Key: "mon.quietWindow", Args: []any{math.Round(bestV/2*10) / 10}})
		}
	}

	sevRank := map[string]int{"critical": 0, "warning": 1, "info": 2}
	sort.SliceStable(out, func(i, j int) bool { return sevRank[out[i].Severity] < sevRank[out[j].Severity] })
	s.mon.mu.Lock()
	s.mon.insights = out
	s.mon.analyzed = time.Now()
	s.mon.mu.Unlock()
	s.alertInsights(ctx, out)
}

// alertInsights — оповещения «прогноз»: только новое или изменившееся
// по серьёзности; ушедшее забывается (вернётся — оповестит снова).
func (s *Server) alertInsights(ctx context.Context, list []Insight) {
	prev := map[string]string{}
	if raw, ok, err := s.db.KVGet(ctx, monAlertedKV); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &prev)
	}
	next := map[string]string{}
	for _, in := range list {
		if !in.Alert {
			continue
		}
		k := in.Kind + "|" + itoa64(in.HostID) + "|" + in.Source + "|" + in.Subject
		next[k] = in.Severity
		if prev[k] == in.Severity {
			continue
		}
		host := store.Host{ID: in.HostID, Name: in.Host}
		if in.HostID == localHostID {
			host.ID = 0
		} else if hh, err := s.db.HostByID(ctx, in.HostID); err == nil {
			host = hh
		}
		s.hub.recordEventMsg(ctx, host, store.EventForecast, in.Severity, in.Key, in.Args...)
	}
	raw, _ := json.Marshal(next)
	_ = s.db.KVSet(ctx, monAlertedKV, string(raw))
}

func lastHours(rows []store.MonRow, now time.Time, hours int) []store.MonRow {
	from := now.Add(-time.Duration(hours) * time.Hour).Format("2006-01-02T15")
	var out []store.MonRow
	for _, r := range rows {
		if r.At >= from {
			out = append(out, r)
		}
	}
	return out
}

func avgOf(rows []store.MonRow, f func(store.MonRow) float64) float64 {
	if len(rows) == 0 {
		return 0
	}
	s := 0.0
	for _, r := range rows {
		s += f(r)
	}
	return s / float64(len(rows))
}

// weekGrowth — рост среднего последних 7 дней к предыдущим 7 (%); 0 —
// данных мало.
func weekGrowth(rows []store.MonRow, now time.Time) float64 {
	cut := now.Add(-7 * 24 * time.Hour).Format("2006-01-02T15")
	var a, b []float64
	for _, r := range rows {
		if r.At >= cut {
			a = append(a, r.Avg)
		} else {
			b = append(b, r.Avg)
		}
	}
	if len(a) < 72 || len(b) < 72 {
		return 0
	}
	pa, pb := meanF(a), meanF(b)
	if pb <= 0 {
		return 0
	}
	return (pa - pb) / pb * 100
}

func meanF(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func itoa64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// localizedInsights — подсказки с текстом на языке запроса.
func localizedInsights(ctx context.Context, list []Insight) []Insight {
	out := make([]Insight, len(list))
	for i, in := range list {
		in.Text = msgs.Tc(ctx, in.Key, in.Args...)
		out[i] = in
	}
	return out
}
