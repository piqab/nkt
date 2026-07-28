package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/althq/netknownsthat/internal/monitor"
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
	s.table = newTable("Ресурс", "Адрес", "Сейчас", "Доступность 24ч", "Задержка", "Проверок")
	s.heat = newPanel("Недоступность по часам недели")
	s.detail = newPanel("Ресурс")

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

func (s *availabilityScreen) title() string          { return "Доступность" }
func (s *availabilityScreen) view() tview.Primitive  { return s.root }
func (s *availabilityScreen) focus() tview.Primitive { return s.table }

func (s *availabilityScreen) hints() string {
	return dim("p проверить сейчас · space пауза · w период: ") + tag(hexSeries1, windowLabel(s.window))
}

func windowLabel(d time.Duration) string {
	switch d {
	case 24 * time.Hour:
		return "сутки"
	case 7 * 24 * time.Hour:
		return "7 дней"
	case 14 * 24 * time.Hour:
		return "14 дней"
	default:
		return "30 дней"
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
	s.app.runAsync("Проверяю "+target.Label, false, func(ctx context.Context) (string, error) {
		result := s.app.Prober.ProbeTarget(ctx, target.Target)
		if err := s.app.DB.InsertProbeResults(ctx, []store.ProbeResult{result}); err != nil {
			return "", err
		}
		if !result.OK {
			return "", fmt.Errorf("%s недоступен: %s", target.Label, result.Error)
		}
		return fmt.Sprintf("%s отвечает за %s", target.Label, formatMS(result.LatencyMS)), nil
	})
}

func (s *availabilityScreen) toggleEnabled() {
	target, ok := s.selected()
	if !ok {
		return
	}
	want := !target.Enabled
	verb := "возобновляю проверки"
	if !want {
		verb = "ставлю проверки на паузу"
	}
	s.app.runAsync(verb+" для "+target.Label, true, func(ctx context.Context) (string, error) {
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
		for i, h := range []string{"Ресурс", "Адрес", "Сейчас", "Доступность 24ч", "Задержка", "Проверок"} {
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

			state, tone := "нет данных", hexMuted
			if t.LastOK != nil {
				if *t.LastOK {
					state, tone = "доступен", hexGood
				} else {
					state, tone = "недоступен", hexCritical
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
				latency = formatMS(t.LastLatency)
			}
			s.table.SetCell(row, 4, cellRight(latency))
			s.table.SetCell(row, 5, cellRight(fmt.Sprintf("%d", t.Checks24h)))
		}
		s.table.SetTitle(fmt.Sprintf(" Ресурсы под наблюдением — %d ", len(statuses)))

		// The heatmap shows downtime, not uptime: the eye is drawn to dark
		// cells, and "когда было недоступно" is the question being asked.
		s.heat.SetText(heatmap(cells,
			func(c store.HeatCell) float64 {
				if c.Total == 0 {
					return 0
				}
				return 100 - c.Uptime
			},
			"недоступность",
			func(v float64) string { return fmt.Sprintf("%.1f%%", v) }))
		s.heat.SetTitle(fmt.Sprintf(" Недоступность по часам недели — %s ", windowLabel(s.window)))

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
		sb.WriteString(dim(" источник: ") + t.Source + dim("   проверок за сутки: ") +
			fmt.Sprintf("%d", t.Checks24h) + dim("   сбоев: ") + fmt.Sprintf("%d", t.Failures24h) + "\n")
		if t.LastError != "" {
			sb.WriteString(" " + tag(hexCritical, "последняя ошибка: "+truncate(t.LastError, 60)) + "\n")
		}
		sb.WriteString(dim(" последняя проверка: ") + relativeTime(t.LastCheck) + "\n\n")

		sb.WriteString(dim(" доступность  ") + tag(hexGood, sparkline(uptimes, 64)) + "\n")
		sb.WriteString(dim(" задержка     ") + tag(hexSeries1, sparkline(latencies, 64)) + "\n")
		if len(buckets) > 0 {
			sb.WriteString(dim(fmt.Sprintf("              %s … %s, по часам\n",
				shortTime(bucketToTS(buckets[0].Bucket)), shortTime(bucketToTS(buckets[len(buckets)-1].Bucket)))))
		}

		if len(mine) > 0 {
			sb.WriteString("\n" + dim(fmt.Sprintf(" простои за %s:\n", windowLabel(s.window))))
			for i, o := range mine {
				if i >= 5 {
					sb.WriteString(dim(fmt.Sprintf("  …ещё %d\n", len(mine)-i)))
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
	label  string
	source string
	metric string
	agg    string
	format func(float64) string
}

var usageCatalogue = []usageSeries{
	{"Сетевой трафик контейнеров", monitor.SourceDocker, "net_rx_bytes", "sum", formatBytes},
	{"CPU контейнеров", monitor.SourceDocker, "cpu_pct", "avg", func(v float64) string { return fmt.Sprintf("%.1f%%", v) }},
	{"Память контейнеров", monitor.SourceDocker, "mem_bytes", "avg", formatBytes},
	{"Трафик по правилам firewall", monitor.SourceIptables, "bytes", "sum", formatBytes},
	{"Запросы nginx", monitor.SourceNginxLog, "requests", "sum", formatCount},
	{"Ошибки 5xx у nginx", monitor.SourceNginxLog, "errors_5xx", "sum", formatCount},
	{"Запросы haproxy", monitor.SourceHAProxyLog, "requests", "sum", formatCount},
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
	s.trend = newPanel("Динамика")
	s.top = newPanel("Кто нагружает больше всех")
	s.heat = newPanel("Расписание использования")

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

func (s *usageScreen) title() string          { return "Нагрузка" }
func (s *usageScreen) view() tview.Primitive  { return s.root }
func (s *usageScreen) focus() tview.Primitive { return s.top }

func (s *usageScreen) hints() string {
	return dim("m показатель: ") + tag(hexSeries1, usageCatalogue[s.current].label) +
		dim("  w период: ") + tag(hexSeries1, windowLabel(s.window))
}

func (s *usageScreen) onKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 'm', 'M':
		s.current = (s.current + 1) % len(usageCatalogue)
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
	spec := usageCatalogue[s.current]
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
		s.trend.SetTitle(fmt.Sprintf(" %s — %s ", spec.label, windowLabel(s.window)))
		var tb strings.Builder
		if len(series) == 0 {
			tb.WriteString("\n " + dim("Данных за период нет. Метрики собирает фоновый планировщик — "))
			tb.WriteString("\n " + dim("запустите сервис netknownsthat или нажмите r для скана."))
		} else {
			sum, max := 0.0, 0.0
			for _, v := range series {
				sum += v
				if v > max {
					max = v
				}
			}
			tb.WriteString("\n " + tag(hexSeries1, sparkline(series, 96)) + "\n\n")
			tb.WriteString(dim(fmt.Sprintf(" всего %s   пик %s   точек %d   шаг %s",
				spec.format(sum), spec.format(max), len(series),
				map[string]string{"hour": "час", "day": "сутки"}[granularity])))
			if s.app.Cfg.IsFixtures() {
				tb.WriteString("\n " + tag(hexWarning,
					"режим снапшота: значения синтетические, а не измеренные"))
			}
		}
		s.trend.SetText(tb.String())

		var bb strings.Builder
		if len(top) == 0 {
			bb.WriteString("\n " + dim("Нет данных за период."))
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
		s.top.SetTitle(fmt.Sprintf(" Кто нагружает больше всех — %s ", spec.label))

		s.heat.SetText(heatmap(cells,
			func(c store.HeatCell) float64 { return c.Value },
			"нагрузка", spec.format))
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
	s.table = newTable("Когда", "Кто", "Действие", "Объект", "Результат", "Подробности")
	s.table.SetTitle(" Журнал действий ")
	s.jobs = newPanel("Фоновые задачи")

	s.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.table, 0, 1, true).
		AddItem(s.jobs, 9, 0, false)
	return s
}

func (s *auditScreen) title() string          { return "Журнал" }
func (s *auditScreen) view() tview.Primitive  { return s.root }
func (s *auditScreen) focus() tview.Primitive { return s.table }
func (s *auditScreen) hints() string          { return dim("Enter полный текст записи") }

func (s *auditScreen) refresh(ctx context.Context) {
	entries, err := s.app.DB.ListAudit(ctx, store.AuditFilter{Limit: 300})
	if err != nil {
		return
	}

	s.app.queue(func() {
		s.table.Clear()
		for i, h := range []string{"Когда", "Кто", "Действие", "Объект", "Результат", "Подробности"} {
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
				dim("пользователь:"), e.Username,
				dim("объект:      "), orDash(e.Target),
				dim("результат:   "), e.Result,
				tview.Escape(e.Detail))
			s.app.showText("audit", "Запись журнала", body)
		})
		s.table.SetTitle(fmt.Sprintf(" Журнал действий — %d записей ", len(entries)))

		var sb strings.Builder
		sb.WriteString(dim(" Планировщик собирает историю только когда запущен сервис netknownsthat.\n"))
		sb.WriteString(dim(" Терминальный интерфейс читает ту же базу и ничего не собирает сам.\n\n"))
		sb.WriteString(fmt.Sprintf(" %-14s %-10s %-16s %s\n",
			dim("интервалы:"), dim("пробы"), dim("метрики"), dim("инвентаризация")))
		sb.WriteString(fmt.Sprintf(" %-14s %-10s %-16s %s\n", "",
			s.app.Cfg.ProbeInterval, s.app.Cfg.MetricsInterval, s.app.Cfg.InventoryInterval))
		s.jobs.SetText(sb.String())
	})
}
