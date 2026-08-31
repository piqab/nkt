package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/althq/netknownsthat/internal/monitor"
	"github.com/althq/netknownsthat/internal/msgs"
	"github.com/althq/netknownsthat/internal/store"
)

// --------------------------------------------------------------- availability

type availabilityScreen struct {
	app *App

	table   *tview.Table
	heat    *tview.TextView
	detail  *tview.TextView
	root    *tview.Flex
	targets []store.TargetStatus
	window  time.Duration
}

func newAvailabilityScreen(a *App) *availabilityScreen {
	s := &availabilityScreen{app: a, window: 7 * 24 * time.Hour}
	s.table = newTable(a.T("tui.monitor.availability.colResource"), a.T("tui.monitor.availability.colAddress"),
		a.T("tui.monitor.availability.colNow"), a.T("tui.monitor.availability.colUptime24h"),
		a.T("tui.monitor.availability.colLatency"), a.T("tui.monitor.availability.colChecks"))
	s.heat = newPanel(a.T("tui.monitor.availability.downtimeByHourTitle"))
	s.detail = newPanel(a.T("tui.monitor.availability.colResource"))

	s.table.SetSelectionChangedFunc(func(row, _ int) { go s.loadDetail(row - 1) })
	s.table.SetInputCapture(s.onKey)

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.heat, 13, 0, false).
		AddItem(s.detail, 0, 1, false)

	s.root = tview.NewFlex().
		AddItem(s.table, 0, 1, true).
		AddItem(right, 82, 0, false)
	return s
}

func (s *availabilityScreen) title() string          { return s.app.T("tui.monitor.availability.title") }
func (s *availabilityScreen) view() tview.Primitive  { return s.root }
func (s *availabilityScreen) focus() tview.Primitive { return s.table }

func (s *availabilityScreen) hints() string {
	return dim(s.app.T("tui.monitor.availability.hints")) + tag(hexSeries1, windowLabel(s.app.Lang, s.window))
}

func windowLabel(lang msgs.Lang, d time.Duration) string {
	switch d {
	case 24 * time.Hour:
		return msgs.T(lang, "tui.monitor.windowDay")
	case 7 * 24 * time.Hour:
		return msgs.T(lang, "tui.monitor.windowWeek")
	case 14 * 24 * time.Hour:
		return msgs.T(lang, "tui.monitor.windowTwoWeeks")
	default:
		return msgs.T(lang, "tui.monitor.windowMonth")
	}
}

func (s *availabilityScreen) onKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 'p', 'P':
		s.probeNow()
		return nil
	case ' ':
		s.toggleEnabled()
		return nil
	case 'w', 'W':
		windows := []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour, 30 * 24 * time.Hour}
		for i, w := range windows {
			if w == s.window {
				s.window = windows[(i+1)%len(windows)]
				break
			}
		}
		s.app.renderNav()
		go s.refresh(context.Background())
		return nil
	}
	return event
}

func (s *availabilityScreen) selected() (store.TargetStatus, bool) {
	row, _ := s.table.GetSelection()
	idx := row - 1
	if idx < 0 || idx >= len(s.targets) {
		return store.TargetStatus{}, false
	}
	return s.targets[idx], true
}

func (s *availabilityScreen) probeNow() {
	target, ok := s.selected()
	if !ok {
		return
	}
	s.app.runAsync(s.app.T("tui.monitor.availability.checking", target.Label), false, func(ctx context.Context) (string, error) {
		result := s.app.Prober.ProbeTarget(ctx, target.Target)
		if err := s.app.DB.InsertProbeResults(ctx, []store.ProbeResult{result}); err != nil {
			return "", err
		}
		if !result.OK {
			return "", fmt.Errorf("%s", s.app.T("tui.monitor.availability.unreachable", target.Label, result.Error))
		}
		return s.app.T("tui.monitor.availability.respondsIn", target.Label, formatMS(s.app.Lang, result.LatencyMS)), nil
	})
}

func (s *availabilityScreen) toggleEnabled() {
	target, ok := s.selected()
	if !ok {
		return
	}
	want := !target.Enabled
	verb := s.app.T("tui.monitor.availability.resuming")
	msg := s.app.T("tui.monitor.availability.resumingFor", target.Label)
	if !want {
		verb = s.app.T("tui.monitor.availability.pausing")
		msg = s.app.T("tui.monitor.availability.pausingFor", target.Label)
	}
	s.app.runAsync(msg, true, func(ctx context.Context) (string, error) {
		if err := s.app.DB.SetTargetEnabled(ctx, target.ID, want); err != nil {
			return "", err
		}
		s.app.DB.Audit(ctx, s.app.actor, "monitor.target", target.Key, "ok",
			map[string]any{"enabled": want})
		return target.Label + ": " + verb, nil
	})
}

func (s *availabilityScreen) refresh(ctx context.Context) {
	statuses, err := s.app.DB.TargetStatuses(ctx)
	if err != nil {
		return
	}
	cells, _ := s.app.DB.AvailabilityHeatmap(ctx, 0, since(s.window), tzOffsetMinutes())

	s.app.queue(func() {
		s.targets = statuses
		s.table.Clear()
		headers := []string{
			s.app.T("tui.monitor.availability.colResource"), s.app.T("tui.monitor.availability.colAddress"),
			s.app.T("tui.monitor.availability.colNow"), s.app.T("tui.monitor.availability.colUptime24h"),
			s.app.T("tui.monitor.availability.colLatency"), s.app.T("tui.monitor.availability.colChecks"),
		}
		for i, h := range headers {
			s.table.SetCell(0, i, tview.NewTableCell(" "+h).
				SetTextColor(colorSecondary).SetSelectable(false).SetAttributes(tcell.AttrBold))
		}
		for i, t := range statuses {
			row := i + 1
			label := t.Label
			if !t.Enabled {
				label = "⏸ " + label
			}
			s.table.SetCell(row, 0, cell(truncate(label, 40)))
			s.table.SetCell(row, 1, cellDim(fmt.Sprintf("%s://%s:%d", t.Kind, t.Host, t.Port)))

			state, tone := s.app.T("tui.noData"), hexMuted
			if t.LastOK != nil {
				if *t.LastOK {
					state, tone = s.app.T("tui.monitor.availability.stateUp"), hexGood
				} else {
					state, tone = s.app.T("tui.monitor.availability.stateDown"), hexCritical
				}
			}
			s.table.SetCell(row, 2, cellColor("●"+state, tone))

			uptime := "—"
			if t.Checks24h > 0 {
				uptime = fmt.Sprintf("%.1f%%", t.Uptime24h)
			}
			s.table.SetCell(row, 3, cellRight(uptime))
			latency := "—"
			if t.LastLatency > 0 {
				latency = formatMS(s.app.Lang, t.LastLatency)
			}
			s.table.SetCell(row, 4, cellRight(latency))
			s.table.SetCell(row, 5, cellRight(fmt.Sprintf("%d", t.Checks24h)))
		}
		s.table.SetTitle(fmt.Sprintf(" %s ", s.app.T("tui.monitor.availability.tableTitle", len(statuses))))

		// The heatmap shows downtime, not uptime: the eye is drawn to dark
		// cells, and "when was it unavailable" is the question being asked.
		s.heat.SetText(heatmap(s.app.Lang, cells,
			func(c store.HeatCell) float64 {
				if c.Total == 0 {
					return 0
				}
				return 100 - c.Uptime
			},
			s.app.T("tui.monitor.availability.downtimeScaleLabel"),
			func(v float64) string { return fmt.Sprintf("%.1f%%", v) }))
		s.heat.SetTitle(fmt.Sprintf(" %s ", s.app.T("tui.monitor.availability.downtimeByHourTitleWindowed", windowLabel(s.app.Lang, s.window))))

		if len(statuses) > 0 {
			go s.loadDetail(0)
		}
	})
}

func (s *availabilityScreen) loadDetail(index int) {
	if index < 0 || index >= len(s.targets) {
		return
	}
	t := s.targets[index]
	ctx := context.Background()
	buckets, _ := s.app.DB.AvailabilityBuckets(ctx, t.ID, since(s.window), "hour", tzOffsetMinutes())
	outages, _ := s.app.DB.RecentOutages(ctx, since(s.window), 200)

	uptimes := make([]float64, 0, len(buckets))
	latencies := make([]float64, 0, len(buckets))
	for _, b := range buckets {
		uptimes = append(uptimes, b.Uptime)
		latencies = append(latencies, b.AvgLatency)
	}

	var mine []store.Outage
	for _, o := range outages {
		if o.TargetID == t.ID {
			mine = append(mine, o)
		}
	}

	s.app.queue(func() {
		var sb strings.Builder
		sb.WriteString(" " + bold(t.Label) + "\n")
		sb.WriteString(dim(fmt.Sprintf(" %s://%s:%d%s", t.Kind, t.Host, t.Port, t.Path)))
		if t.HostHeader != "" {
			sb.WriteString(dim("  Host: " + t.HostHeader))
		}
		sb.WriteString("\n\n")
		sb.WriteString(dim(s.app.T("tui.monitor.availability.sourceLabel")) + t.Source +
			dim(s.app.T("tui.monitor.availability.checksLabel")) +
			fmt.Sprintf("%d", t.Checks24h) + dim(s.app.T("tui.monitor.availability.failuresLabel")) +
			fmt.Sprintf("%d", t.Failures24h) + "\n")
		if t.LastError != "" {
			sb.WriteString(" " + tag(hexCritical, s.app.T("tui.monitor.availability.lastError", truncate(t.LastError, 60))) + "\n")
		}
		sb.WriteString(dim(s.app.T("tui.monitor.availability.lastCheckLabel")) + relativeTime(s.app.Lang, t.LastCheck) + "\n\n")

		sb.WriteString(dim(s.app.T("tui.monitor.availability.uptimeLabel")) + tag(hexGood, sparkline(s.app.Lang, uptimes, 64)) + "\n")
		sb.WriteString(dim(s.app.T("tui.monitor.availability.latencyLabel")) + tag(hexSeries1, sparkline(s.app.Lang, latencies, 64)) + "\n")
		if len(buckets) > 0 {
			sb.WriteString(dim(fmt.Sprintf("              %s … %s, %s\n",
				shortTime(bucketToTS(buckets[0].Bucket)), shortTime(bucketToTS(buckets[len(buckets)-1].Bucket)),
				s.app.T("tui.monitor.availability.byHours"))))
		}

		if len(mine) > 0 {
			sb.WriteString("\n" + dim(fmt.Sprintf(" %s\n", s.app.T("tui.monitor.availability.outagesFor", windowLabel(s.app.Lang, s.window)))))
			for i, o := range mine {
				if i >= 5 {
					sb.WriteString(dim(fmt.Sprintf("  %s\n", s.app.T("tui.monitor.availability.andMore", len(mine)-i))))
					break
				}
				sb.WriteString(fmt.Sprintf("  %s %s — %s %s\n", tag(hexCritical, "●"),
					shortTime(o.Start), shortTime(o.End), dim(truncate(o.Error, 40))))
			}
		}
		s.detail.SetText(sb.String())
	})
}

// bucketToTS turns an hourly bucket key back into a parsable timestamp.
func bucketToTS(bucket string) string {
	if len(bucket) == 13 { // 2026-07-28T14
		return bucket + ":00:00Z"
	}
	if len(bucket) == 10 { // 2026-07-28
		return bucket + "T00:00:00Z"
	}
	return bucket
}

// -------------------------------------------------------------------- usage

// usageSeries fixes the unit and aggregate of each metric, so a chart never
// mixes measures of different scale.
type usageSeries struct {
	labelKey string
	source   string
	metric   string
	agg      string
	format   func(float64) string
}

// usageCatalogue builds the metric catalogue for the given language: the
// format funcs for byte-valued metrics close over lang so their unit labels
// (Б/КБ/… vs B/KB/…) match the rest of the screen.
func usageCatalogue(lang msgs.Lang) []usageSeries {
	return []usageSeries{
		{"tui.monitor.usage.dockerNetTraffic", monitor.SourceDocker, "net_rx_bytes", "sum", func(v float64) string { return formatBytes(lang, v) }},
		{"tui.monitor.usage.dockerCPU", monitor.SourceDocker, "cpu_pct", "avg", func(v float64) string { return fmt.Sprintf("%.1f%%", v) }},
		{"tui.monitor.usage.dockerMemory", monitor.SourceDocker, "mem_bytes", "avg", func(v float64) string { return formatBytes(lang, v) }},
		{"tui.monitor.usage.firewallTraffic", monitor.SourceIptables, "bytes", "sum", func(v float64) string { return formatBytes(lang, v) }},
		{"tui.monitor.usage.nginxRequests", monitor.SourceNginxLog, "requests", "sum", formatCount},
		{"tui.monitor.usage.nginx5xxErrors", monitor.SourceNginxLog, "errors_5xx", "sum", formatCount},
		{"tui.monitor.usage.haproxyRequests", monitor.SourceHAProxyLog, "requests", "sum", formatCount},
	}
}

type usageScreen struct {
	app *App

	top     *tview.TextView
	heat    *tview.TextView
	trend   *tview.TextView
	root    *tview.Flex
	current int
	window  time.Duration
}

func newUsageScreen(a *App) *usageScreen {
	s := &usageScreen{app: a, window: 7 * 24 * time.Hour}
	s.trend = newPanel(a.T("tui.monitor.usage.trendTitle"))
	s.top = newPanel(a.T("tui.monitor.usage.topTitle"))
	s.heat = newPanel(a.T("tui.monitor.usage.scheduleTitle"))

	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.trend, 10, 0, false).
		AddItem(s.top, 0, 1, false)

	s.root = tview.NewFlex().
		AddItem(left, 0, 1, true).
		AddItem(s.heat, 82, 0, false)

	s.trend.SetInputCapture(s.onKey)
	s.top.SetInputCapture(s.onKey)
	return s
}

func (s *usageScreen) title() string          { return s.app.T("tui.monitor.usage.title") }
func (s *usageScreen) view() tview.Primitive  { return s.root }
func (s *usageScreen) focus() tview.Primitive { return s.top }

func (s *usageScreen) hints() string {
	spec := usageCatalogue(s.app.Lang)[s.current]
	return dim(s.app.T("tui.monitor.usage.metricLabel")) + tag(hexSeries1, s.app.T(spec.labelKey)) +
		dim(s.app.T("tui.monitor.usage.periodLabel")) + tag(hexSeries1, windowLabel(s.app.Lang, s.window))
}

func (s *usageScreen) onKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 'm', 'M':
		s.current = (s.current + 1) % len(usageCatalogue(s.app.Lang))
	case 'w', 'W':
		windows := []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour, 30 * 24 * time.Hour}
		for i, w := range windows {
			if w == s.window {
				s.window = windows[(i+1)%len(windows)]
				break
			}
		}
	default:
		return event
	}
	s.app.renderNav()
	go s.refresh(context.Background())
	return nil
}

func (s *usageScreen) refresh(ctx context.Context) {
	spec := usageCatalogue(s.app.Lang)[s.current]
	granularity := "hour"
	if s.window > 14*24*time.Hour {
		granularity = "day"
	}

	points, _ := s.app.DB.MetricSeries(ctx, store.MetricQuery{
		Source: spec.source, Metric: spec.metric, Since: since(s.window),
		Granularity: granularity, TZOffset: tzOffsetMinutes(), Aggregate: spec.agg,
	})
	top, _ := s.app.DB.MetricTop(ctx, spec.source, spec.metric, since(s.window), 12)
	cells, _ := s.app.DB.UsageHeatmap(ctx, spec.source, spec.metric, "", since(s.window), tzOffsetMinutes())

	// Collapse per-subject points into one total series for the trend line.
	totals := map[string]float64{}
	var order []string
	for _, p := range points {
		if _, seen := totals[p.Bucket]; !seen {
			order = append(order, p.Bucket)
		}
		totals[p.Bucket] += p.Value
	}
	series := make([]float64, 0, len(order))
	for _, b := range order {
		series = append(series, totals[b])
	}

	s.app.queue(func() {
		specLabel := s.app.T(spec.labelKey)
		s.trend.SetTitle(fmt.Sprintf(" %s — %s ", specLabel, windowLabel(s.app.Lang, s.window)))
		var tb strings.Builder
		if len(series) == 0 {
			tb.WriteString("\n " + dim(s.app.T("tui.monitor.usage.noDataLine1")))
			tb.WriteString("\n " + dim(s.app.T("tui.monitor.usage.noDataLine2")))
		} else {
			sum, max := 0.0, 0.0
			for _, v := range series {
				sum += v
				if v > max {
					max = v
				}
			}
			tb.WriteString("\n " + tag(hexSeries1, sparkline(s.app.Lang, series, 96)) + "\n\n")
			step := map[string]string{"hour": s.app.T("tui.monitor.usage.stepHour"), "day": s.app.T("tui.monitor.usage.stepDay")}[granularity]
			tb.WriteString(dim(" " + s.app.T("tui.monitor.usage.totalPeakPointsStep",
				spec.format(sum), spec.format(max), len(series), step)))
			if s.app.Cfg.IsFixtures() {
				tb.WriteString("\n " + tag(hexWarning, s.app.T("tui.monitor.usage.fixturesWarning")))
			}
		}
		s.trend.SetText(tb.String())

		var bb strings.Builder
		if len(top) == 0 {
			bb.WriteString("\n " + dim(s.app.T("tui.monitor.usage.noDataInPeriod")))
		} else {
			maxTotal := top[0].Total
			if maxTotal == 0 {
				maxTotal = 1
			}
			for _, row := range top {
				bb.WriteString(fmt.Sprintf(" %-26s %s %s\n",
					truncate(row.Subject, 26),
					hbar(row.Total/maxTotal, 28, hexSeries1),
					dim(spec.format(row.Total))))
			}
		}
		s.top.SetText(bb.String())
		s.top.SetTitle(fmt.Sprintf(" %s ", s.app.T("tui.monitor.usage.topTitleWithMetric", specLabel)))

		s.heat.SetText(heatmap(s.app.Lang, cells,
			func(c store.HeatCell) float64 { return c.Value },
			s.app.T("tui.monitor.usage.loadScaleLabel"), spec.format))
	})
}

// -------------------------------------------------------------------- audit

type auditScreen struct {
	app *App

	table *tview.Table
	jobs  *tview.TextView
	root  *tview.Flex
}

func newAuditScreen(a *App) *auditScreen {
	s := &auditScreen{app: a}
	s.table = newTable(a.T("tui.configs.colWhen"), a.T("tui.configs.colWho"), a.T("tui.monitor.audit.colAction"),
		a.T("tui.monitor.audit.colTarget"), a.T("tui.monitor.audit.colResult"), a.T("tui.monitor.audit.colDetails"))
	s.table.SetTitle(fmt.Sprintf(" %s ", a.T("tui.monitor.audit.tableTitle")))
	s.jobs = newPanel(a.T("tui.monitor.audit.jobsTitle"))

	s.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.table, 0, 1, true).
		AddItem(s.jobs, 9, 0, false)
	return s
}

func (s *auditScreen) title() string          { return s.app.T("tui.monitor.audit.title") }
func (s *auditScreen) view() tview.Primitive  { return s.root }
func (s *auditScreen) focus() tview.Primitive { return s.table }
func (s *auditScreen) hints() string          { return dim(s.app.T("tui.monitor.audit.hints")) }

func (s *auditScreen) refresh(ctx context.Context) {
	entries, err := s.app.DB.ListAudit(ctx, store.AuditFilter{Limit: 300})
	if err != nil {
		return
	}

	s.app.queue(func() {
		s.table.Clear()
		headers := []string{
			s.app.T("tui.configs.colWhen"), s.app.T("tui.configs.colWho"), s.app.T("tui.monitor.audit.colAction"),
			s.app.T("tui.monitor.audit.colTarget"), s.app.T("tui.monitor.audit.colResult"), s.app.T("tui.monitor.audit.colDetails"),
		}
		for i, h := range headers {
			s.table.SetCell(0, i, tview.NewTableCell(" "+h).
				SetTextColor(colorSecondary).SetSelectable(false).SetAttributes(tcell.AttrBold))
		}
		for i, e := range entries {
			row := i + 1
			s.table.SetCell(row, 0, cellDim(shortTime(e.TS)))
			s.table.SetCell(row, 1, cell(e.Username))
			s.table.SetCell(row, 2, cell(e.Action))
			s.table.SetCell(row, 3, cellDim(truncate(e.Target, 32)))
			tone := hexGood
			if e.Result != "ok" {
				tone = hexCritical
			}
			s.table.SetCell(row, 4, cellColor("●"+e.Result, tone))
			s.table.SetCell(row, 5, cellDim(truncate(strings.ReplaceAll(e.Detail, "\n", " "), 60)))
		}
		s.table.SetSelectedFunc(func(row, _ int) {
			idx := row - 1
			if idx < 0 || idx >= len(entries) {
				return
			}
			e := entries[idx]
			body := fmt.Sprintf(" %s\n %s\n\n %s %s\n %s %s\n %s %s\n\n%s",
				bold(e.Action), dim(shortTime(e.TS)),
				dim(s.app.T("tui.monitor.audit.userLabel")), e.Username,
				dim(s.app.T("tui.monitor.audit.targetLabel")), orDash(e.Target),
				dim(s.app.T("tui.monitor.audit.resultLabel")), e.Result,
				tview.Escape(e.Detail))
			s.app.showText("audit", s.app.T("tui.monitor.audit.entryTitle"), body)
		})
		s.table.SetTitle(fmt.Sprintf(" %s ", s.app.T("tui.monitor.audit.tableTitleWithCount", len(entries))))

		var sb strings.Builder
		sb.WriteString(dim(" " + s.app.T("tui.monitor.audit.schedulerNote1") + "\n"))
		sb.WriteString(dim(" " + s.app.T("tui.monitor.audit.schedulerNote2") + "\n\n"))
		sb.WriteString(fmt.Sprintf(" %-14s %-10s %-16s %s\n",
			dim(s.app.T("tui.monitor.audit.intervalsLabel")), dim(s.app.T("tui.monitor.audit.probesLabel")),
			dim(s.app.T("tui.monitor.audit.metricsLabel")), dim(s.app.T("tui.monitor.audit.inventoryLabel"))))
		sb.WriteString(fmt.Sprintf(" %-14s %-10s %-16s %s\n", "",
			s.app.Cfg.ProbeInterval, s.app.Cfg.MetricsInterval, s.app.Cfg.InventoryInterval))
		if s.app.Cfg.AutoRenewCerts && s.app.Cfg.AllowMutations {
			sb.WriteString(fmt.Sprintf(" %s %s\n",
				dim(s.app.T("tui.monitor.audit.autoRenewLabel")),
				s.app.T("tui.monitor.audit.autoRenewTemplate", s.app.Cfg.AutoRenewCertsInterval,
					int(s.app.Cfg.AutoRenewCertsWithin.Hours()/24))))
		}
		s.jobs.SetText(sb.String())
	})
}
