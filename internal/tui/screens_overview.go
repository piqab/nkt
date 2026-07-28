package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/topology"
)

// ------------------------------------------------------------------- overview

type overviewScreen struct {
	app *App

	tiles    [4]*tview.TextView
	findings *tview.TextView
	services *tview.Table
	firewall *tview.TextView
	health   *tview.TextView
	root     *tview.Flex
}

func newOverviewScreen(a *App) *overviewScreen {
	s := &overviewScreen{app: a}

	tileRow := tview.NewFlex()
	for i := range s.tiles {
		s.tiles[i] = newPanel("")
		tileRow.AddItem(s.tiles[i], 0, 1, false)
	}

	s.findings = newPanel("Что требует внимания")
	s.findings.SetScrollable(true)
	s.services = newTable("Сервис", "Состояние", "Автозапуск", "PID", "Память")
	s.services.SetTitle(" Сервисы ")

	s.firewall = newPanel("Firewall")
	s.health = newPanel("Доступность")

	middle := tview.NewFlex().
		AddItem(s.findings, 0, 3, false).
		AddItem(s.services, 0, 2, true)

	bottom := tview.NewFlex().
		AddItem(s.firewall, 0, 1, false).
		AddItem(s.health, 0, 1, false)

	s.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tileRow, 5, 0, false).
		AddItem(middle, 0, 1, true).
		AddItem(bottom, 11, 0, false)
	return s
}

func (s *overviewScreen) title() string          { return "Обзор" }
func (s *overviewScreen) view() tview.Primitive  { return s.root }
func (s *overviewScreen) focus() tview.Primitive { return s.services }
func (s *overviewScreen) hints() string          { return dim("Enter подробности сервиса") }

func (s *overviewScreen) refresh(ctx context.Context) {
	snap, err := s.app.Scanner.LatestOrScan(ctx)
	if err != nil || snap == nil {
		s.app.queue(func() {
			s.app.setStatus(hexCritical, "не удалось прочитать состояние хоста")
		})
		return
	}
	statuses, _ := s.app.DB.TargetStatuses(ctx)
	outages, _ := s.app.DB.RecentOutages(ctx, since(24*time.Hour), 4)

	counts := snap.FindingCounts()
	worst := counts[model.SeverityCritical] + counts[model.SeverityHigh]

	publicCount, tlsCount := 0, 0
	for _, e := range snap.Endpoints {
		if e.Public() {
			publicCount++
		}
		if e.TLS {
			tlsCount++
		}
	}
	running := 0
	for _, c := range snap.Container {
		if c.Running {
			running++
		}
	}
	up, down, uptimeSum, uptimeN := 0, 0, 0.0, 0
	for _, st := range statuses {
		if st.LastOK != nil {
			if *st.LastOK {
				up++
			} else {
				down++
			}
		}
		if st.Checks24h > 0 {
			uptimeSum += st.Uptime24h
			uptimeN++
		}
	}
	avgUptime := 0.0
	if uptimeN > 0 {
		avgUptime = uptimeSum / float64(uptimeN)
	}

	s.app.queue(func() {
		findingTone := hexGood
		if worst > 0 {
			findingTone = hexCritical
		} else if len(snap.Findings) > 0 {
			findingTone = hexWarning
		}
		s.tiles[0].SetText(statTile("Требуют внимания", fmt.Sprintf("%d", worst),
			fmt.Sprintf("критичных %d, высоких %d", counts[model.SeverityCritical], counts[model.SeverityHigh]),
			findingTone))
		s.tiles[1].SetText(statTile("Слушателей объявлено", fmt.Sprintf("%d", len(snap.Endpoints)),
			fmt.Sprintf("публичных %d, с TLS %d", publicCount, tlsCount), hexSeries1))
		containerTone := hexGood
		if running < len(snap.Container) {
			containerTone = hexWarning
		}
		s.tiles[2].SetText(statTile("Контейнеры", fmt.Sprintf("%d/%d", running, len(snap.Container)),
			fmt.Sprintf("сетей docker: %d", len(snap.Networks)), containerTone))
		// With no probe history at all, a bare "0.0%" would read as "всё лежит";
		// say plainly that nothing has been measured yet instead.
		uptimeTone, uptimeValue := hexGood, fmt.Sprintf("%.1f%%", avgUptime)
		uptimeNote := fmt.Sprintf("целей %d: живы %d, мертвы %d", len(statuses), up, down)
		switch {
		case uptimeN == 0:
			uptimeTone, uptimeValue = hexMuted, "нет данных"
			uptimeNote = fmt.Sprintf("целей %d, проверок ещё не было", len(statuses))
		case down > 0:
			uptimeTone = hexWarning
		}
		s.tiles[3].SetText(statTile("Доступность за 24 ч", uptimeValue, uptimeNote, uptimeTone))

		// Findings, worst first.
		var sb strings.Builder
		if len(snap.Findings) == 0 {
			sb.WriteString("\n " + tag(hexGood, "Проблем не найдено."))
		}
		for i, f := range snap.Findings {
			if i >= 14 {
				sb.WriteString(dim(fmt.Sprintf("\n  …ещё %d, см. экран «Проблемы»", len(snap.Findings)-i)))
				break
			}
			sb.WriteString(fmt.Sprintf(" %s %s\n   %s\n",
				tag(severityColor(f.Severity), "●"+severityLabel(f.Severity)),
				bold(truncate(f.Title, 78)),
				dim(truncate(f.Detail, 150))))
		}
		s.findings.SetText(sb.String()).ScrollToBeginning()

		s.services.Clear()
		headers := []string{"Сервис", "Состояние", "Автозапуск", "PID", "Память"}
		for i, h := range headers {
			s.services.SetCell(0, i, tview.NewTableCell(" "+h).
				SetTextColor(colorSecondary).SetSelectable(false).SetAttributes(tcell.AttrBold))
		}
		for i, svc := range snap.Services {
			row := i + 1
			s.services.SetCell(row, 0, cell(svc.Name))
			s.services.SetCell(row, 1, cellColor("●"+svc.ActiveState, stateColor(svc.ActiveState)))
			s.services.SetCell(row, 2, cellDim(orDash(svc.Enabled)))
			s.services.SetCell(row, 3, cellRight(intOrDash(svc.MainPID)))
			mem := "—"
			if svc.MemoryBytes > 0 {
				mem = formatBytes(float64(svc.MemoryBytes))
			}
			s.services.SetCell(row, 4, cellRight(mem))
		}

		fw := snap.Firewall
		var fb strings.Builder
		fb.WriteString(" ufw: " + tag(stateColor(boolState(fw.UFWActive)), boolState(fw.UFWActive)) + "\n")
		if fw.UFWPolicy != "" {
			fb.WriteString(" " + dim(fw.UFWPolicy) + "\n")
		}
		fb.WriteString("\n")
		for _, p := range fw.Policies {
			if p.Table != "filter" || p.Policy == "-" {
				continue
			}
			tone := hexWarning
			if p.Policy == "DROP" || p.Policy == "REJECT" {
				tone = hexGood
			}
			fb.WriteString(fmt.Sprintf(" %-22s %s  %s\n",
				p.Backend+"/"+p.Chain, tag(tone, p.Policy),
				dim(fmt.Sprintf("%s пакетов", formatCount(float64(p.Packets))))))
		}
		fb.WriteString(dim(fmt.Sprintf("\n правил всего: %d", len(fw.Rules))))
		s.firewall.SetText(fb.String())

		var hb strings.Builder
		hb.WriteString(fmt.Sprintf(" целей %d, недоступно сейчас %s\n\n",
			len(statuses), tag(map[bool]string{true: hexCritical, false: hexGood}[down > 0], fmt.Sprintf("%d", down))))
		if len(outages) == 0 {
			hb.WriteString(dim(" за сутки простоев не зафиксировано"))
		} else {
			hb.WriteString(dim(" последние простои:\n"))
			for _, o := range outages {
				hb.WriteString(fmt.Sprintf("  %s %s %s\n",
					tag(hexCritical, "●"), truncate(o.Label, 34), dim(shortTime(o.Start))))
			}
		}
		s.health.SetText(hb.String())
	})
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func intOrDash(n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprintf("%d", n)
}

func boolState(b bool) string {
	if b {
		return "active"
	}
	return "inactive"
}

// ------------------------------------------------------------------- findings

type findingsScreen struct {
	app *App

	table  *tview.Table
	detail *tview.TextView
	root   *tview.Flex
	items  []model.Finding
	filter string
	counts map[string]int
}

func newFindingsScreen(a *App) *findingsScreen {
	s := &findingsScreen{app: a, counts: map[string]int{}}
	s.table = newTable("Серьёзность", "Правило", "Объект", "Что не так")
	s.detail = newPanel("Подробности")
	s.detail.SetScrollable(true)

	s.table.SetSelectionChangedFunc(func(row, _ int) { s.showDetail(row - 1) })
	s.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'f', 'F':
			s.cycleFilter()
			return nil
		}
		return event
	})

	s.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.table, 0, 2, true).
		AddItem(s.detail, 12, 0, false)
	return s
}

func (s *findingsScreen) title() string          { return "Проблемы" }
func (s *findingsScreen) view() tview.Primitive  { return s.root }
func (s *findingsScreen) focus() tview.Primitive { return s.table }

func (s *findingsScreen) hints() string {
	label := "все"
	if s.filter != "" {
		label = severityLabel(s.filter)
	}
	return dim("f фильтр: ") + tag(hexSeries1, label)
}

func (s *findingsScreen) cycleFilter() {
	order := []string{"", model.SeverityCritical, model.SeverityHigh, model.SeverityMedium,
		model.SeverityLow, model.SeverityInfo}
	idx := 0
	for i, v := range order {
		if v == s.filter {
			idx = i
			break
		}
	}
	s.filter = order[(idx+1)%len(order)]
	s.app.renderNav()
	go s.refresh(context.Background())
}

func (s *findingsScreen) refresh(ctx context.Context) {
	snap, err := s.app.Scanner.LatestOrScan(ctx)
	if err != nil || snap == nil {
		return
	}
	items := make([]model.Finding, 0, len(snap.Findings))
	for _, f := range snap.Findings {
		if s.filter == "" || f.Severity == s.filter {
			items = append(items, f)
		}
	}

	s.app.queue(func() {
		s.items = items
		s.counts = snap.FindingCounts()
		s.table.Clear()
		for i, h := range []string{"Серьёзность", "Правило", "Объект", "Что не так"} {
			s.table.SetCell(0, i, tview.NewTableCell(" "+h).
				SetTextColor(colorSecondary).SetSelectable(false).SetAttributes(tcell.AttrBold))
		}
		for i, f := range items {
			row := i + 1
			s.table.SetCell(row, 0, cellColor("●"+severityLabel(f.Severity), severityColor(f.Severity)))
			s.table.SetCell(row, 1, cellDim(f.Rule))
			s.table.SetCell(row, 2, cell(truncate(f.Object, 30)))
			s.table.SetCell(row, 3, cell(truncate(f.Title, 70)))
		}
		s.table.SetTitle(fmt.Sprintf(" Проблемы — показано %d из %d ", len(items), len(snap.Findings)))
		if len(items) > 0 {
			s.table.Select(1, 0)
			s.showDetail(0)
		} else {
			s.detail.SetText("\n " + tag(hexGood, "Под выбранный фильтр ничего не подходит."))
		}
	})
}

func (s *findingsScreen) showDetail(index int) {
	if index < 0 || index >= len(s.items) {
		return
	}
	f := s.items[index]
	var sb strings.Builder
	sb.WriteString(" " + tag(severityColor(f.Severity), "●"+severityLabel(f.Severity)) + "  " + bold(f.Title) + "\n\n")
	sb.WriteString(" " + f.Detail + "\n")
	if f.Suggestion != "" {
		sb.WriteString("\n " + bold("Что сделать: ") + f.Suggestion + "\n")
	}
	sb.WriteString("\n " + dim(fmt.Sprintf("правило %s · сервис %s", f.Rule, f.Service)))
	if f.Object != "" {
		sb.WriteString(dim(" · объект " + f.Object))
	}
	if f.File != "" {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		sb.WriteString("\n " + dim("файл ") + loc)
	}
	s.detail.SetText(sb.String()).ScrollToBeginning()
}

// ------------------------------------------------------------------------ map

type mapScreen struct {
	app  *App
	tree *tview.TreeView
	info *tview.TextView
	root *tview.Flex
}

func newMapScreen(a *App) *mapScreen {
	s := &mapScreen{app: a}
	s.tree = tview.NewTreeView()
	s.tree.SetBorder(true).SetTitle(" Карта сетевых ресурсов ").SetBorderColor(colorBorder)
	s.info = newPanel("Узел")

	s.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.tree, 0, 3, true).
		AddItem(s.info, 8, 0, false)
	return s
}

func (s *mapScreen) title() string          { return "Карта" }
func (s *mapScreen) view() tview.Primitive  { return s.root }
func (s *mapScreen) focus() tview.Primitive { return s.tree }
func (s *mapScreen) hints() string {
	return dim("Enter свернуть и развернуть ветку")
}

func (s *mapScreen) refresh(ctx context.Context) {
	snap, err := s.app.Scanner.LatestOrScan(ctx)
	if err != nil || snap == nil {
		return
	}
	graph := topology.Build(snap)

	byID := map[string]*topology.Node{}
	for i := range graph.Nodes {
		byID[graph.Nodes[i].ID] = &graph.Nodes[i]
	}
	outgoing := map[string][]topology.Edge{}
	for _, e := range graph.Edges {
		outgoing[e.From] = append(outgoing[e.From], e)
	}

	root := tview.NewTreeNode(bold(snap.Host.Hostname) + dim(" · "+snap.Host.OS)).
		SetColor(colorText).SetSelectable(false)

	// Traffic reads outward from the internet, then everything reachable only
	// from inside the host, so the tree matches the direction requests travel.
	addSection := func(title string, ids []string) {
		if len(ids) == 0 {
			return
		}
		section := tview.NewTreeNode(bold(title)).SetColor(colorSecondary).SetSelectable(false)
		for _, id := range ids {
			if n := byID[id]; n != nil {
				section.AddChild(s.buildNode(n, byID, outgoing, map[string]bool{}, 0))
			}
		}
		root.AddChild(section)
	}

	var public, internal, orphanContainers []string
	fromInternet := map[string]bool{}
	for _, e := range outgoing["internet"] {
		fromInternet[e.To] = true
	}
	for i := range graph.Nodes {
		n := graph.Nodes[i]
		switch n.Kind {
		case topology.KindEndpoint:
			if fromInternet[n.ID] {
				public = append(public, n.ID)
			} else {
				internal = append(internal, n.ID)
			}
		case topology.KindContainer:
			orphanContainers = append(orphanContainers, n.ID)
		}
	}
	sort.Strings(public)
	sort.Strings(internal)
	sort.Strings(orphanContainers)

	addSection("Доступно из внешней сети", public)
	addSection("Только изнутри хоста", internal)
	addSection("Контейнеры", orphanContainers)

	s.app.queue(func() {
		s.tree.SetRoot(root).SetCurrentNode(root)
		s.info.SetText(dim("\n Выберите узел, чтобы увидеть подробности."))
		s.tree.SetChangedFunc(func(node *tview.TreeNode) {
			if n, ok := node.GetReference().(*topology.Node); ok {
				s.showNode(n, outgoing[n.ID], byID)
			}
		})
	})
}

// buildNode renders one graph node and, recursively, everything it points at.
func (s *mapScreen) buildNode(n *topology.Node, byID map[string]*topology.Node,
	outgoing map[string][]topology.Edge, visited map[string]bool, depth int) *tview.TreeNode {

	label := s.nodeLabel(n)
	node := tview.NewTreeNode(label).SetReference(n).SetColor(tcell.GetColor(statusHex(n.Status)))

	// A cycle or an over-deep chain would make the tree useless; both are cut.
	if visited[n.ID] || depth > 6 {
		return node
	}
	visited[n.ID] = true
	defer delete(visited, n.ID)

	edges := outgoing[n.ID]
	sort.Slice(edges, func(i, j int) bool { return edges[i].To < edges[j].To })
	for _, e := range edges {
		child := byID[e.To]
		if child == nil {
			continue
		}
		sub := s.buildNode(child, byID, outgoing, visited, depth+1)
		if e.Label != "" {
			sub.SetText(dim(e.Kind+" "+e.Label+" → ") + s.nodeLabel(child))
		} else {
			sub.SetText(dim(e.Kind+" → ") + s.nodeLabel(child))
		}
		node.AddChild(sub)
	}
	if len(node.GetChildren()) > 0 {
		node.SetExpanded(depth < 2)
	}
	return node
}

func (s *mapScreen) nodeLabel(n *topology.Node) string {
	label := tag(statusHex(n.Status), "●") + " " + n.Label
	if n.Sublabel != "" {
		label += dim(" · " + truncate(n.Sublabel, 42))
	}
	if n.Findings > 0 {
		label += " " + tag(severityColor(n.Severity), fmt.Sprintf("[%d]", n.Findings))
	}
	return label
}

func (s *mapScreen) showNode(n *topology.Node, edges []topology.Edge, byID map[string]*topology.Node) {
	var sb strings.Builder
	sb.WriteString(" " + bold(n.Label) + dim("  ("+n.Kind+")") + "\n")
	if n.Sublabel != "" {
		sb.WriteString(" " + dim(n.Sublabel) + "\n")
	}
	sb.WriteString(" состояние: " + tag(statusHex(n.Status), n.Status))
	if n.Findings > 0 {
		sb.WriteString(dim(fmt.Sprintf("   проблем: %d (худшая: %s)", n.Findings, n.Severity)))
	}
	sb.WriteString("\n")

	keys := make([]string, 0, len(n.Meta))
	for k, v := range n.Meta {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(dim(" "+k+": ") + truncate(n.Meta[k], 90) + "\n")
	}
	if len(edges) > 0 {
		sb.WriteString(dim(" ведёт к: "))
		var names []string
		for _, e := range edges {
			if to := byID[e.To]; to != nil {
				names = append(names, to.Label)
			}
		}
		sb.WriteString(truncate(strings.Join(names, ", "), 90))
	}
	s.info.SetText(sb.String())
}

func statusHex(status string) string {
	switch status {
	case topology.StatusOK:
		return hexGood
	case topology.StatusWarn:
		return hexWarning
	case topology.StatusError:
		return hexCritical
	default:
		return hexMuted
	}
}
