package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/model"
)

// Expiry bands, matching the analyzer's thresholds.
const (
	certWarnDays     = 30
	certCriticalDays = 7
	// certScaleDays is the horizon of the expiry bar: a year of runway is full.
	certScaleDays = 365
)

type certsScreen struct {
	app *App

	table    *tview.Table
	detail   *tview.TextView
	schedule *tview.TextView
	root     *tview.Flex
	certs    []model.Certificate
}

func newCertsScreen(a *App) *certsScreen {
	s := &certsScreen{app: a}
	s.table = newTable("Сайты", "Истекает", "Осталось", "Ключ", "Издатель", "Автообновление")
	s.detail = newPanel("Сертификат")
	s.schedule = newPanel("Расписание истечения")

	s.table.SetSelectionChangedFunc(func(row, _ int) { s.showDetail(row - 1) })
	s.table.SetInputCapture(s.onKey)

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.schedule, 0, 1, false).
		AddItem(s.detail, 0, 1, false)

	s.root = tview.NewFlex().
		AddItem(s.table, 0, 1, true).
		AddItem(right, 74, 0, false)
	return s
}

func (s *certsScreen) title() string          { return "Сертификаты" }
func (s *certsScreen) view() tview.Primitive  { return s.root }
func (s *certsScreen) focus() tview.Primitive { return s.table }
func (s *certsScreen) hints() string {
	return dim("Enter показать файл · g выпустить самоподписанный · r продлить через certbot · " +
		"c собрать PEM для haproxy")
}

func (s *certsScreen) onKey(event *tcell.EventKey) *tcell.EventKey {
	switch {
	case event.Rune() == 'g' || event.Rune() == 'G':
		s.showGenerateForm()
		return nil
	case event.Rune() == 'r' || event.Rune() == 'R':
		s.renewSelected()
		return nil
	case event.Rune() == 'c' || event.Rune() == 'C':
		s.showCombineForm()
		return nil
	case event.Key() == tcell.KeyEnter:
		row, _ := s.table.GetSelection()
		idx := row - 1
		if idx < 0 || idx >= len(s.certs) {
			return nil
		}
		cert := s.certs[idx]
		file, err := s.app.Configs.Read(cert.Path)
		if err != nil {
			// Certificates live outside the editable allowlist, which is
			// deliberate: they are not text to hand-edit. Show what is known.
			s.app.showText("certfile", cert.Path, s.describe(cert))
			return nil
		}
		s.app.showText("certfile", cert.Path, tview.Escape(file.Content))
		return nil
	}
	return event
}

// showGenerateForm collects a self-signed certificate request. Generation
// never touches nginx or haproxy configuration itself — the result panel shows
// the exact directives to paste through the config editor (screen 6), which
// validates the change and rolls back automatically if the service rejects it.
func (s *certsScreen) showGenerateForm() {
	if !s.app.canMutate() {
		s.app.setStatus(hexWarning, "Изменения запрещены настройкой NKT_ALLOW_MUTATIONS=false")
		return
	}

	names, service, bits, days := "", "nginx", "2048", "397"
	form := tview.NewForm().
		AddInputField("Имена через запятую", "", 44, nil, func(t string) { names = t }).
		AddDropDown("Сервис", []string{"nginx", "haproxy"}, 0, func(o string, _ int) { service = o }).
		AddDropDown("Длина ключа", []string{"2048", "3072", "4096"}, 0, func(o string, _ int) { bits = o }).
		AddInputField("Срок действия, дней", days, 8, tview.InputFieldInteger, func(t string) { days = t })

	form.AddButton("Создать", func() {
		var nameList []string
		for _, n := range strings.Split(names, ",") {
			if n = strings.TrimSpace(n); n != "" {
				nameList = append(nameList, n)
			}
		}
		b, _ := strconv.Atoi(bits)
		d, _ := strconv.Atoi(days)
		req := control.SelfSignedRequest{Names: nameList, Service: service, Bits: b, Days: d}

		s.app.closeModal("certgen")
		s.app.runAsync("Генерирую самоподписанный сертификат", true, func(ctx context.Context) (string, error) {
			res, err := s.app.Certs.GenerateSelfSigned(ctx, s.app.actor, req)
			if err != nil {
				return "", err
			}
			s.app.queue(func() {
				s.app.showText("certgen-result", "Сертификат создан", fmt.Sprintf(
					" %s\n действителен до %s\n\n Файл в конфигурацию ещё не добавлен — вставьте через "+
						"редактор (экран «Конфигурации»):\n\n%s\n",
					bold(strings.Join(res.Names, ", ")), res.NotAfter.Local().Format("02.01.2006"),
					tview.Escape(res.Snippet)))
			})
			return "сертификат создан, отпечаток " + res.Fingerprint[:16] + "…", nil
		})
	})
	form.AddButton("Отмена", func() { s.app.closeModal("certgen") })

	form.SetBorder(true).SetTitle(" Новый самоподписанный сертификат ").SetBorderColor(colorBorder)
	form.SetCancelFunc(func() { s.app.closeModal("certgen") })

	s.app.editing = true
	s.app.showModal("certgen", form, 74, 13)
}

// lineageLabel describes one /etc/letsencrypt/live lineage with its expiry,
// so picking one in the dropdown doesn't require checking the certificates
// table first.
func lineageLabel(info control.LineageInfo) string {
	if !info.Known {
		return info.Name + " — срок неизвестен"
	}
	switch {
	case info.DaysLeft < 0:
		return fmt.Sprintf("%s — просрочен %d дн. назад", info.Name, -info.DaysLeft)
	case info.DaysLeft == 0:
		return info.Name + " — истекает сегодня"
	default:
		return fmt.Sprintf("%s — %d дн.", info.Name, info.DaysLeft)
	}
}

// showCombineForm packages an already-issued certbot lineage into the single
// PEM haproxy's "crt" needs. Unlike showGenerateForm this never calls
// certbot: it only repackages a certificate that already exists on disk. If
// "Куда записать" names a path haproxy already uses, that exact file is
// overwritten and haproxy reloaded; left on "новый файл", a fresh file is
// written instead and the directive to paste in is shown.
func (s *certsScreen) showCombineForm() {
	if !s.app.canMutate() {
		s.app.setStatus(hexWarning, "Изменения запрещены настройкой NKT_ALLOW_MUTATIONS=false")
		return
	}

	lineages, err := s.app.Certs.ListLetsEncryptLineages()
	if err != nil {
		s.app.setStatus(hexWarning, "Не удалось прочитать /etc/letsencrypt/live: "+err.Error())
		return
	}
	if len(lineages) == 0 {
		s.app.setStatus(hexWarning, "В /etc/letsencrypt/live не найдено ни одной lineage")
		return
	}
	lineageLabels := make([]string, len(lineages))
	for i, info := range lineages {
		lineageLabels[i] = lineageLabel(info)
	}
	pathOptions := append([]string{"— новый файл —"}, s.app.Certs.ListHAProxyCertPaths()...)

	lineage := lineages[0].Name
	targetPath := ""
	form := tview.NewForm().
		AddDropDown("Lineage", lineageLabels, 0, func(_ string, i int) { lineage = lineages[i].Name }).
		AddDropDown("Куда записать", pathOptions, 0, func(o string, i int) {
			if i == 0 {
				targetPath = ""
			} else {
				targetPath = o
			}
		})

	form.AddButton("Собрать", func() {
		s.app.closeModal("certcombine")
		s.app.runAsync("Собираю PEM для haproxy", true, func(ctx context.Context) (string, error) {
			res, err := s.app.Certs.CombineForHAProxy(ctx, s.app.actor, lineage, targetPath)
			if err != nil {
				return "", err
			}
			s.app.queue(func() {
				body := fmt.Sprintf(" %s\n действителен до %s\n\n",
					bold(res.Lineage), res.NotAfter.Local().Format("02.01.2006"))
				if res.Snippet != "" {
					body += fmt.Sprintf("Файл в конфигурацию ещё не добавлен — вставьте через редактор "+
						"(экран «Конфигурации»):\n\n%s\n", tview.Escape(res.Snippet))
				} else {
					body += fmt.Sprintf("Файл %s перезаписан, haproxy перечитал конфигурацию — вставлять "+
						"ничего не нужно.\n", res.CombinedPath)
				}
				s.app.showText("certcombine-result", "PEM собран", body)
			})
			return "PEM собран, отпечаток " + res.Fingerprint[:16] + "…", nil
		})
	})
	form.AddButton("Отмена", func() { s.app.closeModal("certcombine") })

	form.SetBorder(true).SetTitle(" Собрать PEM для haproxy из certbot ").SetBorderColor(colorBorder)
	form.SetCancelFunc(func() { s.app.closeModal("certcombine") })

	s.app.editing = true
	s.app.showModal("certcombine", form, 66, 10)
}

// renewSelected re-issues the selected certificate's certbot lineage in
// place. Only offered when the app already found a renewal.conf for it —
// an orphan lineage or a manually managed path has nothing for
// `certbot renew --cert-name` to act on.
func (s *certsScreen) renewSelected() {
	row, _ := s.table.GetSelection()
	idx := row - 1
	if idx < 0 || idx >= len(s.certs) {
		return
	}
	cert := s.certs[idx]
	if cert.Renewal.Tool != "certbot" || !cert.Renewal.Managed || cert.Renewal.Lineage == "" {
		s.app.setStatus(hexWarning, "certbot не управляет этим сертификатом — продлить нечем")
		return
	}
	if !s.app.canMutate() {
		s.app.setStatus(hexWarning, "Изменения запрещены настройкой NKT_ALLOW_MUTATIONS=false")
		return
	}

	lineage := cert.Renewal.Lineage
	question := fmt.Sprintf("Выполнить certbot renew --cert-name %s?", lineage)
	if cert.Renewal.Derived {
		question += fmt.Sprintf("\n\nЭто копия сертификата из %s — продлится оригинал, а эта копия "+
			"будет автоматически пересобрана из нового сертификата и ключа, после чего перечитается сервис.",
			cert.Renewal.SourcePath)
	}
	s.app.confirm(question, func() {
		s.startRenewProgress(lineage)
	})
}

// startRenewProgress opens a live-updating panel showing a renewal as it
// actually happens — stopping services, certbot's own output, recombining
// any haproxy copy, restarting services — instead of a single status-line
// spinner for however long the whole thing takes.
func (s *certsScreen) startRenewProgress(lineage string) {
	id, err := s.app.Certs.StartRenewCertbot(s.app.actor, lineage)
	if err != nil {
		s.app.setStatus(hexCritical, "✖ "+err.Error())
		return
	}

	const modalName = "renew-progress"
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetScrollable(true)
	view.SetText(" " + dim("Начинаю…"))
	view.SetBorder(true).
		SetTitle(fmt.Sprintf(" Продление %s — Esc скрыть (в фоне продолжится) ", lineage)).
		SetBorderColor(colorBorder)

	stop := make(chan struct{})
	var closeOnce sync.Once
	closePanel := func() {
		closeOnce.Do(func() { close(stop) })
		s.app.closeModal(modalName)
	}
	view.SetDoneFunc(func(tcell.Key) { closePanel() })
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Rune() == 'q' {
			closePanel()
			return nil
		}
		return event
	})
	s.app.showModal(modalName, view, 104, 30)

	go func() {
		ticker := time.NewTicker(400 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				events, done, errMsg, ok := s.app.Certs.RenewJobStatus(id)
				if !ok {
					return
				}
				s.app.queue(func() {
					view.SetText(renderRenewLog(events, done, errMsg))
					view.ScrollToEnd()
				})
				if done {
					s.refresh(context.Background())
					return
				}
			}
		}
	}()
}

// renderRenewLog formats a renew job's progress for the live panel — one
// timestamped line per step, ending with the outcome once done.
func renderRenewLog(events []control.RenewEvent, done bool, errMsg string) string {
	var sb strings.Builder
	for _, e := range events {
		sb.WriteString(fmt.Sprintf(" [%s] %s\n", e.Time.Local().Format("15:04:05"), tview.Escape(e.Text)))
	}
	if done {
		sb.WriteString("\n")
		if errMsg != "" {
			sb.WriteString(" " + tag(hexCritical, "Ошибка: "+tview.Escape(errMsg)))
		} else {
			sb.WriteString(" " + tag(hexGood, "Готово."))
		}
	}
	return sb.String()
}

// expiryTone maps days remaining onto the reserved status palette.
func expiryTone(cert model.Certificate) string {
	switch {
	case cert.Error != "":
		return hexCritical
	case cert.DaysLeft < 0:
		return hexCritical
	case cert.DaysLeft <= certCriticalDays:
		return hexSerious
	case cert.DaysLeft <= certWarnDays:
		return hexWarning
	default:
		return hexGood
	}
}

// expiryWord states the situation in words, so the colour is never alone.
func expiryWord(cert model.Certificate) string {
	switch {
	case cert.Error != "":
		return "не читается"
	case cert.DaysLeft < 0:
		return fmt.Sprintf("просрочен на %d дн.", -cert.DaysLeft)
	case cert.DaysLeft == 0:
		return "истекает сегодня"
	default:
		return fmt.Sprintf("%d дн.", cert.DaysLeft)
	}
}

func (s *certsScreen) refresh(ctx context.Context) {
	snap, err := s.app.Scanner.LatestOrScan(ctx)
	if err != nil || snap == nil {
		return
	}

	s.app.queue(func() {
		s.certs = snap.Certs
		s.table.Clear()
		for i, h := range []string{"Сайты", "Истекает", "Осталось", "Ключ", "Издатель", "Автообновление"} {
			s.table.SetCell(0, i, tview.NewTableCell(" "+h).
				SetTextColor(colorSecondary).SetSelectable(false).SetAttributes(tcell.AttrBold))
		}

		for i, cert := range snap.Certs {
			row := i + 1
			s.table.SetCell(row, 0, cell(truncate(certNames(cert), 34)))
			if cert.Error != "" {
				s.table.SetCell(row, 1, cellColor("—", hexCritical))
			} else {
				s.table.SetCell(row, 1, cell(cert.NotAfter.Local().Format("02.01.2006")))
			}
			s.table.SetCell(row, 2, cellColor("●"+expiryWord(cert), expiryTone(cert)))
			key := "—"
			if cert.KeyAlgorithm != "" {
				key = fmt.Sprintf("%s %d", cert.KeyAlgorithm, cert.KeyBits)
			}
			s.table.SetCell(row, 3, cellDim(key))
			s.table.SetCell(row, 4, cellDim(truncate(commonName(cert.Issuer), 24)))
			s.table.SetCell(row, 5, cellColor(renewalWord(cert), renewalTone(cert)))
		}
		s.table.SetTitle(fmt.Sprintf(" Сертификаты — %d ", len(snap.Certs)))

		s.schedule.SetText(s.renderSchedule(snap.Certs))
		if len(snap.Certs) > 0 {
			s.showDetail(0)
		} else {
			s.detail.SetText("\n " + dim("В разобранных конфигурациях нет ни одного ssl_certificate."))
		}
	})
}

// renderSchedule draws the runway of every certificate on one scale, so the
// order in which they will break is visible at a glance.
func (s *certsScreen) renderSchedule(certs []model.Certificate) string {
	if len(certs) == 0 {
		return "\n " + dim("Нечего показывать.")
	}
	var sb strings.Builder
	sb.WriteString(dim(" запас до истечения, шкала — год\n\n"))

	for _, cert := range certs {
		name := truncate(certNames(cert), 22)
		if cert.Error != "" {
			sb.WriteString(fmt.Sprintf(" %-22s %s\n", name, tag(hexCritical, "файл не читается")))
			continue
		}
		fraction := float64(cert.DaysLeft) / certScaleDays
		if fraction < 0 {
			fraction = 0
		}
		sb.WriteString(fmt.Sprintf(" %-22s %s %s\n",
			name, hbar(fraction, 26, expiryTone(cert)),
			tag(expiryTone(cert), expiryWord(cert))))
	}

	sb.WriteString("\n" + dim(fmt.Sprintf(" пороги: %d дн. — предупреждение, %d дн. — срочно",
		certWarnDays, certCriticalDays)))
	return sb.String()
}

func (s *certsScreen) showDetail(index int) {
	if index < 0 || index >= len(s.certs) {
		return
	}
	s.detail.SetText(s.describe(s.certs[index]))
}

func (s *certsScreen) describe(cert model.Certificate) string {
	var sb strings.Builder
	sb.WriteString(" " + bold(certNames(cert)) + "\n")
	sb.WriteString(" " + dim(cert.Path) + "\n\n")

	if cert.Error != "" {
		sb.WriteString(" " + tag(hexCritical, "ошибка: "+cert.Error) + "\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf(" срок: %s — %s   %s\n",
		cert.NotBefore.Local().Format("02.01.2006"),
		cert.NotAfter.Local().Format("02.01.2006"),
		tag(expiryTone(cert), "●"+expiryWord(cert))))
	sb.WriteString(dim(" издатель: ") + truncate(cert.Issuer, 60) + "\n")
	sb.WriteString(dim(" субъект:  ") + truncate(cert.Subject, 60) + "\n")
	sb.WriteString(dim(" ключ: ") + fmt.Sprintf("%s %d бит", cert.KeyAlgorithm, cert.KeyBits) +
		dim("   подпись: ") + cert.SigAlgorithm + "\n")
	sb.WriteString(dim(" цепочка: ") + fmt.Sprintf("%d сертификат(а)", cert.ChainLength))
	if cert.SelfSigned {
		sb.WriteString("   " + tag(hexWarning, "самоподписанный"))
	}
	sb.WriteString("\n")

	if len(cert.Endpoints) > 0 {
		sb.WriteString(dim(" обслуживает: ") + strings.Join(cert.Endpoints, ", ") + "\n")
	}
	sb.WriteString("\n " + tag(renewalTone(cert), "обновление: "+renewalWord(cert)) + "\n")
	if cert.Renewal.Detail != "" {
		sb.WriteString(" " + dim(cert.Renewal.Detail) + "\n")
	}
	return sb.String()
}

func renewalWord(cert model.Certificate) string {
	prefix := ""
	if cert.Renewal.Derived {
		prefix = "копия certbot, "
	}
	switch {
	case cert.Renewal.Automatic:
		return prefix + "автоматическое"
	case cert.Renewal.Managed:
		return prefix + "настроено, но не запускается"
	case cert.Renewal.Tool == "certbot":
		return prefix + "запись certbot потеряна"
	default:
		return "вручную"
	}
}

func renewalTone(cert model.Certificate) string {
	switch {
	case cert.Renewal.Automatic:
		return hexGood
	case cert.Renewal.Managed:
		return hexWarning
	case cert.Renewal.Tool == "certbot":
		return hexCritical
	default:
		return hexMuted
	}
}

// certNames prefers the names in the certificate, falling back to the sites the
// configuration serves with it.
func certNames(cert model.Certificate) string {
	if len(cert.Names) > 0 {
		return strings.Join(cert.Names, ", ")
	}
	if len(cert.Sites) > 0 {
		return strings.Join(cert.Sites, ", ")
	}
	return cert.Path
}

// commonName pulls CN out of an RFC 2253 distinguished name for compact display.
func commonName(dn string) string {
	for _, part := range strings.Split(dn, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "CN=") {
			return strings.TrimPrefix(part, "CN=")
		}
	}
	return dn
}
