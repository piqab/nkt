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
	"github.com/althq/netknownsthat/internal/msgs"
	"github.com/althq/netknownsthat/internal/store"
)

// tuiLangKey is the internal/store KV key the TUI's language choice is
// persisted under — the same generic KVGet/KVSet mechanism
// internal/monitor already uses for a log-tailer position and the demo
// backfill flag, not a new table/column.
const tuiLangKey = "tui_lang"

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

	// Lang picks the TUI's display language up front, skipping both the
	// tuiLangKey DB lookup and the first-launch prompt — tests set this
	// explicitly (msgs.RU, matching every existing fixture assertion) so
	// they never need to script an answer to a prompt they don't expect.
	// Left empty in normal use: Run resolves it from the DB, or prompts
	// and persists the answer if this is genuinely the first launch.
	Lang msgs.Lang
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
	// bootstrapping is true from the moment the "loading host state" status
	// line is first set until the first refreshAll actually completes —
	// replaces a substring check against that status line's own (now
	// translatable, so no longer a stable match target) text.
	bootstrapping bool
}

// Run starts the terminal interface and blocks until the user quits.
func Run(ctx context.Context, deps Deps) error {
	a := &App{Deps: deps, tv: tview.NewApplication(), actor: actorName()}

	// A caller-supplied Lang (only ever set by tests) skips both the DB
	// lookup and the first-launch prompt below — see Deps.Lang's own doc
	// comment. Otherwise, a language chosen on a previous run is
	// persisted under tuiLangKey; nothing there means this really is the
	// first launch, and promptLang below asks and saves the answer before
	// the interface's first real screen shows.
	langKnown := a.Lang != ""
	if !langKnown {
		if raw, ok, err := a.DB.KVGet(ctx, tuiLangKey); err == nil && ok {
			a.Lang = msgs.ParseLang(raw)
			langKnown = true
		}
	}
	if a.Lang == "" {
		// Transient default: only ever visibly used for the brief instant
		// before promptLang's own SetDoneFunc overwrites it, since nothing
		// else is on screen yet at this point.
		a.Lang = msgs.DefaultLang
	}

	a.header = tview.NewTextView().SetDynamicColors(true)
	a.status = tview.NewTextView().SetDynamicColors(true)
	a.nav = tview.NewTextView().SetDynamicColors(true)
	a.pages = tview.NewPages()

	// Screens carry chrome (panel/table titles) that each constructor sets
	// once via SetTitle, never revisited by refresh() — building them here
	// unconditionally would freeze that chrome in whatever a.Lang held at
	// this instant. On a first launch that's still the transient default
	// above, not the operator's actual choice, so construction waits for
	// langKnown; promptLang's SetDoneFunc calls buildScreens itself once the
	// answer is in.
	buildScreens := func() {
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
	}
	if langKnown {
		buildScreens()
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
	if langKnown {
		a.renderNav()
	}
	a.setStatus(hexMuted, a.T("tui.loadingHostState"))
	a.bootstrapping = true

	start := func() {
		// The first scan happens in the background so the interface appears at once.
		go a.refreshAll(ctx)
		// A slow periodic refresh keeps the screen honest without hammering the host.
		go a.autoRefresh(ctx)
	}
	if langKnown {
		start()
	} else {
		a.promptLang(ctx, buildScreens, start)
	}

	return a.tv.Run()
}

// promptLang shows the one-time "English or Russian?" choice a genuinely
// first launch needs — worded so it reads regardless of which language the
// operator ends up picking, since a.Lang isn't known yet at this point.
// Persists the answer under tuiLangKey so this never asks again, then calls
// buildScreens now that a.Lang is finally settled — screens must not be
// constructed any earlier, since each one bakes chrome like panel titles
// into a one-time SetTitle call that nothing ever re-runs afterward — before
// re-rendering the header/nav/status text (built before the answer existed)
// and calling onDone to start the background refreshes the normal path
// already kicked off by this point.
func (a *App) promptLang(ctx context.Context, buildScreens, onDone func()) {
	modal := tview.NewModal().
		SetText("Language / Язык").
		AddButtons([]string{"English", "Русский"}).
		SetDoneFunc(func(_ int, label string) {
			if label == "English" {
				a.Lang = msgs.EN
			} else {
				a.Lang = msgs.RU
			}
			_ = a.DB.KVSet(ctx, tuiLangKey, string(a.Lang))
			buildScreens()
			a.pages.RemovePage("modal-lang")
			a.renderHeader()
			a.renderNav()
			a.setStatus(hexMuted, a.T("tui.loadingHostState"))
			a.tv.SetFocus(a.screens[a.current].focus())
			onDone()
		})
	a.pages.AddPage("modal-lang", modal, true, true)
	a.tv.SetFocus(modal)
}

// T looks up key in the TUI's own chosen language — the tui.* namespace
// counterpart to internal/api's per-request msgs.T, just resolved once at
// Run() instead of per-request, since the TUI has one operator per
// process, not one per HTTP request.
func (a *App) T(key string, args ...any) string {
	return msgs.T(a.Lang, key, args...)
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
	a.nav.SetText(line + "   " + dim("│  "+a.T("tui.globalHints")))
}

func (a *App) renderHeader() {
	snap := a.Scanner.Latest()
	left := bold(" NetKnownsThat")
	if snap != nil {
		counts := snap.FindingCounts()
		worst := counts["critical"] + counts["high"]
		findings := tag(hexGood, a.T("tui.noProblemsFound"))
		if worst > 0 {
			findings = tag(hexCritical, a.T("tui.needsAttention", worst))
		} else if len(snap.Findings) > 0 {
			findings = tag(hexWarning, a.T("tui.notesCount", len(snap.Findings)))
		}
		left += dim(" · ") + snap.Host.Hostname +
			dim(" · "+a.T("tui.modeLabel")+" ") + snap.Mode +
			dim(" · ") + findings +
			dim(" · "+a.T("tui.scanLabel")+" ") + shortTime(snap.TS)
	}
	if a.busy > 0 {
		left += tag(hexWarning, "  ● "+a.T("tui.working"))
	}
	if !a.canMutate() {
		left += tag(hexWarning, "  ● "+a.T("tui.readOnly"))
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
		a.setStatus(hexWarning, a.T("tui.mutationsDisabled"))
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
					msg = what + a.T("tui.doneSuffix")
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
		AddButtons([]string{a.T("tui.cancel"), a.T("tui.execute")}).
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
		bold(a.T("tui.help.navigation")),
		a.T("tui.help.switchScreen"),
		a.T("tui.help.tabNextPrev"),
		a.T("tui.help.tableNav"),
		a.T("tui.help.enterDetails"),
		"",
		bold(a.T("tui.help.commonActions")),
		a.T("tui.help.f5Refresh"),
		a.T("tui.help.rRescan"),
		a.T("tui.help.qmarkHelp"),
		a.T("tui.help.qQuit"),
		"",
		bold(a.T("tui.help.screensHeading")),
		a.T("tui.help.configsKeys"),
		a.T("tui.help.servicesKeys"),
		a.T("tui.help.firewallKeys"),
		a.T("tui.help.certsKeys"),
		a.T("tui.help.availabilityKeys"),
		"",
		dim(a.T("tui.help.auditNote", a.actor)),
		dim(a.T("tui.help.mutationsNote")),
	}, "\n")
	a.showText("help", a.T("tui.help.title"), body)
}

// --------------------------------------------------------------------- reload

func (a *App) refreshAll(ctx context.Context) {
	for _, s := range a.screens {
		s.refresh(ctx)
	}
	a.queue(func() {
		a.renderHeader()
		if a.bootstrapping {
			a.bootstrapping = false
			a.setStatus(hexMuted, a.T("tui.readyHint"))
		}
	})
}

func (a *App) rescan(ctx context.Context) {
	a.queue(func() {
		a.busy++
		a.setStatus(hexMuted, a.T("tui.scanningHost"))
		a.renderHeader()
	})

	snap, err := a.Scanner.Scan(ctx)

	a.queue(func() {
		a.busy--
		if err != nil {
			a.setStatus(hexCritical, "✖ "+a.T("tui.scanFailed", err.Error()))
		} else {
			a.setStatus(hexGood, "✔ "+a.T("tui.rescanDone", snap.ScanMS, len(snap.Findings)))
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
func relativeTime(lang msgs.Lang, ts string) string {
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
		return msgs.T(lang, "tui.justNow")
	case d < time.Hour:
		return msgs.T(lang, "tui.minutesAgo", int(d.Minutes()))
	case d < 24*time.Hour:
		return msgs.T(lang, "tui.hoursAgo", int(d.Hours()))
	default:
		return msgs.T(lang, "tui.daysAgo", int(d.Hours()/24))
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
