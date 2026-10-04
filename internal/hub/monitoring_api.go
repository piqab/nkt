package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/monitor"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// API раздела «Мониторинг»: сводка по всем хостам за период, ряды для
// графиков, «собрать сейчас», пороги прогнозов.

// monRange — период: начало и суточная ли детализация.
func monRange(r *http.Request) (from, to string, daily bool, span time.Duration) {
	now := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	span = 24 * time.Hour
	switch r.URL.Query().Get("range") {
	case "7d":
		span = 7 * 24 * time.Hour
	case "30d":
		span = 30 * 24 * time.Hour
	case "90d":
		span = 90 * 24 * time.Hour
	case "365d":
		span, daily = 365*24*time.Hour, true
	}
	if daily {
		return now.Add(-span).Format("2006-01-02"), now.Format("2006-01-02") + "Z", true, span
	}
	return now.Add(-span).Format("2006-01-02T15"), now.Format("2006-01-02T15"), false, span
}

type monDisk struct {
	Mount string   `json:"mount"`
	Used  float64  `json:"used"`
	Size  float64  `json:"size"`
	Pct   float64  `json:"pct"`
	ETA   *float64 `json:"eta_days,omitempty"`
}

type monHostRow struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Group       string    `json:"group,omitempty"`
	Reachable   *bool     `json:"reachable,omitempty"`
	Old         bool      `json:"old,omitempty"`
	Error       string    `json:"error,omitempty"`
	CollectedAt string    `json:"collected_at,omitempty"`
	HasData     bool      `json:"has_data"`
	CPUAvg      float64   `json:"cpu_avg"`
	CPUMax      float64   `json:"cpu_max"`
	CPUNow      float64   `json:"cpu_now"`
	MemUsed     float64   `json:"mem_used"`
	MemTotal    float64   `json:"mem_total"`
	MemAvgPct   float64   `json:"mem_avg_pct"`
	MemMaxPct   float64   `json:"mem_max_pct"`
	LoadAvg     float64   `json:"load_avg"`
	LoadMax     float64   `json:"load_max"`
	Disks       []monDisk `json:"disks"`
	Workloads   int       `json:"workloads"`
}

type monWorkload struct {
	HostID  int64   `json:"host_id"`
	Host    string  `json:"host"`
	Source  string  `json:"source"`
	Subject string  `json:"subject"`
	CPUAvg  float64 `json:"cpu_avg"`
	CPUMax  float64 `json:"cpu_max"`
	MemAvg  float64 `json:"mem_avg"`
	MemMax  float64 `json:"mem_max"`
	NetRx   float64 `json:"net_rx"`
	NetTx   float64 `json:"net_tx"`
}

type monTargetRow struct {
	HostID  int64    `json:"host_id"`
	Host    string   `json:"host"`
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	Service string   `json:"service"`
	OK      float64  `json:"ok"`
	Total   float64  `json:"total"`
	Uptime  *float64 `json:"uptime,omitempty"`
	Latency float64  `json:"latency"`
}

// handleMonitoringOverview — GET /hub/monitoring/overview?range=.
func (s *Server) handleMonitoringOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	from, to, daily, _ := monRange(r)
	aggs, err := s.db.MonAggregate(ctx, from, to, daily)
	if err != nil {
		fail(w, r, err)
		return
	}
	allow := s.scopeFilter(ctx)
	hosts := s.monHosts(ctx)
	names := map[int64]string{}
	rows := map[int64]*monHostRow{}
	var order []int64
	for _, h := range hosts {
		if allow != nil && !allow(h.ID) {
			continue
		}
		names[h.ID] = h.Name
		row := &monHostRow{ID: h.ID, Name: h.Name, Group: h.Group, Disks: []monDisk{}}
		if h.ID != localHostID {
			if ov, ok := s.hub.overviewOf(h.ID); ok {
				reach := ov.reachable
				row.Reachable = &reach
			}
		}
		rows[h.ID] = row
		order = append(order, h.ID)
	}
	s.mon.mu.Lock()
	for id, st := range s.mon.hosts {
		if row := rows[id]; row != nil {
			row.Old, row.Error = st.Old, st.Error
			if !st.At.IsZero() {
				row.CollectedAt = store.FormatTime(st.At)
			}
		}
	}
	insights := append([]Insight(nil), s.mon.insights...)
	collecting, lastRun := s.mon.collecting, s.mon.lastRun
	s.mon.mu.Unlock()

	eta := map[string]float64{}
	for _, in := range insights {
		if in.Kind == "disk_full" {
			eta[strconv.FormatInt(in.HostID, 10)+"|"+in.Subject] = in.Value
		}
	}
	disks := map[int64]map[string]*monDisk{}
	wl := map[string]*monWorkload{}
	targets := map[string]*monTargetRow{}
	for _, a := range aggs {
		row := rows[a.HostID]
		if row == nil {
			continue
		}
		row.HasData = true
		switch a.Source {
		case monitor.SourceHost:
			switch a.Metric {
			case "cpu_pct":
				row.CPUAvg, row.CPUMax, row.CPUNow = round1(a.Avg), round1(a.Max), round1(a.Last)
			case "mem_used_bytes":
				row.MemUsed = a.Last
				row.MemAvgPct, row.MemMaxPct = a.Avg, a.Max // в проценты — ниже, когда известен объём
			case "mem_total_bytes":
				row.MemTotal = a.Max
			case "load1":
				row.LoadAvg, row.LoadMax = round1(a.Avg), round1(a.Max)
			}
		case monitor.SourceDisk:
			if disks[a.HostID] == nil {
				disks[a.HostID] = map[string]*monDisk{}
			}
			d := disks[a.HostID][a.Subject]
			if d == nil {
				d = &monDisk{Mount: a.Subject}
				disks[a.HostID][a.Subject] = d
			}
			if a.Metric == "used_bytes" {
				d.Used = a.Last
			} else if a.Metric == "size_bytes" {
				d.Size = a.Max
			}
		case "probe":
			k := strconv.FormatInt(a.HostID, 10) + "|" + a.Subject
			t := targets[k]
			if t == nil {
				t = &monTargetRow{HostID: a.HostID, Host: row.Name, Key: a.Subject, Label: a.Subject}
				targets[k] = t
			}
			switch a.Metric {
			case "ok":
				t.OK = a.Sum
			case "total":
				t.Total = a.Sum
			case "latency_ms":
				t.Latency = round1(a.Avg)
			}
		default:
			k := strconv.FormatInt(a.HostID, 10) + "|" + a.Source + "|" + a.Subject
			x := wl[k]
			if x == nil {
				x = &monWorkload{HostID: a.HostID, Host: row.Name, Source: a.Source, Subject: a.Subject}
				wl[k] = x
			}
			switch a.Metric {
			case "cpu_pct":
				x.CPUAvg, x.CPUMax = round1(a.Avg), round1(a.Max)
			case "mem_bytes":
				x.MemAvg, x.MemMax = a.Avg, a.Max
			case "net_rx_bytes":
				x.NetRx = a.Sum
			case "net_tx_bytes":
				x.NetTx = a.Sum
			}
		}
	}
	for id, row := range rows {
		if row.MemTotal > 0 {
			row.MemAvgPct, row.MemMaxPct = round1(row.MemAvgPct/row.MemTotal*100), round1(row.MemMaxPct/row.MemTotal*100)
		} else {
			row.MemAvgPct, row.MemMaxPct = 0, 0
		}
		for _, d := range disks[id] {
			if d.Size > 0 {
				d.Pct = round1(d.Used / d.Size * 100)
			}
			if v, ok := eta[strconv.FormatInt(id, 10)+"|"+d.Mount]; ok {
				v := v
				d.ETA = &v
			}
			row.Disks = append(row.Disks, *d)
		}
		sort.Slice(row.Disks, func(i, j int) bool { return row.Disks[i].Mount < row.Disks[j].Mount })
	}
	outWL := make([]monWorkload, 0, len(wl))
	for _, x := range wl {
		outWL = append(outWL, *x)
		rows[x.HostID].Workloads++
	}
	sort.Slice(outWL, func(i, j int) bool { return outWL[i].CPUAvg > outWL[j].CPUAvg })
	// Подписи целей — из сохранённого списка хоста.
	for id := range rows {
		if list, err := s.db.MonTargets(ctx, id); err == nil {
			for _, mt := range list {
				if t := targets[strconv.FormatInt(id, 10)+"|"+mt.Key]; t != nil {
					t.Label, t.Kind, t.Service = mt.Label, mt.Kind, mt.Service
				}
			}
		}
	}
	outT := make([]monTargetRow, 0, len(targets))
	for _, t := range targets {
		if t.Total > 0 {
			u := round2f(t.OK / t.Total * 100)
			t.Uptime = &u
		}
		outT = append(outT, *t)
	}
	sort.Slice(outT, func(i, j int) bool {
		ui, uj := 101.0, 101.0
		if outT[i].Uptime != nil {
			ui = *outT[i].Uptime
		}
		if outT[j].Uptime != nil {
			uj = *outT[j].Uptime
		}
		if ui != uj {
			return ui < uj
		}
		return outT[i].Host+outT[i].Label < outT[j].Host+outT[j].Label
	})
	hostRows := make([]monHostRow, 0, len(order))
	for _, id := range order {
		hostRows = append(hostRows, *rows[id])
	}

	// Час недели (UTC) за период, не больше 30 дней: доступность всех
	// целей и средний CPU всех хостов.
	heatFrom := from
	if daily || len(from) != 13 {
		heatFrom = time.Now().UTC().Add(-30 * 24 * time.Hour).Format("2006-01-02T15")
	}
	heatTo := time.Now().UTC().Truncate(time.Hour).Add(time.Hour).Format("2006-01-02T15")
	avail := [7][24][2]float64{}
	cpu := [7][24][2]float64{}
	if cells, err := s.db.MonByHour(ctx, heatFrom, heatTo, "probe"); err == nil {
		for _, c := range cells {
			if t, ok := hourTime(c.Hour); ok {
				switch c.Metric {
				case "ok":
					avail[t.Weekday()][t.Hour()][0] += c.Sum
				case "total":
					avail[t.Weekday()][t.Hour()][1] += c.Sum
				}
			}
		}
	}
	if cells, err := s.db.MonByHour(ctx, heatFrom, heatTo, monitor.SourceHost); err == nil {
		for _, c := range cells {
			if t, ok := hourTime(c.Hour); ok && c.Metric == "cpu_pct" {
				cpu[t.Weekday()][t.Hour()][0] += c.Avg
				cpu[t.Weekday()][t.Hour()][1]++
			}
		}
	}
	heatAvail := make([][]*float64, 7)
	heatCPU := make([][]*float64, 7)
	for d := 0; d < 7; d++ {
		heatAvail[d] = make([]*float64, 24)
		heatCPU[d] = make([]*float64, 24)
		for h := 0; h < 24; h++ {
			if c := avail[d][h]; c[1] > 0 {
				v := round2f(c[0] / c[1] * 100)
				heatAvail[d][h] = &v
			}
			if c := cpu[d][h]; c[1] > 0 {
				v := round1(c[0] / c[1])
				heatCPU[d][h] = &v
			}
		}
	}
	// Подсказки — только по хостам в пределах токена.
	var vis []Insight
	for _, in := range insights {
		if in.HostID == 0 || rows[in.HostID] != nil {
			vis = append(vis, in)
		}
	}
	out := map[string]any{
		"hosts": hostRows, "workloads": outWL, "targets": outT,
		"heat_avail": heatAvail, "heat_cpu": heatCPU,
		"insights": localizedInsights(ctx, vis), "collecting": collecting,
		"settings": s.MonitoringSettings(ctx), "daily": daily,
	}
	if !lastRun.IsZero() {
		out["last_run"] = store.FormatTime(lastRun)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMonitoringSeries — GET /hub/monitoring/series?host=&source=&subject=&metric=&range=.
func (s *Server) handleMonitoringSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.URL.Query().Get("host"), 10, 64)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if allow := s.scopeFilter(ctx); allow != nil && !allow(id) {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("auth.tokenHostDenied"))
		return
	}
	from, to, daily, _ := monRange(r)
	q := r.URL.Query()
	rows, err := s.db.MonSeries(ctx, store.MonSeriesQuery{HostID: id, Source: q.Get("source"), Subject: q.Get("subject"),
		Metric: q.Get("metric"), From: from, To: to, Daily: daily})
	if err != nil {
		fail(w, r, err)
		return
	}
	type series struct {
		Source  string   `json:"source"`
		Subject string   `json:"subject"`
		Metric  string   `json:"metric"`
		Points  [][4]any `json:"points"`
	}
	var out []series
	for _, rr := range rows {
		n := len(out)
		if n == 0 || out[n-1].Source != rr.Source || out[n-1].Subject != rr.Subject || out[n-1].Metric != rr.Metric {
			out = append(out, series{Source: rr.Source, Subject: rr.Subject, Metric: rr.Metric})
			n++
		}
		out[n-1].Points = append(out[n-1].Points, [4]any{rr.At, rr.Avg, rr.Max, rr.Sum})
	}
	if out == nil {
		out = []series{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": out, "daily": daily})
}

// handleMonitoringCollect — POST /hub/monitoring/collect: собрать сейчас.
func (s *Server) handleMonitoringCollect(w http.ResponseWriter, r *http.Request) {
	go s.collectMonitoring(context.WithoutCancel(r.Context()))
	s.db.Audit(r.Context(), auth.Username(r.Context()), "hub.monitoring_collect", "", "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"started": true})
}

// handleMonitoringSettings — GET/PUT /hub/monitoring/settings.
func (s *Server) handleMonitoringSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.MonitoringSettings(ctx))
		return
	}
	var set MonSettings
	if err := decodeJSON(r, &set); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if set.DiskCritDays <= 0 || set.DiskWarnDays < set.DiskCritDays || set.DiskWarnDays > 90 ||
		set.MemPct < 50 || set.MemPct > 100 || set.CPUPct < 50 || set.CPUPct > 100 ||
		set.LeakDays < 1 || set.LeakDays > 14 || set.LeakGrowthPct < 5 || set.LeakGrowthPct > 500 ||
		set.AvailDropPP <= 0 || set.AvailDropPP > 50 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("mon.settingsBad"))
		return
	}
	raw, _ := json.Marshal(set)
	if err := s.db.KVSet(ctx, monSettingsKV, string(raw)); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, auth.Username(ctx), "hub.monitoring_settings", "", "ok", string(raw))
	go s.analyzeMonitoring(context.WithoutCancel(ctx))
	writeJSON(w, http.StatusOK, set)
}

func round1(v float64) float64  { return float64(int64(v*10+0.5*sign(v))) / 10 }
func round2f(v float64) float64 { return float64(int64(v*100+0.5*sign(v))) / 100 }
func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
