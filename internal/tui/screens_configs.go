package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

type configsScreen struct {
	app *App

	list    *tview.Table
	preview *tview.TextView
	root    *tview.Flex
	files   []model.ManagedFile
}

func newConfigsScreen(a *App) *configsScreen {
	s := &configsScreen{app: a}
	s.list = newTable(a.T("tui.configs.colFile"), a.T("tui.configs.colService"), a.T("tui.configs.colSize"), a.T("tui.configs.colModified"))
	s.list.SetTitle(" " + a.T("tui.configs.listTitle") + " ")
	s.preview = newPanel(a.T("tui.configs.contentTitle"))
	s.preview.SetScrollable(true)

	s.list.SetSelectionChangedFunc(func(row, _ int) { go s.loadPreview(row - 1) })
	s.list.SetInputCapture(s.onKey)

	s.root = tview.NewFlex().
		AddItem(s.list, 62, 0, true).
		AddItem(s.preview, 0, 1, false)
	return s
}

func (s *configsScreen) title() string          { return s.app.T("tui.configs.title") }
func (s *configsScreen) view() tview.Primitive  { return s.root }
func (s *configsScreen) focus() tview.Primitive { return s.list }

func (s *configsScreen) hints() string {
	return dim(s.app.T("tui.configs.hints"))
}

func (s *configsScreen) selected() (model.ManagedFile, bool) {
	row, _ := s.list.GetSelection()
	idx := row - 1
	if idx < 0 || idx >= len(s.files) {
		return model.ManagedFile{}, false
	}
	return s.files[idx], true
}

func (s *configsScreen) onKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 'e', 'E':
		s.openEditor()
		return nil
	case 'v', 'V', 'u', 'U':
		if f, ok := s.selected(); ok {
			s.showVersions(f.Path)
		}
		return nil
	}
	return event
}

func (s *configsScreen) refresh(ctx context.Context) {
	files, err := s.app.Configs.List(ctx)
	if err != nil {
		s.app.queue(func() { s.app.setStatus(hexCritical, s.app.T("tui.configs.listError", err.Error())) })
		return
	}

	s.app.queue(func() {
		s.files = files
		s.list.Clear()
		for i, h := range []string{s.app.T("tui.configs.colFile"), s.app.T("tui.configs.colService"), s.app.T("tui.configs.colSize"), s.app.T("tui.configs.colModified")} {
			s.list.SetCell(0, i, tview.NewTableCell(" "+h).
				SetTextColor(colorSecondary).SetSelectable(false).SetAttributes(tcell.AttrBold))
		}
		for i, f := range files {
			row := i + 1
			name := f.Path
			if len(name) > 40 {
				name = "…" + name[len(name)-39:]
			}
			s.list.SetCell(row, 0, cell(name))
			s.list.SetCell(row, 1, cellDim(f.Service))
			s.list.SetCell(row, 2, cellRight(formatBytes(s.app.Lang, float64(f.Size))))
			s.list.SetCell(row, 3, cellDim(f.ModTime.Local().Format("02.01 15:04")))
		}
		s.list.SetTitle(" " + s.app.T("tui.configs.listTitleCount", len(files)) + " ")
		if len(files) > 0 {
			go s.loadPreview(0)
		}
	})
}

func (s *configsScreen) loadPreview(index int) {
	if index < 0 || index >= len(s.files) {
		return
	}
	file, err := s.app.Configs.Read(s.files[index].Path)
	s.app.queue(func() {
		if err != nil {
			s.preview.SetText(" " + tag(hexCritical, s.app.T("tui.configs.readError", err.Error())))
			return
		}
		s.preview.SetTitle(fmt.Sprintf(" %s ", file.Path))
		// Escape the content: a config may legitimately contain square brackets,
		// which tview would otherwise read as colour tags.
		s.preview.SetText(tview.Escape(file.Content)).ScrollToBeginning()
	})
}

// openEditor edits the selected file in a full-screen text area. Saving goes
// through the same validated path as the web interface: the service checks the
// config, and a rejected change is rolled back automatically.
func (s *configsScreen) openEditor() {
	target, ok := s.selected()
	if !ok {
		return
	}
	if !s.app.canMutate() {
		s.app.setStatus(hexWarning, s.app.T("tui.mutationsDisabled"))
		return
	}

	file, err := s.app.Configs.Read(target.Path)
	if err != nil {
		s.app.setStatus(hexCritical, s.app.T("tui.configs.openError", err.Error()))
		return
	}

	area := tview.NewTextArea().SetText(file.Content, false)
	area.SetBorder(true).SetBorderColor(colorAccent).
		SetTitle(" " + s.app.T("tui.configs.editorTitle", file.Path) + " ")
	// Несохранённые правки — пометка в заголовке.
	area.SetChangedFunc(func() {
		title := " " + s.app.T("tui.configs.editorTitle", file.Path) + " "
		if area.GetText() != file.Content {
			title += tag(hexWarning, "● "+s.app.T("tui.configs.modified")) + " "
		}
		area.SetTitle(title)
	})
	hints := tview.NewTextView().SetDynamicColors(true).SetText(" " + dim(s.app.T("tui.configs.editorHints")))

	note := tview.NewInputField().SetLabel(" " + s.app.T("tui.configs.noteLabel") + ": ").SetFieldWidth(50)
	apply := tview.NewCheckbox().SetLabel(" " + s.app.T("tui.configs.applyLabel") + ": ")

	bar := tview.NewFlex().
		AddItem(note, 0, 3, false).
		AddItem(apply, 0, 2, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(area, 0, 1, true).
		AddItem(bar, 1, 0, false).
		AddItem(hints, 1, 0, false)

	save := func() {
		content := area.GetText()
		if content == file.Content {
			s.app.closeModal("editor")
			s.app.setStatus(hexMuted, s.app.T("tui.configs.unchanged"))
			return
		}
		s.app.closeModal("editor")
		s.app.runAsync(s.app.T("tui.configs.saving", file.Path), true, func(ctx context.Context) (string, error) {
			res, err := s.app.Configs.Write(ctx, s.app.Lang, s.app.actor, file.Path, content,
				note.GetText(), apply.IsChecked())
			if err != nil {
				if res.RolledBack {
					return "", fmt.Errorf("%w"+s.app.T("tui.configs.rolledBackSuffix"), err)
				}
				return "", err
			}
			msg := s.app.T("tui.configs.saved", file.Path, res.VersionID)
			if !res.Validated {
				msg += s.app.T("tui.configs.notValidatedSuffix")
			}
			if res.Applied {
				msg += s.app.T("tui.configs.appliedSuffix")
			}
			return msg, nil
		})
	}

	capture := func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlS:
			save()
			return nil
		case tcell.KeyEsc:
			s.app.confirmDiscard(area.GetText() != file.Content, func() { s.app.closeModal("editor") })
			return nil
		case tcell.KeyF2:
			// История — вместо редактора: откат меняет файл, и открытый
			// текст был бы уже не тем.
			s.app.confirmDiscard(area.GetText() != file.Content, func() {
				s.app.closeModal("editor")
				s.showVersions(file.Path)
			})
			return nil
		case tcell.KeyTab:
			// Inside the editor Tab moves between the field row and the text.
			if area.HasFocus() {
				s.app.tv.SetFocus(note)
			} else if note.HasFocus() {
				s.app.tv.SetFocus(apply)
			} else {
				s.app.tv.SetFocus(area)
			}
			return nil
		}
		return event
	}
	area.SetInputCapture(capture)
	note.SetInputCapture(capture)
	apply.SetInputCapture(capture)

	s.app.editing = true
	s.app.showModal("editor", layout, 120, 40)
	s.app.tv.SetFocus(area)
}

// confirmDiscard asks before throwing away unsaved edits.
func (a *App) confirmDiscard(dirty bool, discard func()) {
	if !dirty {
		discard()
		return
	}
	a.confirm(a.T("tui.configs.confirmDiscard"), discard)
}

// showVersions — история версий файла в одном окне: Enter — различия с
// текущим файлом, r — откатить к выбранной (с выбором, перезагрузить ли
// сервис), Esc — закрыть.
func (s *configsScreen) showVersions(path string) {
	versions, err := s.app.Configs.Versions(context.Background(), path, 100)
	if err != nil {
		s.app.setStatus(hexCritical, err.Error())
		return
	}
	if len(versions) == 0 {
		s.app.setStatus(hexMuted, s.app.T("tui.configs.emptyHistory"))
		return
	}

	table := newTable(s.app.T("tui.configs.colVersion"), s.app.T("tui.configs.colWhen"), s.app.T("tui.configs.colWho"),
		s.app.T("tui.configs.colEvent"), s.app.T("tui.configs.colSize"), s.app.T("tui.configs.colNote"))
	table.SetTitle(" " + s.app.T("tui.configs.versionsTitle", path) + " ").SetBorderColor(colorAccent)

	for i, v := range versions {
		row := i + 1
		table.SetCell(row, 0, cellRight(fmt.Sprintf("%d", v.ID)))
		table.SetCell(row, 1, cell(shortTime(v.TS)))
		table.SetCell(row, 2, cellDim(v.Author))
		table.SetCell(row, 3, cellDim(v.Action))
		table.SetCell(row, 4, cellRight(formatBytes(s.app.Lang, float64(v.Size))))
		table.SetCell(row, 5, cellDim(truncate(v.Note, 40)))
	}
	current := func() (store.ConfigVersion, bool) {
		row, _ := table.GetSelection()
		if row-1 < 0 || row-1 >= len(versions) {
			return store.ConfigVersion{}, false
		}
		return versions[row-1], true
	}
	hints := tview.NewTextView().SetDynamicColors(true).SetText(" " + dim(s.app.T("tui.configs.versionsHints")))
	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(table, 0, 1, true).
		AddItem(hints, 1, 0, false)

	table.SetSelectedFunc(func(row, _ int) {
		if v, ok := current(); ok {
			s.showDiff(path, v)
		}
	})
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			s.app.closeModal("versions")
			return nil
		}
		if event.Rune() == 'r' || event.Rune() == 'R' {
			if v, ok := current(); ok {
				s.confirmRollback(v)
			}
			return nil
		}
		return event
	})

	s.app.showModal("versions", layout, 108, 26)
	s.app.tv.SetFocus(table)
}

// showDiff — различия версии с текущим файлом поверх истории: Esc — назад
// к истории, r — откатить к этой версии.
func (s *configsScreen) showDiff(path string, v store.ConfigVersion) {
	diff, err := s.app.Configs.Diff(context.Background(), v.ID)
	if err != nil {
		s.app.setStatus(hexCritical, err.Error())
		return
	}
	if strings.TrimSpace(diff) == "" {
		s.app.setStatus(hexMuted, s.app.T("tui.configs.versionMatchesCurrent", v.ID))
		return
	}
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetScrollable(true)
	view.SetText(colorizeDiff(diff))
	view.SetBorder(true).SetTitle(" " + s.app.T("tui.configs.diffTitle", v.ID) + " ").SetBorderColor(colorBorder)
	hints := tview.NewTextView().SetDynamicColors(true).SetText(" " + dim(s.app.T("tui.configs.diffHints")))
	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(view, 0, 1, true).
		AddItem(hints, 1, 0, false)
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch {
		case event.Key() == tcell.KeyEsc || event.Rune() == 'q':
			s.app.closeModal("diff")
			s.app.tv.SetFocus(s.app.modals["versions"].p)
			return nil
		case event.Rune() == 'r' || event.Rune() == 'R':
			s.confirmRollback(v)
			return nil
		}
		return event
	})
	s.app.showModal("diff", layout, 100, 30)
	s.app.tv.SetFocus(view)
}

// confirmRollback — откатить к версии: просто записать её или записать и
// перезагрузить сервис.
func (s *configsScreen) confirmRollback(v store.ConfigVersion) {
	s.app.choose(s.app.T("tui.configs.confirmRollback", v.ID, shortTime(v.TS)),
		[]string{s.app.T("tui.cancel"), s.app.T("tui.configs.rollbackOnly"), s.app.T("tui.configs.rollbackApply")},
		func(index int) {
			if index == 0 {
				return
			}
			apply := index == 2
			s.app.closeModal("diff")
			s.app.closeModal("versions")
			s.app.runAsync(s.app.T("tui.configs.rollingBack", v.ID), true,
				func(ctx context.Context) (string, error) {
					res, err := s.app.Configs.Rollback(ctx, s.app.Lang, s.app.actor, v.ID, apply)
					if err != nil {
						return "", err
					}
					msg := s.app.T("tui.configs.rolledBackTo", v.ID, res.VersionID)
					switch {
					case res.Applied:
						msg += s.app.T("tui.configs.appliedSuffix")
					case apply:
						msg += s.app.T("tui.configs.notAppliedSuffix")
					default:
						msg += s.app.T("tui.configs.notReloadedSuffix")
					}
					return msg, nil
				})
		})
}

// colorizeDiff paints a unified diff. Added and removed lines carry a sign as
// well as a colour, so the difference survives a monochrome terminal.
func colorizeDiff(diff string) string {
	var sb strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		escaped := tview.Escape(line)
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			sb.WriteString(dim(escaped))
		case strings.HasPrefix(line, "@@"):
			sb.WriteString(tag(hexSeries1, escaped))
		case strings.HasPrefix(line, "+"):
			sb.WriteString(tag(hexGood, escaped))
		case strings.HasPrefix(line, "-"):
			sb.WriteString(tag(hexCritical, escaped))
		default:
			sb.WriteString(escaped)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// interface assertion: the config manager is the only writer of host configs.
var _ interface {
	Write(ctx context.Context, lang msgs.Lang, user, path, content, note string, apply bool) (control.WriteResult, error)
} = (*control.ConfigManager)(nil)
