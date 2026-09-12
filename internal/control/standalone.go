package control

import (
	"bytes"
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	osuser "os/user"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/parse"
)

// certbot --standalone поднимает свой веб-сервер на 80/443, и порты
// должны быть свободны. Кто их держит — известно заранее: у слушателя
// есть pid, юнит, контейнер. Раньше останавливался фиксированный список
// (nginx, haproxy, caddy) вслепую: Flask, gunicorn или самописный
// сервер на 80 оставались, и certbot падал с «Problem binding to port».
//
// Теперь держатели опрашиваются до подтверждения и показываются
// оператору, а обращение с каждым зависит от того, что он такое:
//
//   - служба systemd — остановить и запустить обратно;
//   - процесс, запущенный руками, — по галочке остановить и поднять
//     заново той же командой, в том же каталоге, под тем же пользователем
//     и с тем же окружением; всё это читается из /proc;
//   - контейнер или процесс, который не прочитать, — не трогаем: порт
//     освобождает оператор.

// Виды держателей порта.
const (
	HolderService   = "service"
	HolderProcess   = "process"
	HolderContainer = "container"
	HolderUnknown   = "unknown"
)

// standalonePorts — что certbot --standalone занимает.
var standalonePorts = []int{80, 443}

// PortHolder — кто держит порт 80 или 443.
type PortHolder struct {
	Ports       []int  `json:"ports"`
	PID         int    `json:"pid,omitempty"`
	Process     string `json:"process,omitempty"`
	User        string `json:"user,omitempty"`
	Command     string `json:"command,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	UptimeS     int    `json:"uptime_s,omitempty"`
	Unit        string `json:"unit,omitempty"`
	ContainerID string `json:"container_id,omitempty"`
	Kind        string `json:"kind"`
	// Restartable — nkt может остановить и вернуть обратно.
	Restartable bool `json:"restartable"`

	argv []string
	env  []string
}

// StandalonePlan — что стоит на 80/443 и можно ли вообще начинать.
type StandalonePlan struct {
	Holders []PortHolder `json:"holders"`
	// Blocked — есть держатель, которого nkt убрать не может.
	Blocked bool `json:"blocked"`
}

// StandalonePlan опрашивает держателей 80/443.
func (m *CertManager) StandalonePlan(ctx context.Context) (StandalonePlan, error) {
	listeners, status := parse.Listeners(ctx, m.c)
	if !status.Available {
		return StandalonePlan{}, msgs.Errorf("control.couldReadListListeningSockets", status.Error)
	}
	var onPorts []model.Listener
	for _, l := range listeners {
		if strings.HasPrefix(strings.ToLower(l.Protocol), "udp") {
			continue
		}
		for _, p := range standalonePorts {
			if l.Port == p {
				onPorts = append(onPorts, l)
			}
		}
	}
	parse.EnrichListeners(ctx, m.c, onPorts)

	// Один процесс на обоих портах (nginx) — один держатель.
	byPID := map[int]*PortHolder{}
	plan := StandalonePlan{Holders: []PortHolder{}}
	for _, l := range onPorts {
		if h, ok := byPID[l.PID]; ok && l.PID > 0 {
			h.Ports = appendPort(h.Ports, l.Port)
			continue
		}
		h := PortHolder{
			Ports: []int{l.Port}, PID: l.PID, Process: l.Process, User: l.User,
			Command: l.Command, UptimeS: l.UptimeS, Unit: l.Unit, ContainerID: l.ContainerID,
		}
		switch {
		case l.ContainerID != "":
			h.Kind = HolderContainer
		case l.Unit != "":
			h.Kind = HolderService
			h.Restartable = true
		case l.PID > 0:
			h.Kind = HolderProcess
			h.argv, h.env, h.Cwd = m.processLaunch(ctx, l.PID)
			if len(h.argv) > 0 {
				h.Restartable = true
				h.Command = strings.Join(h.argv, " ")
			}
		default:
			h.Kind = HolderUnknown
		}
		if !h.Restartable {
			plan.Blocked = true
		}
		plan.Holders = append(plan.Holders, h)
		if l.PID > 0 {
			byPID[l.PID] = &plan.Holders[len(plan.Holders)-1]
		}
	}
	sort.Slice(plan.Holders, func(i, j int) bool { return plan.Holders[i].Ports[0] < plan.Holders[j].Ports[0] })
	return plan, nil
}

func appendPort(ports []int, p int) []int {
	for _, have := range ports {
		if have == p {
			return ports
		}
	}
	return append(ports, p)
}

// processLaunch читает из /proc то, чем процесс поднимают заново:
// команду целиком, окружение и рабочий каталог. Чего-то нет — процесс
// не перезапускаемый, и оператор освобождает порт сам.
func (m *CertManager) processLaunch(ctx context.Context, pid int) (argv, env []string, cwd string) {
	raw, err := m.c.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(raw) == 0 {
		return nil, nil, ""
	}
	argv = splitNUL(raw)
	if raw, err := m.c.ReadFile(fmt.Sprintf("/proc/%d/environ", pid)); err == nil {
		for _, kv := range splitNUL(raw) {
			// Служебные переменные systemd и терминала принадлежат старому
			// запуску, новому они только помешают.
			switch strings.SplitN(kv, "=", 2)[0] {
			case "INVOCATION_ID", "JOURNAL_STREAM", "LISTEN_FDS", "LISTEN_PID", "NOTIFY_SOCKET", "TERM_SESSION_ID", "_":
				continue
			}
			env = append(env, kv)
		}
	}
	if res, err := m.c.Run(ctx, "readlink", "-f", fmt.Sprintf("/proc/%d/cwd", pid)); err == nil && res.ExitCode == 0 {
		cwd = strings.TrimSpace(res.Stdout)
	}
	if cwd == "" {
		cwd = "/"
	}
	return argv, env, cwd
}

func splitNUL(raw []byte) []string {
	var out []string
	for _, part := range bytes.Split(bytes.TrimRight(raw, "\x00"), []byte{0}) {
		if len(part) > 0 {
			out = append(out, string(part))
		}
	}
	return out
}

// standaloneState — что было остановлено и что вернуть обратно.
type standaloneState struct {
	units []string
	procs []PortHolder
}

// freeStandalonePorts освобождает 80/443 по плану.
//
// restart — pid ручных процессов, которые оператор разрешил остановить и
// поднять заново. Процесс без разрешения, контейнер или неизвестный
// держатель — отказ до всякого certbot: он всё равно не смог бы занять
// порт, а отказ с именем держателя понятнее его «Problem binding».
func (m *CertManager) freeStandalonePorts(ctx context.Context, user string, plan StandalonePlan,
	restart map[int]bool, report *certProgress) (standaloneState, error) {
	var st standaloneState
	for _, h := range plan.Holders {
		switch h.Kind {
		case HolderService:
			report.Msg("certgen.stoppingUnit", h.Unit)
			if res, err := m.c.Run(ctx, "systemctl", "stop", h.Unit); err != nil {
				return st, msgs.Errorf("control.stopping", h.Unit, err)
			} else if res.ExitCode != 0 {
				return st, msgs.Errorf("control.stopping2", h.Unit, strings.TrimSpace(res.Output()))
			}
			m.db.Audit(ctx, user, "cert.standalone_stop", h.Unit, "ok", nil)
			st.units = append(st.units, h.Unit)
			report.Msg("certgen.serviceStopped", h.Unit)
		case HolderProcess:
			if !restart[h.PID] {
				return st, msgs.Errorf("control.portHeldProcessPidStop",
					portList(h.Ports), h.Process, h.PID, h.User)
			}
			report.Msg("certgen.stoppingProcess", h.Process, h.PID)
			if err := m.stopProcess(ctx, h.PID); err != nil {
				return st, err
			}
			m.db.Audit(ctx, user, "cert.standalone_kill", fmt.Sprintf("%s (pid %d)", h.Process, h.PID), "ok",
				map[string]any{"command": h.Command, "cwd": h.Cwd, "user": h.User})
			st.procs = append(st.procs, h)
			report.Msg("certgen.processStopped", h.Process, h.PID)
		default:
			return st, msgs.Errorf("control.portBusyNktCannotFree",
				portList(h.Ports), describeHolder(h))
		}
	}
	return st, nil
}

// stopProcess — SIGTERM, до 10 секунд ожидания, потом SIGKILL.
func (m *CertManager) stopProcess(ctx context.Context, pid int) error {
	spid := strconv.Itoa(pid)
	if res, err := m.c.Run(ctx, "kill", "-TERM", spid); err != nil {
		return msgs.Errorf("control.stoppingPid", pid, err)
	} else if res.ExitCode != 0 {
		return msgs.Errorf("control.stoppingPid2", pid, strings.TrimSpace(res.Output()))
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !m.processAlive(ctx, pid) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	_, _ = m.c.Run(ctx, "kill", "-KILL", spid)
	time.Sleep(300 * time.Millisecond)
	if m.processAlive(ctx, pid) {
		return msgs.Errorf("control.processPidDoesExitEven", pid)
	}
	return nil
}

func (m *CertManager) processAlive(ctx context.Context, pid int) bool {
	res, err := m.c.Run(ctx, "kill", "-0", strconv.Itoa(pid))
	return err == nil && res.ExitCode == 0
}

// restoreStandalone возвращает всё на место: службы — systemctl start,
// ручные процессы — заново той же командой. На собственном таймауте,
// независимо от запроса: обрыв соединения с браузером не должен быть
// причиной, по которой nginx остался лежать.
func (m *CertManager) restoreStandalone(user string, st standaloneState, report *certProgress) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*m.cfg.CommandTimeout+20*time.Second)
	defer cancel()
	for _, unit := range st.units {
		if res, err := m.c.Run(ctx, "systemctl", "start", unit); err != nil || res.ExitCode != 0 {
			detail := ""
			if err != nil {
				detail = err.Error()
			} else {
				detail = strings.TrimSpace(res.Output())
			}
			m.db.Audit(ctx, user, "cert.renew_restart_failed", unit, "error", detail)
			report.Msg("certgen.restartFailed", unit, detail)
			continue
		}
		m.db.Audit(ctx, user, "cert.standalone_start", unit, "ok", nil)
		report.Msg("certgen.serviceStarted", unit)
	}
	for _, h := range st.procs {
		if err := m.relaunch(ctx, h); err != nil {
			m.db.Audit(ctx, user, "cert.standalone_relaunch_failed", h.Command, "error", err.Error())
			report.Msg("certgen.relaunchFailed", h.Command, err.Error())
			continue
		}
		if m.waitListening(ctx, h.Ports, 15*time.Second) {
			report.Msg("certgen.processRelaunched", h.Command)
		} else {
			report.Msg("certgen.relaunchNotListening", h.Command, portList(h.Ports))
		}
	}
}

// relaunch поднимает процесс заново: под тем же пользователем, в том же
// каталоге, с тем же окружением.
//
// Через systemd-run, если есть выход из песочницы: процесс получает свой
// юнит и переживает перезапуск nkt. Иначе — setsid в фоне; тогда он
// потомок nkt, и перезапуск службы заберёт его с собой — об этом
// написано в подсказке окна.
func (m *CertManager) relaunch(ctx context.Context, h PortHolder) error {
	if len(h.argv) == 0 {
		return msgs.Errorf("control.launchCommandUnknown")
	}
	// Смена пользователя нужна только на чужого: nkt под root поднимает
	// процесс alex через runuser, а nkt, запущенный самим alex (стенд,
	// разработка), — напрямую, runuser ему и не разрешён.
	switchUser := h.User != "" && h.User != "root" && h.User != currentUserName()
	if m.escape != nil {
		unit := fmt.Sprintf("nkt-relaunch-%s-%d", safeDirName(h.Process), time.Now().Unix())
		argv := []string{"systemd-run", "--collect", "--quiet", "--unit=" + unit, "--working-directory=" + h.Cwd}
		if switchUser {
			argv = append(argv, "--uid="+h.User)
		}
		for _, kv := range h.env {
			argv = append(argv, "--setenv="+kv)
		}
		argv = append(argv, "--")
		argv = append(argv, h.argv...)
		if res, err := m.escape(ctx, argv...); err == nil && res.ExitCode == 0 {
			return nil
		}
		// systemd-run не сработал (нет dbus) — обычный запуск в фоне.
	}
	script := `cd "$1" && shift && exec "$@"`
	argv := []string{"setsid", "-f", "env", "-i"}
	argv = append(argv, h.env...)
	if switchUser {
		argv = append([]string{"runuser", "-u", h.User, "--"}, argv...)
	}
	argv = append(argv, "sh", "-c", script, "sh", h.Cwd)
	argv = append(argv, h.argv...)
	run := func(ctx context.Context, argv ...string) (collect.CommandResult, error) {
		if m.escape != nil {
			return m.escape(ctx, argv...)
		}
		return m.c.Run(ctx, argv[0], argv[1:]...)
	}
	res, err := run(ctx, argv...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(res.Output()))
	}
	return nil
}

// waitListening ждёт, пока порты снова кто-то не займёт.
func (m *CertManager) waitListening(ctx context.Context, ports []int, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		listeners, status := parse.Listeners(ctx, m.c)
		if status.Available {
			taken := 0
			for _, p := range ports {
				for _, l := range listeners {
					if l.Port == p {
						taken++
						break
					}
				}
			}
			if taken == len(ports) {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

func currentUserName() string {
	if u, err := osuser.Current(); err == nil {
		return u.Username
	}
	return ""
}

func portList(ports []int) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, strconv.Itoa(p))
	}
	return strings.Join(parts, "/")
}

func describeHolder(h PortHolder) string {
	switch h.Kind {
	case HolderContainer:
		return msgs.T(msgs.DefaultLang, "control.containerHolder", shortID(h.ContainerID), h.Process)
	case HolderUnknown:
		if h.Process != "" {
			return h.Process
		}
		return msgs.T(msgs.DefaultLang, "control.unknownProcess")
	}
	return fmt.Sprintf("%s (pid %d)", h.Process, h.PID)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
