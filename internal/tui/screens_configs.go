package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/msgs"
	"github.com/althq/netknownsthat/internal/store"
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
	s.list = newTable("Файл", "Сервис", "Размер", "Изменён")
	s.list.SetTitle(" Файлы конфигурации ")
	s.preview = newPanel("Содержимое")
	s.preview.SetScrollable(true)

	s.list.SetSelectionChangedFunc(func(row, _ int) { go s.loadPreview(row - 1) })
	s.list.SetInputCapture(s.onKey)

	s.root = tview.NewFlex().
		AddItem(s.list, 62, 0, true).
		AddItem(s.preview, 0, 1, false)
	return s
}

func (s *configsScreen) title() string          { return "Конфигурации" }
func (s *configsScreen) view() tview.Primitive  { return s.root }
func (s *configsScreen) focus() tview.Primitive { return s.list }

func (s *configsScreen) hints() string {
	return dim("e править · v история версий · u откат к версии")
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
	case 'v', 'V':
		s.showVersions(false)
		return nil
	case 'u', 'U':
		s.showVersions(true)
		return nil
	}
	return event
}

func (s *configsScreen) refresh(ctx context.Context) {
	files, err := s.app.Configs.List(ctx)
	if err != nil {
		s.app.queue(func() { s.app.setStatus(hexCritical, "список файлов: "+err.Error()) })
		return
	}

	s.app.queue(func() {
		s.files = files
		s.list.Clear()
		for i, h := range []string{"Файл", "Сервис", "Размер", "Изменён"} {
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
			s.list.SetCell(row, 2, cellRight(formatBytes(float64(f.Size))))
			s.list.SetCell(row, 3, cellDim(f.ModTime.Local().Format("02.01 15:04")))
		}
		s.list.SetTitle(fmt.Sprintf(" Файлы конфигурации — %d ", len(files)))
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
			s.preview.SetText(" " + tag(hexCritical, "не удалось прочитать: "+err.Error()))
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
		s.app.setStatus(hexWarning, "Изменения запрещены настройкой NKT_ALLOW_MUTATIONS=false")
		return
	}

	file, err := s.app.Configs.Read(target.Path)
	if err != nil {
		s.app.setStatus(hexCritical, "не удалось открыть файл: "+err.Error())
		return
	}

	area := tview.NewTextArea().SetText(file.Content, false)
	area.SetBorder(true).SetBorderColor(colorAccent).
		SetTitle(fmt.Sprintf(" %s — Ctrl+S сохранить, Esc отменить ", file.Path))

	note := tview.NewInputField().SetLabel(" Комментарий к правке: ").SetFieldWidth(50)
	apply := tview.NewCheckbox().SetLabel(" Перезагрузить сервис после сохранения: ")

	bar := tview.NewFlex().
		AddItem(note, 0, 3, false).
		AddItem(apply, 0, 2, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(area, 0, 1, true).
		AddItem(bar, 1, 0, false)

	save := func() {
		content := area.GetText()
		if content == file.Content {
			s.app.closeModal("editor")
			s.app.setStatus(hexMuted, "Файл не изменился — ничего не сохранял.")
			return
		}
		s.app.closeModal("editor")
		s.app.runAsync("Проверяю и сохраняю "+file.Path, true, func(ctx context.Context) (string, error) {
			// The TUI has no per-viewer language of its own (no HTTP request
			// to read it from) — it always renders Russian, same as every
			// other string on screen here.
			res, err := s.app.Configs.Write(ctx, msgs.RU, s.app.actor, file.Path, content,
				note.GetText(), apply.IsChecked())
			if err != nil {
				if res.RolledBack {
					return "", fmt.Errorf("%w — файл возвращён в прежнее состояние", err)
				}
				return "", err
			}
			msg := fmt.Sprintf("%s сохранён, версия #%d", file.Path, res.VersionID)
			if !res.Validated {
				msg += " (проверка конфигурации для этого сервиса недоступна)"
			}
			if res.Applied {
				msg += ", сервис перезагружен"
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
	a.confirm("Правки не сохранены. Закрыть редактор и потерять их?", discard)
}

// showVersions lists the stored revisions of the selected file. In rollback
// mode Enter restores the chosen version; otherwise it shows the difference
// between that version and what is on disk now.
func (s *configsScreen) showVersions(rollback bool) {
	target, ok := s.selected()
	if !ok {
		return
	}
	versions, err := s.app.Configs.Versions(context.Background(), target.Path, 100)
	if err != nil {
		s.app.setStatus(hexCritical, err.Error())
		return
	}
	if len(versions) == 0 {
		s.app.setStatus(hexMuted,
			"История пуста: файл ещё не редактировался через это приложение.")
		return
	}

	table := newTable("#", "Когда", "Кто", "Событие", "Размер", "Комментарий")
	title := " История версий — Enter показать различия, Esc закрыть "
	if rollback {
		title = " Откат — Enter восстановить выбранную версию, Esc закрыть "
	}
	table.SetTitle(title).SetBorderColor(colorAccent)

	for i, v := range versions {
		row := i + 1
		table.SetCell(row, 0, cellRight(fmt.Sprintf("%d", v.ID)))
		table.SetCell(row, 1, cell(shortTime(v.TS)))
		table.SetCell(row, 2, cellDim(v.Author))
		table.SetCell(row, 3, cellDim(v.Action))
		table.SetCell(row, 4, cellRight(formatBytes(float64(v.Size))))
		table.SetCell(row, 5, cellDim(truncate(v.Note, 40)))
	}

	table.SetSelectedFunc(func(row, _ int) {
		idx := row - 1
		if idx < 0 || idx >= len(versions) {
			return
		}
		v := versions[idx]
		s.app.closeModal("versions")
		if rollback {
			s.confirmRollback(v)
			return
		}
		s.showDiff(v)
	})
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			s.app.closeModal("versions")
			return nil
		}
		return event
	})

	s.app.showModal("versions", table, 108, 24)
}

func (s *configsScreen) showDiff(v store.ConfigVersion) {
	diff, err := s.app.Configs.Diff(context.Background(), v.ID)
	if err != nil {
		s.app.setStatus(hexCritical, err.Error())
		return
	}
	if strings.TrimSpace(diff) == "" {
		s.app.setStatus(hexMuted, fmt.Sprintf("Версия #%d совпадает с текущим файлом.", v.ID))
		return
	}
	s.app.showText("diff", fmt.Sprintf("Различия: версия #%d и текущий файл", v.ID), colorizeDiff(diff))
}

func (s *configsScreen) confirmRollback(v store.ConfigVersion) {
	s.app.confirm(fmt.Sprintf("Восстановить версию #%d от %s?\n\nТекущее содержимое файла будет заменено.",
		v.ID, shortTime(v.TS)), func() {
		s.app.runAsync(fmt.Sprintf("Восстанавливаю версию #%d", v.ID), true,
			func(ctx context.Context) (string, error) {
				res, err := s.app.Configs.Rollback(ctx, msgs.RU, s.app.actor, v.ID, false)
				if err != nil {
					return "", err
				}
				return res.Message, nil
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
