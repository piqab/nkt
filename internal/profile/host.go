package profile

import (
	"context"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/model"
)

// HostReader читает действительное состояние машины.
//
// Часть берётся из уже собранной инвентаризации (пакетный фильтр —
// сканирование его и так делает), часть спрашивается у системы прямо
// сейчас: состояние конкретной службы и наличие пакета в снапшоте не
// лежат, а спрашивать их дёшево.
type HostReader struct {
	c       collect.Collector
	scanner *inventory.Scanner
	osusers *control.OSUserManager
	sysconf *control.SysConfigManager
}

// NewHostReader строит читателя состояния.
func NewHostReader(c collect.Collector, scanner *inventory.Scanner,
	osusers *control.OSUserManager, sysconf *control.SysConfigManager) *HostReader {
	return &HostReader{c: c, scanner: scanner, osusers: osusers, sysconf: sysconf}
}

// InstalledPackages спрашивает dpkg-query об именно этих пакетах.
//
// Не «список всех установленных»: их тысячи, а нужны единицы. Пакет
// считается установленным только в состоянии ii — «полуустановленный»
// после оборванного apt это не то, на что можно положиться.
func (h *HostReader) InstalledPackages(ctx context.Context, names []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(names) == 0 {
		return out, nil
	}
	argv := append([]string{"-W", "-f", "${Package} ${db:Status-Abbrev}\\n"}, names...)
	res, err := h.c.Run(ctx, "dpkg-query", argv...)
	if err != nil {
		return nil, err
	}
	// Ненулевой код здесь — обычное дело: dpkg-query ругается на каждый
	// неизвестный ему пакет, но про известные всё равно печатает.
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		out[fields[0]] = strings.HasPrefix(fields[1], "ii")
	}
	if res.ExitCode != 0 && len(out) == 0 && strings.TrimSpace(res.Stderr) != "" {
		return nil, fmt.Errorf("dpkg-query: %s", strings.TrimSpace(firstLine(res.Stderr)))
	}
	return out, nil
}

// ServiceState спрашивает systemd о конкретном юните.
func (h *HostReader) ServiceState(ctx context.Context, name string) (installed, enabled, active bool, err error) {
	unit := name
	if !strings.Contains(unit, ".") {
		unit += ".service"
	}
	res, err := h.c.Run(ctx, "systemctl", "show", unit,
		"--property=LoadState,UnitFileState,ActiveState", "--no-pager")
	if err != nil {
		return false, false, false, err
	}
	props := map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			props[k] = v
		}
	}
	load := props["LoadState"]
	fileState := props["UnitFileState"]
	installed = load != "" && load != "not-found"
	enabled = fileState == "enabled" || fileState == "enabled-runtime" || fileState == "static"
	active = props["ActiveState"] == "active"
	return installed, enabled, active, nil
}

// FileContent читает файл целиком.
func (h *HostReader) FileContent(_ context.Context, path string) (string, bool, error) {
	if !h.c.Exists(path) {
		return "", false, nil
	}
	raw, err := h.c.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

// ComposeRunning считает работающие контейнеры стека.
//
// «docker compose ps -q» отдаёт по строке на контейнер и ничего не
// печатает, когда стека нет вовсе; отсутствие файла — не ошибка (его ещё
// только предстоит записать), а вот молчащий docker — ошибка, и попасть
// она должна в «не знаю», а не в «ни одного контейнера».
func (h *HostReader) ComposeRunning(ctx context.Context, path string) (int, error) {
	if !h.c.Exists(path) {
		return 0, nil
	}
	res, err := h.c.Run(ctx, "docker", "compose", "-f", path, "ps", "-q")
	if err != nil {
		return 0, err
	}
	if !res.OK() {
		return 0, fmt.Errorf("docker compose ps: %s", lastMeaningfulLine(res.Stderr, res.Stdout))
	}
	n := 0
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

// FirewallState берёт то, что уже собрало сканирование: гонять
// iptables-save ради плана незачем.
func (h *HostReader) FirewallState(ctx context.Context) (model.FirewallState, error) {
	snap, err := h.scanner.LatestOrScan(ctx)
	if err != nil {
		return model.FirewallState{}, err
	}
	return snap.Firewall, nil
}

// Users отдаёт системные учётки с полными ключами.
func (h *HostReader) Users(ctx context.Context) (map[string]UserState, error) {
	if h.osusers == nil {
		return nil, fmt.Errorf("управление учётными записями недоступно")
	}
	list, err := h.osusers.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]UserState, len(list))
	for _, u := range list {
		// Ключи читаются файлом, а не берутся из списка: там они
		// намеренно усечены для показа, а сравнивать усечённые нельзя —
		// два разных ключа одного вида отличаются как раз серединой.
		out[u.Name] = UserState{Sudo: u.Sudo, Keys: h.authorizedKeys(u.Home)}
	}
	return out, nil
}

// authorizedKeys читает ключи учётной записи целиком. Нечитаемый или
// отсутствующий файл — это просто «ключей нет»: у учётки может не быть
// домашнего каталога вовсе.
func (h *HostReader) authorizedKeys(home string) []string {
	if home == "" {
		return nil
	}
	raw, err := h.c.ReadFile(strings.TrimSuffix(home, "/") + "/.ssh/authorized_keys")
	if err != nil {
		return nil
	}
	var keys []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keys = append(keys, line)
	}
	return keys
}

// System отдаёт имя машины и часовой пояс.
func (h *HostReader) System(ctx context.Context) (string, string, error) {
	if h.sysconf == nil {
		return "", "", fmt.Errorf("системные настройки недоступны")
	}
	s := h.sysconf.Read(ctx)
	name := s.StaticHostname
	if name == "" {
		name = s.Hostname
	}
	return name, s.Timezone, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
