package control

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/piqab/nkt/internal/collect"
)

// Имя машины, часовой пояс, локаль и автоматические обновления
// безопасности — четыре настройки, которые ставят один раз при заведении
// сервера и потом ищут по документации, потому что делаются они каждая
// своей командой. Раздел собирает их в одно место.
//
// Всё читается машинно-читаемыми формами systemd (`hostnamectl
// --json=short`, `timedatectl show`), а не разбором человеческого вывода:
// тот меняется между версиями и переводится на язык системы.

// SystemSettings — текущее состояние.
type SystemSettings struct {
	Hostname        string `json:"hostname"`
	StaticHostname  string `json:"static_hostname,omitempty"`
	PrettyHostname  string `json:"pretty_hostname,omitempty"`
	OperatingSystem string `json:"operating_system,omitempty"`
	Kernel          string `json:"kernel,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
	NTP             bool   `json:"ntp"`
	NTPSynchronized bool   `json:"ntp_synchronized"`
	CanNTP          bool   `json:"can_ntp"`
	LocalTime       string `json:"local_time,omitempty"`
	Locale          string `json:"locale,omitempty"`
	// AutoUpgrades — состояние unattended-upgrades: установлен ли пакет и
	// включено ли автоматическое применение обновлений безопасности.
	AutoUpgradesInstalled bool     `json:"auto_upgrades_installed"`
	AutoUpgradesEnabled   bool     `json:"auto_upgrades_enabled"`
	Notes                 []string `json:"notes,omitempty"`
}

// SysConfigManager читает и меняет системные настройки.
type SysConfigManager struct {
	c collect.Collector
	// escape — выход из песочницы для команд, пишущих в /etc и /usr
	// (locale-gen, update-locale, перезапуск службы времени); nil — их нет.
	escape PrivilegedRunner
}

func NewSysConfigManager(c collect.Collector) *SysConfigManager { return &SysConfigManager{c: c} }

// hostnamectlJSON — подмножество полей `hostnamectl --json=short`.
type hostnamectlJSON struct {
	Hostname                  string  `json:"Hostname"`
	StaticHostname            *string `json:"StaticHostname"`
	PrettyHostname            *string `json:"PrettyHostname"`
	OperatingSystemPrettyName *string `json:"OperatingSystemPrettyName"`
	KernelName                string  `json:"KernelName"`
	KernelRelease             string  `json:"KernelRelease"`
}

// Read собирает текущее состояние; недоступность одной команды не
// отменяет остальные.
func (m *SysConfigManager) Read(ctx context.Context) SystemSettings {
	var out SystemSettings

	if res, err := m.c.Run(ctx, "hostnamectl", "--json=short"); err == nil && res.ExitCode == 0 {
		var doc hostnamectlJSON
		if json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &doc) == nil {
			out.Hostname = doc.Hostname
			out.StaticHostname = deref(doc.StaticHostname)
			out.PrettyHostname = deref(doc.PrettyHostname)
			out.OperatingSystem = deref(doc.OperatingSystemPrettyName)
			out.Kernel = strings.TrimSpace(doc.KernelName + " " + doc.KernelRelease)
		}
	} else {
		out.Notes = append(out.Notes, "hostnamectl недоступен — имя машины показано по /etc/hostname")
		if raw, err := m.c.ReadFile("/etc/hostname"); err == nil {
			out.Hostname = strings.TrimSpace(string(raw))
			out.StaticHostname = out.Hostname
		}
	}

	if res, err := m.c.Run(ctx, "timedatectl", "show"); err == nil && res.ExitCode == 0 {
		kv := parseKeyValue(res.Stdout)
		out.Timezone = kv["Timezone"]
		out.NTP = kv["NTP"] == "yes"
		out.NTPSynchronized = kv["NTPSynchronized"] == "yes"
		out.CanNTP = kv["CanNTP"] == "yes"
		out.LocalTime = kv["TimeUSec"]
	} else {
		out.Notes = append(out.Notes, "timedatectl недоступен — часовой пояс изменить нельзя")
	}

	// Локаль: /etc/default/locale в Debian/Ubuntu, /etc/locale.conf в
	// остальных. Читается файл, а не localectl: у того вывод только
	// человеческий.
	for _, path := range []string{"/etc/default/locale", "/etc/locale.conf"} {
		if raw, err := m.c.ReadFile(path); err == nil {
			if lang := parseKeyValue(strings.ReplaceAll(string(raw), "\"", ""))["LANG"]; lang != "" {
				out.Locale = lang
				break
			}
		}
	}

	out.AutoUpgradesInstalled, out.AutoUpgradesEnabled = m.autoUpgrades(ctx)
	return out
}

// autoUpgrades отвечает на два разных вопроса: стоит ли пакет и включено
// ли применение. Пакет часто стоит по умолчанию, а обновления при этом
// выключены — по одному только наличию пакета судить нельзя.
func (m *SysConfigManager) autoUpgrades(ctx context.Context) (installed, enabled bool) {
	res, err := m.c.Run(ctx, "dpkg-query", "-W", "-f=${Status}", "unattended-upgrades")
	installed = err == nil && res.ExitCode == 0 && strings.Contains(res.Stdout, "install ok installed")
	if !installed {
		return false, false
	}
	raw, err := m.c.ReadFile("/etc/apt/apt.conf.d/20auto-upgrades")
	if err != nil {
		return true, false
	}
	// Строка вида: APT::Periodic::Unattended-Upgrade "1";
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//") {
			continue
		}
		if strings.Contains(line, "Unattended-Upgrade") && strings.Contains(line, `"1"`) {
			return true, true
		}
	}
	return true, false
}

// parseKeyValue разбирает вывод вида KEY=value по строке на пару.
func parseKeyValue(out string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		kv[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return kv
}

// machineNameRe — RFC 1123: буквы, цифры и дефис, не начинается и не
// заканчивается дефисом. Имя уходит в команду и в /etc/hosts.
var machineNameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// timezoneRe — «Area/Location», как их печатает timedatectl
// list-timezones; плюс особый случай UTC.
var timezoneRe = regexp.MustCompile(`^[A-Za-z]+(/[A-Za-z0-9_+-]+){0,2}$`)

// SetHostname меняет имя машины.
func (m *SysConfigManager) SetHostname(ctx context.Context, name string) error {
	if !machineNameRe.MatchString(name) {
		return fmt.Errorf("недопустимое имя машины: %q", name)
	}
	res, err := m.c.Run(ctx, "hostnamectl", "set-hostname", name)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("hostnamectl: %s", strings.TrimSpace(res.Output()))
	}
	return nil
}

// SetTimezone меняет часовой пояс.
func (m *SysConfigManager) SetTimezone(ctx context.Context, zone string) error {
	if !timezoneRe.MatchString(zone) {
		return fmt.Errorf("недопустимый часовой пояс: %q", zone)
	}
	res, err := m.c.Run(ctx, "timedatectl", "set-timezone", zone)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("timedatectl: %s", strings.TrimSpace(res.Output()))
	}
	return nil
}

// SetNTP включает или выключает синхронизацию времени.
func (m *SysConfigManager) SetNTP(ctx context.Context, on bool) error {
	value := "false"
	if on {
		value = "true"
	}
	res, err := m.c.Run(ctx, "timedatectl", "set-ntp", value)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("timedatectl: %s", strings.TrimSpace(res.Output()))
	}
	return nil
}

// Timezones — список для выбора. 485 строк на этой машине: отдаются
// целиком, фильтрует их поле поиска в интерфейсе.
func (m *SysConfigManager) Timezones(ctx context.Context) ([]string, error) {
	res, err := m.c.Run(ctx, "timedatectl", "list-timezones")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("timedatectl: %s", strings.TrimSpace(res.Output()))
	}
	return nonEmptyLines(res.Stdout), nil
}
