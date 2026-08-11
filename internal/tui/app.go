package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/monitor"
	"github.com/althq/netknownsthat/internal/store"
)

// screen is one page of the interface.
type screen interface {
	// title appears in the footer navigation.
	title() string
	// view is the primitive shown in the content area.
	view() tview.Primitive
	// refresh reloads the screen's data. It runs off the UI thread, so any
	// widget mutation inside must go through App.queue.
	refresh(ctx context.Context)
	// hints lists the keys this screen adds to the global ones.
	hints() string
	// focus returns the primitive that should receive focus on entry.
	focus() tview.Primitive
}

// Deps bundles everything the terminal interface drives.
type Deps struct {
	Cfg       *config.Config
	DB        *store.DB
	Collector collect.Collector
	Scanner   *inventory.Scanner
	Services  *control.ServiceManager
	Configs   *control.ConfigManager
	Firewall  *control.FirewallManager
	Certs     *control.CertManager
	Podman    *control.PodmanManager
	LXD       *control.LXDManager
	Libvirt   *control.LibvirtManager
	Prober    *monitor.Prober

	// Screen overrides the terminal. Tests drive the interface through a
	// simulation screen; in normal use this stays nil and tcell picks the
	// real terminal.
	Screen tcell.Screen
}

// App is the terminal application.
type App struct {
	Deps

	tv      *tview.Application
	pages   *tview.Pages
	header  *tview.TextView
	status  *tview.TextView
	nav     *tview.TextView
	screens []screen
	current int

	// actor identifies who performed an action in the audit log.
	actor string
	// editing suppresses global single-key shortcuts while text is being typed.
	editing bool
	// busy counts operations in flight, for the header indicator.
	busy int
}

// Run starts the terminal interface and blocks until the user quits.
func Run(ctx context.Context, deps Deps) error {
	a := &App{Deps: deps, tv: tview.NewApplication(), actor: actorName()}

	a.header = tview.NewTextView().SetDynamicColors(true)
	a.status = tview.NewTextView().SetDynamicColors(true)
	a.nav = tview.NewTextView().SetDynamicColors(true)
	a.pages = tview.NewPages()

	a.screens = []screen{
		newOverviewScreen(a),
		newFindingsScreen(a),
		newMapScreen(a),
		newAvailabilityScreen(a),
		newUsageScreen(a),
		newConfigsScreen(a),
		newServicesScreen(a),
		newFirewallScreen(a),
		newCertsScreen(a),
		newAuditScreen(a),
	}
	for i, s := range a.screens {
		a.pages.AddPage(pageName(i), s.view(), true, i == 0)
	}

	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.status, 1, 0, false).
		AddItem(a.nav, 1, 0, false)

	if deps.Screen != nil {
		a.tv.SetScreen(deps.Screen)
	}
	a.tv.SetRoot(root, true).EnableMouse(true).SetInputCapture(a.onKey)

	a.renderHeader()
	a.renderNav()
	a.setStatus(hexMuted, "Загружаю состояние хоста…")

	// The first scan happens in the background so the interface appears at once.
	go a.refreshAll(ctx)

	// A slow periodic refresh keeps the screen honest without hammering the host.
	go a.autoRefresh(ctx)

	return a.tv.Run()
}

func pageName(i int) string { return fmt.Sprintf("page-%d", i) }

// actorName identifies the operator for the audit log. Under sudo the original
// login is far more useful than "root".
func actorName() string {
	name := os.Getenv("SUDO_USER")
	if name == "" {
		name = os.Getenv("USER")
	}
	if name == "" {
		name = os.Getenv("USERNAME")
	}
	if name == "" {
		name = "unknown"
	}
	return "tui:" + name
}

// ---------------------------------------------------------------- navigation

func (a *App) onKey(event *tcell.EventKey) *tcell.EventKey {
	// While a modal is up, only Escape is global; everything else belongs to it.
	if front, _ := a.pages.GetFrontPage(); strings.HasPrefix(front, "modal") {
		return event
	}
	// A text field must receive its characters.
	if a.editing && event.Key() != tcell.KeyF5 && event.Key() != tcell.KeyEsc {
		return event
	}

	switch event.Key() {
	case tcell.KeyTab:
		a.switchTo(a.current + 1)
		return nil
	case tcell.KeyBacktab:
		a.switchTo(a.current - 1)
		return nil
	case tcell.KeyF5:
		go a.refreshAll(context.Background())
		return nil
	case tcell.KeyCtrlC:
		a.tv.Stop()
		return nil
	}

	switch event.Rune() {
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		a.switchTo(int(event.Rune() - '1'))
		return nil
	case '0':
		// The tenth screen keeps the natural keyboard order.
		a.switchTo(9)
		return nil
	case 'q', 'Q':
		a.tv.Stop()
		return nil
	case 'r', 'R':
		go a.rescan(context.Background())
		return nil
	case '?', 'h', 'H':
		a.showHelp()
		return nil
	}
	return event
}

func (a *App) switchTo(index int) {
	if index < 0 {
		index = len(a.screens) - 1
	}
	if index >= len(a.screens) {
		index = 0
	}
	a.current = index
	a.pages.SwitchToPage(pageName(index))
	a.tv.SetFocus(a.screens[index].focus())
	a.renderNav()
	go a.screens[index].refresh(context.Background())
}

func (a *App) renderNav() {
	var parts []string
	for i, s := range a.screens {
		// The tenth screen answers to 0, so the digits stay single-key.
		key := i + 1
		if key == 10 {
			key = 0
		}
		label := fmt.Sprintf("%d %s", key, s.title())
		if i == a.current {
			parts = append(parts, tag(hexSeries1, bold(label)))
		} else {
			parts = append(parts, dim(label))
		}
	}
	hints := a.screens[a.current].hints()
	line := " " + strings.Join(parts, dim(" · "))
	if hints != "" {
		line += "   " + dim("│") + "  " + hints
	}
	a.nav.SetText(line + "   " + dim("│  F5 обновить · r пересканировать · ? помощь · q выход"))
}

func (a *App) renderHeader() {
	snap := a.Scanner.Latest()
	left := bold(" NetKnownsThat")
	if snap != nil {
		counts := snap.FindingCounts()
		worst := counts["critical"] + counts["high"]
		findings := tag(hexGood, "проблем не найдено")
		if worst > 0 {
			findings = tag(hexCritical, fmt.Sprintf("требуют внимания: %d", worst))
		} else if len(snap.Findings) > 0 {
			findings = tag(hexWarning, fmt.Sprintf("замечаний: %d", len(snap.Findings)))
		}
		left += dim(" · ") + snap.Host.Hostname +
			dim(" · режим ") + snap.Mode +
			dim(" · ") + findings +
			dim(" · скан ") + shortTime(snap.TS)
	}
	if a.busy > 0 {
		left += tag(hexWarning, "  ● работаю…")
	}
	if !a.canMutate() {
		left += tag(hexWarning, "  ● только чтение")
	}
	a.header.SetText(left)
}

// canMutate reports whether the operator may change host state.
func (a *App) canMutate() bool { return a.Cfg.AllowMutations }

// --------------------------------------------------------------------- status

func (a *App) setStatus(hex, text string) {
	a.status.SetText(" " + tag(hex, text))
}

func (a *App) queue(fn func()) {
	a.tv.QueueUpdateDraw(fn)
}

// runAsync performs a blocking operation off the UI thread and reports the
// outcome on the status line. Refusing to mutate is handled here once.
func (a *App) runAsync(what string, needsMutation bool, fn func(ctx context.Context) (string, error)) {
	if needsMutation && !a.canMutate() {
		a.setStatus(hexWarning, "Изменения запрещены настройкой NKT_ALLOW_MUTATIONS=false")
		return
	}
	a.busy++
	a.setStatus(hexMuted, what+"…")
	a.renderHeader()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		msg, err := fn(ctx)

		a.queue(func() {
			a.busy--
			if err != nil {
				a.setStatus(hexCritical, "✖ "+err.Error())
			} else {
				if msg == "" {
					msg = what + ": готово"
				}
				a.setStatus(hexGood, "✔ "+msg)
			}
			a.renderHeader()
		})
		a.screens[a.current].refresh(context.Background())
	}()
}

// --------------------------------------------------------------------- modals

// confirm asks a yes/no question before a destructive action.
func (a *App) confirm(question string, onYes func()) {
	modal := tview.NewModal().
		SetText(question).
		AddButtons([]string{"Отмена", "Выполнить"}).
		SetDoneFunc(func(index int, _ string) {
			a.pages.RemovePage("modal-confirm")
			a.tv.SetFocus(a.screens[a.current].focus())
			if index == 1 {
				onYes()
			}
		})
	a.pages.AddPage("modal-confirm", modal, true, true)
	a.tv.SetFocus(modal)
}

// showModal puts an arbitrary primitive on top, centred, closed with Escape.
func (a *App) showModal(name string, p tview.Primitive, width, height int) {
	wrapper := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 0, true).
			AddItem(nil, 0, 1, false), width, 0, true).
		AddItem(nil, 0, 1, false)

	a.pages.AddPage("modal-"+name, wrapper, true, true)
	a.tv.SetFocus(p)
}

func (a *App) closeModal(name string) {
	a.pages.RemovePage("modal-" + name)
	a.editing = false
	a.tv.SetFocus(a.screens[a.current].focus())
}

// showText displays a scrollable read-only panel, used for details and diffs.
func (a *App) showText(name, title, body string) {
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetScrollable(true)
	view.SetText(body)
	view.SetBorder(true).SetTitle(" " + title + " ").SetBorderColor(colorBorder)
	view.SetDoneFunc(func(tcell.Key) { a.closeModal(name) })
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			a.closeModal(name)
			return nil
		}
		return event
	})
	a.showModal(name, view, 100, 30)
}

func (a *App) showHelp() {
	body := strings.Join([]string{
		bold("Навигация"),
		"  1…9, 0       перейти на экран",
		"  Tab / Shift+Tab   следующий и предыдущий экран",
		"  ↑ ↓ PgUp PgDn     перемещение по таблице",
		"  Enter        подробности выбранной строки",
		"",
		bold("Общие действия"),
		"  F5           обновить данные текущего экрана",
		"  r            пересканировать хост целиком",
		"  ?            эта справка",
		"  q            выход",
		"",
		bold("Экраны"),
		"  Конфигурации  e править, d различия с версией, v история, u откат",
		"  Сервисы       s запустить, x остановить, t перезапустить, l перечитать, c проверить конфиг",
		"  Firewall      a добавить правило, x удалить правило",
		"  Сертификаты   Enter показать файл, g выпустить самоподписанный",
		"  Доступность   p проверить сейчас, space пауза и возобновление",
		"",
		dim("Все изменения записываются в журнал под именем " + a.actor + "."),
		dim("Если задано NKT_ALLOW_MUTATIONS=false, действия недоступны."),
	}, "\n")
	a.showText("help", "Справка", body)
}

// --------------------------------------------------------------------- reload

func (a *App) refreshAll(ctx context.Context) {
	for _, s := range a.screens {
		s.refresh(ctx)
	}
	a.queue(func() {
		a.renderHeader()
		if strings.Contains(a.status.GetText(true), "Загружаю") {
			a.setStatus(hexMuted, "Готово. F5 обновить, r пересканировать, ? справка.")
		}
	})
}

func (a *App) rescan(ctx context.Context) {
	a.queue(func() {
		a.busy++
		a.setStatus(hexMuted, "Сканирую хост…")
		a.renderHeader()
	})

	snap, err := a.Scanner.Scan(ctx)

	a.queue(func() {
		a.busy--
		if err != nil {
			a.setStatus(hexCritical, "✖ скан не удался: "+err.Error())
		} else {
			a.setStatus(hexGood, fmt.Sprintf("✔ хост пересканирован за %d мс, находок: %d",
				snap.ScanMS, len(snap.Findings)))
		}
		a.renderHeader()
	})
	a.refreshAll(ctx)
}

// autoRefresh repaints the current screen on the inventory interval. It never
// rescans by itself: reading the host is the scheduler's job, and a terminal
// left open overnight should not keep the machine busy.
func (a *App) autoRefresh(ctx context.Context) {
	interval := a.Cfg.InventoryInterval
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.screens[a.current].refresh(ctx)
			a.queue(a.renderHeader)
		}
	}
}

// ------------------------------------------------------------------- helpers

// newTable builds a table styled consistently across screens.
func newTable(headers ...string) *tview.Table {
	t := tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	t.SetBorder(true).SetBorderColor(colorBorder)
	t.SetSelectedStyle(tcell.StyleDefault.Background(colorAccent).Foreground(tcell.ColorBlack))
	for i, h := range headers {
		t.SetCell(0, i, tview.NewTableCell(" "+h).
			SetTextColor(colorSecondary).
			SetSelectable(false).
			SetAttributes(tcell.AttrBold))
	}
	return t
}

// cell builds a plain table cell.
func cell(text string) *tview.TableCell {
	return tview.NewTableCell(" " + text).SetTextColor(colorText)
}

// cellColor builds a coloured table cell.
func cellColor(text, hex string) *tview.TableCell {
	return tview.NewTableCell(" " + text).SetTextColor(tcell.GetColor(hex))
}

// cellDim builds a secondary table cell.
func cellDim(text string) *tview.TableCell {
	return tview.NewTableCell(" " + text).SetTextColor(colorMuted)
}

// cellRight builds a right-aligned numeric cell.
func cellRight(text string) *tview.TableCell {
	return tview.NewTableCell(text + " ").SetTextColor(colorText).SetAlign(tview.AlignRight)
}

// newPanel builds a bordered text panel.
func newPanel(title string) *tview.TextView {
	v := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	v.SetBorder(true).SetBorderColor(colorBorder)
	if title != "" {
		v.SetTitle(" " + title + " ")
	}
	return v
}

// shortTime renders a stored RFC3339 timestamp in local time.
func shortTime(ts string) string {
	if ts == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("02.01 15:04")
}

// relativeTime renders how long ago something happened.
func relativeTime(ts string) string {
	if ts == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
}

// since builds a storage timestamp for a lookback window.
func since(d time.Duration) string {
	return store.FormatTime(time.Now().Add(-d))
}

// tzOffsetMinutes is the local UTC offset, so hourly buckets mean local hours.
func tzOffsetMinutes() int {
	_, offset := time.Now().Zone()
	return offset / 60
}
