package api

import (
	"context"
	"encoding/hex"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Когда терминал не открывается, оператор видит сообщение самого nsenter —
// «reassociate to namespace 'ns/mnt' failed: Operation not permitted». Оно
// точное, но ничего не говорит о том, что чинить: вызов setns отклоняется
// одинаково и когда у процесса нет CAP_SYS_ADMIN, и когда юнит на диске
// старше бинарника (в нём нет SystemCallFilter=setns), и когда юнит
// правильный, но после его замены забыли daemon-reload.
//
// Разобрать это можно прямо здесь, ничего не спрашивая у systemd: все
// нужные данные лежат в /proc и в файле юнита. Это важно — D-Bus в этом
// состоянии как раз и недоступен, иначе бы использовался systemd-run и
// никакого nsenter не понадобилось.

// capSysAdmin — номер CAP_SYS_ADMIN в маске capabilities ядра.
const capSysAdmin = 21

// unitDirectives — то, без чего выход из песочницы через nsenter не
// работает; см. deploy/netknownsthat.service, где каждая строка объяснена.
type unitDirectives struct {
	HasSetnsFilter bool
	HasSysAdminCap bool
	AllowsMountNS  bool
	Found          bool
	Path           string
	ModTime        time.Time
}

// SandboxProblem — одна найденная причина с готовым способом починки.
type SandboxProblem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// SandboxDiagnosis — ответ /terminal/diagnose.
type SandboxDiagnosis struct {
	// OK — ни одной причины не найдено: значит терминал упал не из-за
	// песочницы, и подсовывать оператору инструкцию по её починке было бы
	// хуже, чем промолчать.
	OK       bool             `json:"ok"`
	Sandbox  bool             `json:"sandboxed"`
	DBus     bool             `json:"dbus_reachable"`
	Unit     string           `json:"unit,omitempty"`
	Problems []SandboxProblem `json:"problems"`
	// Commands — команды для консоли или SSH, а не для веб-терминала:
	// именно он в этом состоянии и не работает.
	Commands []string `json:"commands"`
}

// parseCapEff разбирает строку CapEff из /proc/<pid>/status и отвечает,
// есть ли в маске CAP_SYS_ADMIN.
func parseCapEff(status string) (has bool, found bool) {
	for _, line := range strings.Split(status, "\n") {
		value, ok := strings.CutPrefix(line, "CapEff:")
		if !ok {
			continue
		}
		raw := strings.TrimSpace(value)
		// Маска печатается шестнадцатеричной строкой фиксированной длины.
		if _, err := hex.DecodeString(raw); err != nil && len(raw)%2 == 0 {
			return false, false
		}
		mask, err := strconv.ParseUint(raw, 16, 64)
		if err != nil {
			return false, false
		}
		return mask&(1<<capSysAdmin) != 0, true
	}
	return false, false
}

// unitNameFromCgroup достаёт имя юнита из /proc/self/cgroup — systemd не
// передаёт его переменной окружения, но путь контрольной группы всегда
// заканчивается именем юнита: "0::/system.slice/netknownsthat.service".
func unitNameFromCgroup(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) < 3 {
			continue
		}
		for _, segment := range strings.Split(parts[2], "/") {
			if strings.HasSuffix(segment, ".service") {
				return segment
			}
		}
	}
	return ""
}

// parseUnitDirectives читает содержимое юнита (основной файл плюс его
// drop-in'ы, склеенные вызывающей стороной) и отвечает, разрешает ли он
// setns в mount-пространство имён.
func parseUnitDirectives(content string) unitDirectives {
	d := unitDirectives{Found: content != ""}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "SystemCallFilter":
			// Строк может быть несколько, они объединяются; нас интересует
			// та, что добавляет сам setns — в @system-service его нет.
			for _, call := range strings.Fields(value) {
				if call == "setns" {
					d.HasSetnsFilter = true
				}
			}
		case "AmbientCapabilities", "CapabilityBoundingSet":
			for _, cap := range strings.Fields(value) {
				if strings.EqualFold(cap, "CAP_SYS_ADMIN") {
					d.HasSysAdminCap = true
				}
			}
		case "RestrictNamespaces":
			switch {
			case strings.EqualFold(value, "no") || strings.EqualFold(value, "false"):
				d.AllowsMountNS = true
			case strings.HasPrefix(value, "~"):
				// Список запрещённых: mnt разрешён, если его там нет.
				d.AllowsMountNS = !strings.Contains(value, "mnt")
			default:
				for _, ns := range strings.Fields(value) {
					if ns == "mnt" {
						d.AllowsMountNS = true
					}
				}
			}
		}
	}
	return d
}

// readUnit собирает содержимое юнита вместе с drop-in'ами. Drop-in может
// как добавить нужную директиву, так и снять её — поэтому смотреть только
// основной файл нельзя.
func readUnit(unit string) unitDirectives {
	var content strings.Builder
	var newest time.Time
	var mainPath string

	for _, dir := range []string{"/etc/systemd/system", "/run/systemd/system", "/lib/systemd/system", "/usr/lib/systemd/system"} {
		path := filepath.Join(dir, unit)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if mainPath == "" {
			mainPath = path
		}
		content.Write(raw)
		content.WriteString("\n")
		if st, err := os.Stat(path); err == nil && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
		// Первый найденный основной файл побеждает: systemd ищет в том же
		// порядке приоритета.
		break
	}

	for _, dir := range []string{"/etc/systemd/system", "/run/systemd/system"} {
		matches, _ := filepath.Glob(filepath.Join(dir, unit+".d", "*.conf"))
		for _, path := range matches {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			content.Write(raw)
			content.WriteString("\n")
			if st, err := os.Stat(path); err == nil && st.ModTime().After(newest) {
				newest = st.ModTime()
			}
		}
	}

	d := parseUnitDirectives(content.String())
	d.Path = mainPath
	d.ModTime = newest
	return d
}

// processStarted — время запуска этого процесса. Каталог /proc/self имеет
// mtime, равный моменту старта, что избавляет от разбора поля starttime в
// тактах и пересчёта относительно времени загрузки.
func processStarted() (time.Time, bool) {
	st, err := os.Stat("/proc/self")
	if err != nil {
		return time.Time{}, false
	}
	return st.ModTime(), true
}

// diagnoseSandbox собирает причины, по которым выход из песочницы через
// nsenter не работает.
func diagnoseSandbox(ctx context.Context) SandboxDiagnosis {
	diag := SandboxDiagnosis{
		Sandbox: os.Getenv("INVOCATION_ID") != "",
		DBus:    systemdRunReachable(),
		// Пустые срезы, а не nil: nil уезжает в JSON как null, и
		// «problems.length» в браузере роняет отрисовку целиком — на
		// здоровом хосте, где ровно оба списка и пусты.
		Problems: []SandboxProblem{},
		Commands: []string{},
	}
	if !diag.Sandbox {
		// Вне юнита никакой песочницы нет — падение терминала объясняется
		// чем-то другим, и гадать здесь нечем.
		diag.OK = true
		return diag
	}
	if diag.DBus {
		// Основной путь доступен: до nsenter дело не доходит вовсе.
		diag.OK = true
		return diag
	}

	add := func(code, detail string) {
		diag.Problems = append(diag.Problems, SandboxProblem{Code: code, Detail: detail})
	}

	if status, err := os.ReadFile("/proc/self/status"); err == nil {
		if has, found := parseCapEff(string(status)); found && !has {
			add("no_cap_sys_admin", msgs.Tc(ctx, "api.diagNoCapSysAdmin"))
		}
	}

	cgroup, _ := os.ReadFile("/proc/self/cgroup")
	unit := unitNameFromCgroup(string(cgroup))
	if unit == "" {
		unit = "netknownsthat.service"
	}
	diag.Unit = unit

	d := readUnit(unit)
	switch {
	case !d.Found:
		add("unit_not_found", msgs.Tc(ctx, "api.diagUnitNotFound", unit))
	default:
		if !d.HasSetnsFilter {
			add("unit_missing_setns", msgs.Tc(ctx, "api.diagUnitMissingSetns", d.Path))
		}
		if !d.HasSysAdminCap {
			add("unit_missing_cap", msgs.Tc(ctx, "api.diagUnitMissingCap", d.Path))
		}
		if !d.AllowsMountNS {
			add("unit_restricts_namespaces", msgs.Tc(ctx, "api.diagUnitRestrictsNS", d.Path))
		}
		if started, ok := processStarted(); ok && !d.ModTime.IsZero() && d.ModTime.After(started) {
			add("unit_newer_than_process", msgs.Tc(ctx, "api.diagUnitNewer",
				d.Path, d.ModTime.Format(time.RFC3339), started.Format(time.RFC3339)))
		}
	}

	diag.OK = len(diag.Problems) == 0
	diag.Commands = sandboxFixCommands(diag)
	return diag
}

// sandboxFixCommands — что выполнить на хосте. Порядок сознательный:
// сначала dbus (после него основной путь через systemd-run заработает и
// nsenter вообще не понадобится), и только потом обновление юнита.
func sandboxFixCommands(diag SandboxDiagnosis) []string {
	cmds := []string{
		"sudo apt-get update && sudo apt-get install -y dbus",
		"sudo systemctl start dbus",
	}
	needsUnit := false
	for _, p := range diag.Problems {
		if strings.HasPrefix(p.Code, "unit_") {
			needsUnit = true
		}
	}
	if needsUnit {
		cmds = append(cmds,
			"sudo curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/netknownsthat.service"+
				" -o /etc/systemd/system/netknownsthat.service",
			"sudo systemctl daemon-reload")
	}
	return append(cmds, "sudo systemctl restart netknownsthat")
}

// handleTerminalDiagnose отвечает на вопрос «почему не открылся терминал».
func (s *Server) handleTerminalDiagnose(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, diagnoseSandbox(r.Context()))
}
