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

	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
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
	s.table = newTable(a.T("tui.certs.colSites"), a.T("tui.certs.colExpires"), a.T("tui.certs.colRemaining"),
		a.T("tui.certs.colKey"), a.T("tui.certs.colIssuer"), a.T("tui.certs.colAutoRenew"))
	s.detail = newPanel(a.T("tui.certs.detailTitle"))
	s.schedule = newPanel(a.T("tui.certs.scheduleTitle"))

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

func (s *certsScreen) title() string          { return s.app.T("tui.certs.title") }
func (s *certsScreen) view() tview.Primitive  { return s.root }
func (s *certsScreen) focus() tview.Primitive { return s.table }
func (s *certsScreen) hints() string {
	return dim(s.app.T("tui.certs.hints"))
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
		s.app.setStatus(hexWarning, s.app.T("tui.mutationsDisabled"))
		return
	}

	names, service, bits, days := "", "nginx", "2048", "397"
	form := tview.NewForm().
		AddInputField(s.app.T("tui.certs.formNames"), "", 44, nil, func(t string) { names = t }).
		AddDropDown(s.app.T("tui.certs.formService"), []string{"nginx", "haproxy"}, 0, func(o string, _ int) { service = o }).
		AddDropDown(s.app.T("tui.certs.formKeyBits"), []string{"2048", "3072", "4096"}, 0, func(o string, _ int) { bits = o }).
		AddInputField(s.app.T("tui.certs.formDays"), days, 8, tview.InputFieldInteger, func(t string) { days = t })

	form.AddButton(s.app.T("tui.certs.createButton"), func() {
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
		s.app.runAsync(s.app.T("tui.certs.generatingSelfSigned"), true, func(ctx context.Context) (string, error) {
			res, err := s.app.Certs.GenerateSelfSigned(ctx, s.app.actor, req)
			if err != nil {
				return "", err
			}
			s.app.queue(func() {
				body := s.app.T("tui.certs.certValidUntil", bold(strings.Join(res.Names, ", ")),
					res.NotAfter.Local().Format("02.01.2006")) +
					" " + s.app.T("tui.certs.configNotAddedHint", tview.Escape(res.Snippet))
				s.app.showText("certgen-result", s.app.T("tui.certs.certCreatedTitle"), body)
			})
			return s.app.T("tui.certs.certCreatedStatus", res.Fingerprint[:16]), nil
		})
	})
	form.AddButton(s.app.T("tui.cancel"), func() { s.app.closeModal("certgen") })

	form.SetBorder(true).SetTitle(" " + s.app.T("tui.certs.generateFormTitle") + " ").SetBorderColor(colorBorder)
	form.SetCancelFunc(func() { s.app.closeModal("certgen") })

	s.app.editing = true
	s.app.showModal("certgen", form, 74, 13)
}

// lineageLabel describes one /etc/letsencrypt/live lineage with its expiry,
// so picking one in the dropdown doesn't require checking the certificates
// table first.
func lineageLabel(lang msgs.Lang, info control.LineageInfo) string {
	if !info.Known {
		return msgs.T(lang, "tui.certs.lineageExpiryUnknown", info.Name)
	}
	switch {
	case info.DaysLeft < 0:
		return msgs.T(lang, "tui.certs.lineageExpiredAgo", info.Name, -info.DaysLeft)
	case info.DaysLeft == 0:
		return msgs.T(lang, "tui.certs.lineageExpiresToday", info.Name)
	default:
		return msgs.T(lang, "tui.certs.lineageDaysLeft", info.Name, info.DaysLeft)
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
		s.app.setStatus(hexWarning, s.app.T("tui.mutationsDisabled"))
		return
	}

	lineages, err := s.app.Certs.ListLetsEncryptLineages()
	if err != nil {
		s.app.setStatus(hexWarning, s.app.T("tui.certs.letsencryptReadError", err.Error()))
		return
	}
	if len(lineages) == 0 {
		s.app.setStatus(hexWarning, s.app.T("tui.certs.noLineagesFound"))
		return
	}
	lineageLabels := make([]string, len(lineages))
	for i, info := range lineages {
		lineageLabels[i] = lineageLabel(s.app.Lang, info)
	}
	pathOptions := append([]string{s.app.T("tui.certs.newFileOption")}, s.app.Certs.ListHAProxyCertPaths()...)

	lineage := lineages[0].Name
	targetPath := ""
	form := tview.NewForm().
		AddDropDown("Lineage", lineageLabels, 0, func(_ string, i int) { lineage = lineages[i].Name }).
		AddDropDown(s.app.T("tui.certs.formTargetPath"), pathOptions, 0, func(o string, i int) {
			if i == 0 {
				targetPath = ""
			} else {
				targetPath = o
			}
		})

	form.AddButton(s.app.T("tui.certs.combineButton"), func() {
		s.app.closeModal("certcombine")
		s.app.runAsync(s.app.T("tui.certs.combiningPEM"), true, func(ctx context.Context) (string, error) {
			res, err := s.app.Certs.CombineForHAProxy(ctx, s.app.actor, lineage, targetPath)
			if err != nil {
				return "", err
			}
			s.app.queue(func() {
				body := s.app.T("tui.certs.certValidUntil", bold(res.Lineage), res.NotAfter.Local().Format("02.01.2006"))
				if res.Snippet != "" {
					body += s.app.T("tui.certs.configNotAddedHint", tview.Escape(res.Snippet))
				} else {
					body += s.app.T("tui.certs.overwrittenNoInsertNeeded", res.CombinedPath)
				}
				s.app.showText("certcombine-result", s.app.T("tui.certs.pemCreatedTitle"), body)
			})
			return s.app.T("tui.certs.pemCreatedStatus", res.Fingerprint[:16]), nil
		})
	})
	form.AddButton(s.app.T("tui.cancel"), func() { s.app.closeModal("certcombine") })

	form.SetBorder(true).SetTitle(" " + s.app.T("tui.certs.combineFormTitle") + " ").SetBorderColor(colorBorder)
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
		s.app.setStatus(hexWarning, s.app.T("tui.certs.notManagedByCertbot"))
		return
	}
	if !s.app.canMutate() {
		s.app.setStatus(hexWarning, s.app.T("tui.mutationsDisabled"))
		return
	}

	lineage := cert.Renewal.Lineage
	question := s.app.T("tui.certs.confirmRenew", lineage)
	if cert.Renewal.Derived {
		question += s.app.T("tui.certs.confirmRenewDerivedNote", cert.Renewal.SourcePath)
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
	view.SetText(" " + dim(s.app.T("tui.certs.starting")))
	view.SetBorder(true).
		SetTitle(" " + s.app.T("tui.certs.renewProgressTitle", lineage) + " ").
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
					view.SetText(renderRenewLog(s.app.Lang, events, done, errMsg))
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
func renderRenewLog(lang msgs.Lang, events []control.RenewEvent, done bool, errMsg string) string {
	var sb strings.Builder
	for _, e := range events {
		sb.WriteString(fmt.Sprintf(" [%s] %s\n", e.Time.Local().Format("15:04:05"), tview.Escape(e.Text)))
	}
	if done {
		sb.WriteString("\n")
		if errMsg != "" {
			sb.WriteString(" " + tag(hexCritical, msgs.T(lang, "tui.certs.renewError", tview.Escape(errMsg))))
		} else {
			sb.WriteString(" " + tag(hexGood, msgs.T(lang, "tui.certs.renewDone")))
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
func expiryWord(lang msgs.Lang, cert model.Certificate) string {
	switch {
	case cert.Error != "":
		return msgs.T(lang, "tui.certs.unreadable")
	case cert.DaysLeft < 0:
		return msgs.T(lang, "tui.certs.expiredDaysAgo", -cert.DaysLeft)
	case cert.DaysLeft == 0:
		return msgs.T(lang, "tui.certs.expiresToday")
	default:
		return msgs.T(lang, "tui.certs.daysLeft", cert.DaysLeft)
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
		headers := []string{
			s.app.T("tui.certs.colSites"), s.app.T("tui.certs.colExpires"), s.app.T("tui.certs.colRemaining"),
			s.app.T("tui.certs.colKey"), s.app.T("tui.certs.colIssuer"), s.app.T("tui.certs.colAutoRenew"),
		}
		for i, h := range headers {
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
			s.table.SetCell(row, 2, cellColor("●"+expiryWord(s.app.Lang, cert), expiryTone(cert)))
			key := "—"
			if cert.KeyAlgorithm != "" {
				key = fmt.Sprintf("%s %d", cert.KeyAlgorithm, cert.KeyBits)
			}
			s.table.SetCell(row, 3, cellDim(key))
			s.table.SetCell(row, 4, cellDim(truncate(commonName(cert.Issuer), 24)))
			s.table.SetCell(row, 5, cellColor(renewalWord(s.app.Lang, cert), renewalTone(cert)))
		}
		s.table.SetTitle(" " + s.app.T("tui.certs.titleCount", len(snap.Certs)) + " ")

		s.schedule.SetText(s.renderSchedule(snap.Certs))
		if len(snap.Certs) > 0 {
			s.showDetail(0)
		} else {
			s.detail.SetText("\n " + dim(s.app.T("tui.certs.noSslCertificates")))
		}
	})
}

// renderSchedule draws the runway of every certificate on one scale, so the
// order in which they will break is visible at a glance.
func (s *certsScreen) renderSchedule(certs []model.Certificate) string {
	if len(certs) == 0 {
		return "\n " + dim(s.app.T("tui.certs.scheduleNoData"))
	}
	var sb strings.Builder
	sb.WriteString(dim(s.app.T("tui.certs.scheduleHeader")))

	for _, cert := range certs {
		name := truncate(certNames(cert), 22)
		if cert.Error != "" {
			sb.WriteString(fmt.Sprintf(" %-22s %s\n", name, tag(hexCritical, s.app.T("tui.certs.fileUnreadable"))))
			continue
		}
		fraction := float64(cert.DaysLeft) / certScaleDays
		if fraction < 0 {
			fraction = 0
		}
		sb.WriteString(fmt.Sprintf(" %-22s %s %s\n",
			name, hbar(fraction, 26, expiryTone(cert)),
			tag(expiryTone(cert), expiryWord(s.app.Lang, cert))))
	}

	sb.WriteString("\n" + dim(s.app.T("tui.certs.scheduleThresholds", certWarnDays, certCriticalDays)))
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
		sb.WriteString(" " + tag(hexCritical, s.app.T("tui.certs.certErrorLabel", cert.Error)) + "\n")
		return sb.String()
	}

	sb.WriteString(s.app.T("tui.certs.detailValidity",
		cert.NotBefore.Local().Format("02.01.2006"),
		cert.NotAfter.Local().Format("02.01.2006"),
		tag(expiryTone(cert), "●"+expiryWord(s.app.Lang, cert))))
	sb.WriteString(dim(s.app.T("tui.certs.issuerLabel")) + truncate(cert.Issuer, 60) + "\n")
	sb.WriteString(dim(s.app.T("tui.certs.subjectLabel")) + truncate(cert.Subject, 60) + "\n")
	sb.WriteString(dim(s.app.T("tui.certs.keyLabel")) + s.app.T("tui.certs.keyBitsFormat", cert.KeyAlgorithm, cert.KeyBits) +
		dim(s.app.T("tui.certs.sigLabel")) + cert.SigAlgorithm + "\n")
	sb.WriteString(dim(s.app.T("tui.certs.chainLabel")) + s.app.T("tui.certs.chainCountFormat", cert.ChainLength))
	if cert.SelfSigned {
		sb.WriteString("   " + tag(hexWarning, s.app.T("tui.certs.selfSignedLabel")))
	}
	sb.WriteString("\n")

	if len(cert.Endpoints) > 0 {
		sb.WriteString(dim(s.app.T("tui.certs.servesLabel")) + strings.Join(cert.Endpoints, ", ") + "\n")
	}
	sb.WriteString("\n " + tag(renewalTone(cert), s.app.T("tui.certs.renewalLabel", renewalWord(s.app.Lang, cert))) + "\n")
	if cert.Renewal.Detail != "" {
		sb.WriteString(" " + dim(cert.Renewal.Detail) + "\n")
	}
	return sb.String()
}

func renewalWord(lang msgs.Lang, cert model.Certificate) string {
	prefix := ""
	if cert.Renewal.Derived {
		prefix = msgs.T(lang, "tui.certs.derivedCopyPrefix")
	}
	switch {
	case cert.Renewal.Automatic:
		return prefix + msgs.T(lang, "tui.certs.renewalAutomatic")
	case cert.Renewal.Managed:
		return prefix + msgs.T(lang, "tui.certs.renewalConfiguredNotRunning")
	case cert.Renewal.Tool == "certbot":
		return prefix + msgs.T(lang, "tui.certs.renewalCertbotRecordLost")
	default:
		return msgs.T(lang, "tui.certs.renewalManual")
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
