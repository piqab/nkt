package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/msgs"
)

// ------------------------------------------------------------------- services

// serviceRow is what one line of the services table refers to. Units,
// docker containers and Podman containers share the table so there is a
// single selection to reason about.
type serviceRow struct {
	kind    string // unit | container | podman
	name    string
	actions []string
}

type servicesScreen struct {
	app *App

	table  *tview.Table
	detail *tview.TextView
	root   *tview.Flex
	rows   []serviceRow
}

func newServicesScreen(a *App) *servicesScreen {
	s := &servicesScreen{app: a}
	s.table = newTable(s.app.T("tui.services.colType"), s.app.T("tui.services.colName"),
		s.app.T("tui.services.colState"), s.app.T("tui.services.colDetails"), s.app.T("tui.services.colPortsMemory"))
	s.detail = newPanel(s.app.T("tui.services.selectedPanelTitle"))

	s.table.SetSelectionChangedFunc(func(row, _ int) { s.showDetail(row - 1) })
	s.table.SetInputCapture(s.onKey)

	s.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.table, 0, 3, true).
		AddItem(s.detail, 9, 0, false)
	return s
}

func (s *servicesScreen) title() string          { return s.app.T("tui.services.title") }
func (s *servicesScreen) view() tview.Primitive  { return s.root }
func (s *servicesScreen) focus() tview.Primitive { return s.table }

func (s *servicesScreen) hints() string {
	return dim(s.app.T("tui.services.hints"))
}

func (s *servicesScreen) selected() (serviceRow, bool) {
	row, _ := s.table.GetSelection()
	idx := row - 1
	if idx < 0 || idx >= len(s.rows) {
		return serviceRow{}, false
	}
	return s.rows[idx], true
}

func (s *servicesScreen) onKey(event *tcell.EventKey) *tcell.EventKey {
	var action string
	switch event.Rune() {
	case 's':
		action = "start"
	case 'x':
		action = "stop"
	case 't':
		action = "restart"
	case 'l':
		action = "reload"
	case 'c':
		action = "validate"
	default:
		return event
	}

	target, ok := s.selected()
	if !ok {
		return nil
	}
	if !contains(target.actions, action) {
		s.app.setStatus(hexWarning, s.app.T("tui.services.actionUnavailable", action, target.name))
		return nil
	}

	if action == "validate" {
		s.app.runAsync(s.app.T("tui.services.validatingConfig", target.name), false,
			func(ctx context.Context) (string, error) {
				res, ok, err := s.app.Services.ValidateOnly(ctx, s.app.actor, target.name)
				if err != nil {
					return "", err
				}
				if !ok {
					return s.app.T("tui.services.validationUnavailable"), nil
				}
				out := strings.TrimSpace(res.Output())
				if !res.OK() {
					return "", fmt.Errorf("%s", s.app.T("tui.services.configRejected", out))
				}
				return target.name + ": " + truncate(out, 120), nil
			})
		return nil
	}

	s.app.confirm(s.app.T("tui.services.confirmAction", action,
		map[string]string{
			"unit": s.app.T("tui.services.kindUnit"), "container": s.app.T("tui.services.kindContainer"),
			"podman": s.app.T("tui.services.kindPodman"), "lxd": s.app.T("tui.services.kindLXD"),
			"vm": s.app.T("tui.services.kindVM"),
		}[target.kind],
		target.name),
		func() {
			s.app.runAsync(fmt.Sprintf("%s %s", action, target.name), true,
				func(ctx context.Context) (string, error) {
					switch target.kind {
					case "container":
						if err := s.app.Services.ContainerAction(ctx, s.app.actor, target.name, action); err != nil {
							return "", err
						}
						return s.app.T("tui.services.containerActionDone", target.name, action), nil
					case "podman":
						if err := s.app.Podman.ContainerAction(ctx, s.app.actor, target.name, action); err != nil {
							return "", err
						}
						return s.app.T("tui.services.podmanActionDone", target.name, action), nil
					case "lxd":
						if err := s.app.LXD.InstanceAction(ctx, s.app.actor, target.name, action); err != nil {
							return "", err
						}
						return s.app.T("tui.services.lxdActionDone", target.name, action), nil
					case "vm":
						vmAction := action
						if vmAction == "stop" {
							vmAction = "shutdown"
						}
						if err := s.app.Libvirt.VMAction(ctx, s.app.actor, target.name, vmAction); err != nil {
							return "", err
						}
						return s.app.T("tui.services.vmActionDone", target.name, vmAction), nil
					}
					res, err := s.app.Services.Action(ctx, s.app.actor, target.name, action)
					if err != nil {
						return "", err
					}
					msg := s.app.T("tui.services.actionDone", target.name, action)
					if res.Simulated {
						msg += s.app.T("tui.services.simulatedSuffix")
					}
					return msg, nil
				})
		})
	return nil
}

func (s *servicesScreen) refresh(ctx context.Context) {
	snap, err := s.app.Scanner.LatestOrScan(ctx)
	if err != nil || snap == nil {
		return
	}

	s.app.queue(func() {
		s.rows = s.rows[:0]
		s.table.Clear()
		headers := []string{
			s.app.T("tui.services.colType"), s.app.T("tui.services.colName"), s.app.T("tui.services.colState"),
			s.app.T("tui.services.colDetails"), s.app.T("tui.services.colPortsMemory"),
		}
		for i, h := range headers {
			s.table.SetCell(0, i, tview.NewTableCell(" "+h).
				SetTextColor(colorSecondary).SetSelectable(false).SetAttributes(tcell.AttrBold))
		}

		row := 1
		for _, svc := range snap.Services {
			s.rows = append(s.rows, serviceRow{kind: "unit", name: svc.Name, actions: svc.Actions})
			s.table.SetCell(row, 0, cellDim("systemd"))
			s.table.SetCell(row, 1, cell(svc.Name))
			s.table.SetCell(row, 2, cellColor("●"+svc.ActiveState, stateColor(svc.ActiveState)))
			extra := svc.Description
			if !svc.Installed {
				extra = s.app.T("tui.services.notInstalled")
			}
			s.table.SetCell(row, 3, cellDim(truncate(extra, 46)))
			mem := "—"
			if svc.MemoryBytes > 0 {
				mem = formatBytes(s.app.Lang, float64(svc.MemoryBytes))
			}
			s.table.SetCell(row, 4, cellDim(fmt.Sprintf("pid %s · %s", intOrDash(svc.MainPID), mem)))
			row++
		}

		for _, ct := range snap.Container {
			s.rows = append(s.rows, serviceRow{
				kind: "container", name: ct.Name, actions: []string{"start", "stop", "restart"},
			})
			s.table.SetCell(row, 0, cellDim("docker"))
			s.table.SetCell(row, 1, cell(ct.Name))
			s.table.SetCell(row, 2, cellColor("●"+ct.State, stateColor(ct.State)))
			s.table.SetCell(row, 3, cellDim(truncate(ct.Image, 46)))
			s.table.SetCell(row, 4, cellDim(truncate(portSummary(s.app.Lang, ct), 40)))
			row++
		}

		for _, ct := range snap.Podman {
			s.rows = append(s.rows, serviceRow{
				kind: "podman", name: ct.Name, actions: []string{"start", "stop", "restart"},
			})
			s.table.SetCell(row, 0, cellDim("podman"))
			s.table.SetCell(row, 1, cell(ct.Name))
			s.table.SetCell(row, 2, cellColor("●"+ct.State, stateColor(ct.State)))
			s.table.SetCell(row, 3, cellDim(truncate(ct.Image, 46)))
			s.table.SetCell(row, 4, cellDim(truncate(podmanPortSummary(s.app.Lang, ct), 40)))
			row++
		}

		for _, inst := range snap.LXD {
			s.rows = append(s.rows, serviceRow{
				kind: "lxd", name: inst.Name, actions: []string{"start", "stop", "restart", "pause"},
			})
			s.table.SetCell(row, 0, cellDim("lxd"))
			s.table.SetCell(row, 1, cell(inst.Name))
			s.table.SetCell(row, 2, cellColor("●"+inst.Status, stateColor(inst.Status)))
			s.table.SetCell(row, 3, cellDim(inst.Type))
			s.table.SetCell(row, 4, cellDim(strings.Join(inst.IPv4, ", ")))
			row++
		}

		for _, vm := range snap.VMs {
			// Only the two safest lifecycle actions map onto this screen's
			// single-key shortcuts (s/x); destroy/reboot/suspend/resume and
			// undefine stay web-only, where each gets its own labelled
			// button and its own confirmation instead of sharing a key.
			// "stop" here is translated to VMAction's "shutdown" (graceful)
			// at dispatch time — see onKey — to share this screen's fixed
			// key vocabulary with every other row kind.
			s.rows = append(s.rows, serviceRow{
				kind: "vm", name: vm.Name, actions: []string{"start", "stop"},
			})
			s.table.SetCell(row, 0, cellDim("libvirt"))
			s.table.SetCell(row, 1, cell(vm.Name))
			s.table.SetCell(row, 2, cellColor("●"+vm.State, stateColor(vm.State)))
			s.table.SetCell(row, 3, cellDim(fmt.Sprintf("%d vCPU, %s", vm.VCPUs, formatBytes(s.app.Lang, float64(vm.MemoryKB*1024)))))
			s.table.SetCell(row, 4, cellDim(orDash(vm.UUID)))
			row++
		}
		s.table.SetTitle(s.app.T("tui.services.tableTitle", len(s.rows)))
		if len(s.rows) > 0 {
			s.showDetail(0)
		}
	})
}

func portSummary(lang msgs.Lang, ct model.Container) string {
	if len(ct.Ports) == 0 {
		return msgs.T(lang, "tui.services.noPortsPublished")
	}
	var parts []string
	for _, p := range ct.Ports {
		if p.HostPort == 0 {
			continue
		}
		ip := p.HostIP
		if ip == "" {
			ip = "0.0.0.0"
		}
		parts = append(parts, fmt.Sprintf("%s:%d→%d", ip, p.HostPort, p.ContainerPort))
	}
	if len(parts) == 0 {
		return msgs.T(lang, "tui.services.noPortsPublished")
	}
	return strings.Join(parts, " ")
}

func podmanPortSummary(lang msgs.Lang, ct model.PodmanContainer) string {
	if len(ct.Ports) == 0 {
		return msgs.T(lang, "tui.services.noPortsPublished")
	}
	var parts []string
	for _, p := range ct.Ports {
		if p.HostPort == 0 {
			continue
		}
		ip := p.HostIP
		if ip == "" {
			ip = "0.0.0.0"
		}
		parts = append(parts, fmt.Sprintf("%s:%d→%d", ip, p.HostPort, p.ContainerPort))
	}
	if len(parts) == 0 {
		return msgs.T(lang, "tui.services.noPortsPublished")
	}
	return strings.Join(parts, " ")
}

func (s *servicesScreen) showDetail(index int) {
	snap := s.app.Scanner.Latest()
	if snap == nil || index < 0 || index >= len(s.rows) {
		return
	}
	target := s.rows[index]
	var sb strings.Builder

	switch target.kind {
	case "unit":
		for _, svc := range snap.Services {
			if svc.Name != target.name {
				continue
			}
			sb.WriteString(" " + bold(svc.Name) + dim(s.app.T("tui.services.unitSuffix", svc.Unit)) + "\n")
			sb.WriteString(" " + dim(svc.Description) + "\n\n")
			sb.WriteString(s.app.T("tui.services.stateLineUnit",
				tag(stateColor(svc.ActiveState), svc.ActiveState), dim(svc.SubState),
				orDash(svc.Enabled), svc.Restarts))
			if svc.SinceText != "" {
				sb.WriteString(dim(s.app.T("tui.services.runningSince", svc.SinceText)))
			}
			if len(svc.ConfigFiles) > 0 {
				sb.WriteString(dim(s.app.T("tui.services.configFilesCount", len(svc.ConfigFiles))) +
					truncate(strings.Join(svc.ConfigFiles, ", "), 100) + "\n")
			}
			sb.WriteString(dim(s.app.T("tui.services.availableActions", strings.Join(svc.Actions, ", "))))
		}
	case "podman":
		for _, ct := range snap.Podman {
			if ct.Name != target.name {
				continue
			}
			sb.WriteString(" " + bold(ct.Name) + dim(" · "+ct.Image) + "\n")
			sb.WriteString(s.app.T("tui.services.stateBasic",
				tag(stateColor(ct.State), ct.State), dim(ct.Status)))
			if ct.Pod != "" {
				sb.WriteString(dim(s.app.T("tui.services.podLabel", ct.Pod)))
			}
			sb.WriteString(s.app.T("tui.services.portsPrefix") + podmanPortSummary(s.app.Lang, ct))
		}
	case "lxd":
		for _, inst := range snap.LXD {
			if inst.Name != target.name {
				continue
			}
			sb.WriteString(" " + bold(inst.Name) + dim(" · "+inst.Type) + "\n")
			sb.WriteString(s.app.T("tui.services.stateArch",
				tag(stateColor(inst.Status), inst.Status), orDash(inst.Architecture)))
			if len(inst.IPv4) > 0 {
				sb.WriteString(dim(s.app.T("tui.services.ipv4Prefix")) + strings.Join(inst.IPv4, ", "))
			} else {
				sb.WriteString(dim(s.app.T("tui.services.ipv4NoData")))
			}
		}
	case "vm":
		for _, vm := range snap.VMs {
			if vm.Name != target.name {
				continue
			}
			sb.WriteString(" " + bold(vm.Name) + dim(" · "+orDash(vm.UUID)) + "\n")
			sb.WriteString(s.app.T("tui.services.stateVCPUMem",
				tag(stateColor(vm.State), vm.State), vm.VCPUs, formatBytes(s.app.Lang, float64(vm.MemoryKB*1024))))
			sb.WriteString(s.app.T("tui.services.persistentAutostart",
				boolState(vm.Persistent), boolState(vm.Autostart)))
			var disks []string
			for _, d := range vm.Disks {
				disks = append(disks, fmt.Sprintf("%s(%s)", d.Source, d.Bus))
			}
			sb.WriteString(dim(s.app.T("tui.services.disksPrefix")) + strings.Join(disks, ", ") + "\n")
			var nets []string
			for _, n := range vm.Networks {
				nets = append(nets, n.Source)
			}
			sb.WriteString(dim(s.app.T("tui.services.networksPrefix")) + strings.Join(nets, ", "))
		}
	default:
		for _, ct := range snap.Container {
			if ct.Name != target.name {
				continue
			}
			sb.WriteString(" " + bold(ct.Name) + dim(" · "+ct.Image) + "\n")
			sb.WriteString(s.app.T("tui.services.stateBasic",
				tag(stateColor(ct.State), ct.State), dim(ct.Status)))
			if ct.Project != "" {
				sb.WriteString(dim(s.app.T("tui.services.projectServiceFile",
					ct.Project, ct.ServiceName, ct.ComposeFile)))
			}
			sb.WriteString(s.app.T("tui.services.portsPrefix") + portSummary(s.app.Lang, ct) + "\n")
			var nets []string
			for _, n := range ct.Networks {
				if n.IPAddress != "" {
					nets = append(nets, n.Name+" ("+n.IPAddress+")")
				} else {
					nets = append(nets, n.Name)
				}
			}
			sb.WriteString(dim(s.app.T("tui.services.networksPrefix")) + strings.Join(nets, ", "))
			if !ct.Declared {
				sb.WriteString("\n " + tag(hexWarning, s.app.T("tui.services.runningOutsideCompose")))
			}
			if ct.Declared && !ct.Running {
				sb.WriteString("\n " + tag(hexWarning, s.app.T("tui.services.declaredNotRunning")))
			}
		}
	}
	s.detail.SetText(sb.String())
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------------- firewall

// firewallRow ties a table line either to a deletable ufw rule or to a
// read-only packet filter rule.
type firewallRow struct {
	ufwNumber int    // 0 when the rule is not managed through ufw
	ufwText   string // exact text shown, checked again before deleting
}

type firewallScreen struct {
	app *App

	table  *tview.Table
	detail *tview.TextView
	root   *tview.Flex
	rows   []firewallRow
}

func newFirewallScreen(a *App) *firewallScreen {
	s := &firewallScreen{app: a}
	s.table = newTable(s.app.T("tui.firewall.colNumber"), s.app.T("tui.firewall.colRuleSource"),
		s.app.T("tui.firewall.colChain"), s.app.T("tui.firewall.colAction"), s.app.T("tui.firewall.colPort"),
		s.app.T("tui.firewall.colFrom"), s.app.T("tui.firewall.colPackets"), s.app.T("tui.firewall.colBytes"))
	s.detail = newPanel(s.app.T("tui.firewall.statusPanelTitle"))

	s.table.SetInputCapture(s.onKey)
	s.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(s.table, 0, 3, true).
		AddItem(s.detail, 8, 0, false)
	return s
}

func (s *firewallScreen) title() string          { return "Firewall" }
func (s *firewallScreen) view() tview.Primitive  { return s.root }
func (s *firewallScreen) focus() tview.Primitive { return s.table }
func (s *firewallScreen) hints() string {
	return dim(s.app.T("tui.firewall.hints"))
}

func (s *firewallScreen) onKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 'a', 'A':
		s.showAddForm()
		return nil
	case 'x', 'X':
		s.deleteSelected()
		return nil
	}
	return event
}

func (s *firewallScreen) deleteSelected() {
	row, _ := s.table.GetSelection()
	idx := row - 1
	if idx < 0 || idx >= len(s.rows) || s.rows[idx].ufwNumber == 0 {
		s.app.setStatus(hexWarning, s.app.T("tui.firewall.onlyUfwDeletable"))
		return
	}
	target := s.rows[idx]
	s.app.confirm(s.app.T("tui.firewall.confirmDelete", target.ufwNumber, target.ufwText), func() {
		s.app.runAsync(s.app.T("tui.firewall.deletingRule", target.ufwNumber), true,
			func(ctx context.Context) (string, error) {
				res, err := s.app.Firewall.DeleteRule(ctx, s.app.actor, target.ufwNumber, target.ufwText)
				if err != nil {
					return "", err
				}
				go s.app.rescan(context.Background())
				return s.app.T("tui.firewall.ruleDeleted", truncate(strings.TrimSpace(res.Output()), 80)), nil
			})
	})
}

func (s *firewallScreen) showAddForm() {
	if !s.app.canMutate() {
		s.app.setStatus(hexWarning, s.app.T("tui.mutationsDisabled"))
		return
	}
	spec := control.RuleSpec{Action: "allow", Protocol: "tcp"}
	actions := []string{"allow", "deny", "reject", "limit"}
	protocols := []string{"tcp", "udp"}
	port, from, comment := "", "", ""

	form := tview.NewForm().
		AddDropDown(s.app.T("tui.firewall.colAction"), actions, 0, func(option string, _ int) { spec.Action = option }).
		AddInputField(s.app.T("tui.firewall.colPort"), "", 8, tview.InputFieldInteger, func(text string) { port = text }).
		AddDropDown(s.app.T("tui.firewall.fieldProtocol"), protocols, 0, func(option string, _ int) { spec.Protocol = option }).
		AddInputField(s.app.T("tui.firewall.fieldSource"), "", 24, nil, func(text string) { from = text }).
		AddInputField(s.app.T("tui.firewall.fieldComment"), "", 40, nil, func(text string) { comment = text })

	form.AddButton(s.app.T("tui.firewall.addButton"), func() {
		spec.Port, _ = strconv.Atoi(port)
		spec.From, spec.Comment = strings.TrimSpace(from), strings.TrimSpace(comment)
		if err := spec.Validate(); err != nil {
			s.app.setStatus(hexCritical, "✖ "+err.Error())
			return
		}
		s.app.closeModal("fwadd")
		s.app.runAsync(s.app.T("tui.firewall.addingRule", spec.Action, spec.Port, spec.Protocol), true,
			func(ctx context.Context) (string, error) {
				res, err := s.app.Firewall.AddRule(ctx, s.app.actor, spec)
				if err != nil {
					return "", err
				}
				go s.app.rescan(context.Background())
				return s.app.T("tui.firewall.ruleAdded", truncate(strings.TrimSpace(res.Output()), 80)), nil
			})
	})
	form.AddButton(s.app.T("tui.cancel"), func() { s.app.closeModal("fwadd") })

	form.SetBorder(true).SetTitle(s.app.T("tui.firewall.newRuleFormTitle")).SetBorderColor(colorBorder)
	form.SetCancelFunc(func() { s.app.closeModal("fwadd") })

	s.app.editing = true
	s.app.showModal("fwadd", form, 76, 15)
}

func (s *firewallScreen) refresh(ctx context.Context) {
	snap, err := s.app.Scanner.LatestOrScan(ctx)
	if err != nil || snap == nil {
		return
	}
	// Rule numbers come from ufw itself: they shift after every change, and the
	// parsed snapshot is not authoritative about them.
	numbered, numErr := s.app.Firewall.NumberedRules(ctx)

	s.app.queue(func() {
		s.rows = s.rows[:0]
		s.table.Clear()
		headers := []string{
			s.app.T("tui.firewall.colNumber"), s.app.T("tui.firewall.colRuleSource"), s.app.T("tui.firewall.colChain"),
			s.app.T("tui.firewall.colAction"), s.app.T("tui.firewall.colPort"), s.app.T("tui.firewall.colFrom"),
			s.app.T("tui.firewall.colPackets"), s.app.T("tui.firewall.colBytes"),
		}
		for i, h := range headers {
			s.table.SetCell(0, i, tview.NewTableCell(" "+h).
				SetTextColor(colorSecondary).SetSelectable(false).SetAttributes(tcell.AttrBold))
		}

		row := 1
		for _, r := range numbered {
			s.rows = append(s.rows, firewallRow{ufwNumber: r.Number, ufwText: r.Text})
			s.table.SetCell(row, 0, cellColor(strconv.Itoa(r.Number), hexSeries1))
			s.table.SetCell(row, 1, cellDim("ufw"))
			s.table.SetCell(row, 2, cellDim("user-input"))
			s.table.SetCell(row, 3, cell(truncate(r.Text, 60)))
			s.table.SetCell(row, 4, cellDim(""))
			s.table.SetCell(row, 5, cellDim(""))
			s.table.SetCell(row, 6, cellDim(""))
			s.table.SetCell(row, 7, cellDim(""))
			row++
		}

		for _, r := range snap.Firewall.Rules {
			if r.Backend == "ufw" || r.Backend == "ufw6" {
				continue // already listed above, with their real numbers
			}
			s.rows = append(s.rows, firewallRow{})
			s.table.SetCell(row, 0, cellDim("—"))
			s.table.SetCell(row, 1, cellDim(r.Backend+"/"+r.Table))
			s.table.SetCell(row, 2, cellDim(r.Chain))
			action := r.Action
			if r.DNATTo != "" {
				action += " → " + r.DNATTo
			}
			s.table.SetCell(row, 3, cellColor(action, actionColor(r.Action)))
			portSpec := r.PortSpec
			if portSpec == "" {
				portSpec = "—"
			} else if r.Protocol != "" {
				portSpec += "/" + r.Protocol
			}
			s.table.SetCell(row, 4, cell(portSpec))
			s.table.SetCell(row, 5, cellDim(orAny(s.app.Lang, r.Source)))
			s.table.SetCell(row, 6, cellRight(formatCount(float64(r.Packets))))
			s.table.SetCell(row, 7, cellRight(formatBytes(s.app.Lang, float64(r.Bytes))))
			row++
		}
		s.table.SetTitle(s.app.T("tui.firewall.tableTitle", len(numbered), len(s.rows)-len(numbered)))

		var sb strings.Builder
		ufw := snap.Firewall.Manager("ufw")
		sb.WriteString(" ufw: " + tag(stateColor(boolState(ufw.Active)), boolState(ufw.Active)))
		if ufw.Policy != "" {
			sb.WriteString(dim("   " + ufw.Policy))
		}
		if fd := snap.Firewall.Manager("firewalld"); fd.Installed {
			sb.WriteString("\n firewalld: " + tag(stateColor(boolState(fd.Active)), boolState(fd.Active)))
			if fd.Policy != "" {
				sb.WriteString(dim("   " + s.app.T("tui.firewall.defaultZonePrefix") + fd.Policy))
			}
		}
		if numErr != nil {
			sb.WriteString("\n " + tag(hexWarning, s.app.T("tui.firewall.ufwRulesUnavailable", numErr.Error())))
		}
		sb.WriteString("\n\n")
		for _, p := range snap.Firewall.Policies {
			if p.Table != "filter" || p.Policy == "-" {
				continue
			}
			tone := hexWarning
			if p.Policy == "DROP" || p.Policy == "REJECT" {
				tone = hexGood
			}
			sb.WriteString(fmt.Sprintf(" %-24s %s %s\n", p.Backend+"/"+p.Chain, tag(tone, p.Policy),
				dim(s.app.T("tui.firewall.packetsBytes", formatCount(float64(p.Packets)), formatBytes(s.app.Lang, float64(p.Bytes))))))
		}
		sb.WriteString(dim(s.app.T("tui.firewall.openSockets", len(snap.Listeners))))
		s.detail.SetText(sb.String())
	})
}

func actionColor(action string) string {
	switch strings.ToUpper(action) {
	case "ACCEPT", "ALLOW":
		return hexGood
	case "DROP", "REJECT", "DENY":
		return hexCritical
	case "DNAT", "MASQUERADE":
		return hexSeries2
	default:
		return hexMuted
	}
}

func orAny(lang msgs.Lang, s string) string {
	if s == "" {
		return msgs.T(lang, "tui.firewall.any")
	}
	return s
}
