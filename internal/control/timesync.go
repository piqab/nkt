package control

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"regexp"
	"strings"
	"time"
)

// Синхронизация времени. «NTP включён» в timedatectl — только флаг:
// за ним должна стоять служба (systemd-timesyncd, chrony или ntpsec), и у
// неё — сервер, до которого дотянуться можно. Раздел показывает, какая
// служба стоит, с кем она сверяется и с каким расхождением, даёт сменить
// серверы и подтолкнуть сверку прямо сейчас.

// TimeSyncPresets — серверы на выбор; своё имя тоже принимается.
var TimeSyncPresets = []string{
	"pool.ntp.org", "time.google.com", "time.cloudflare.com", "time.apple.com",
	"ntp.ubuntu.com", "ru.pool.ntp.org", "ntp1.vniiftri.ru", "ntp.msk-ix.ru",
}

// timeSyncServices — кандидаты в порядке предпочтения: юнит, пакет.
var timeSyncServices = []struct{ name, unit, pkg string }{
	{"systemd-timesyncd", "systemd-timesyncd.service", "systemd-timesyncd"},
	{"chrony", "chrony.service", "chrony"},
	{"ntpsec", "ntpsec.service", "ntpsec"},
	{"ntp", "ntp.service", "ntp"},
}

// TimeSync — состояние синхронизации времени.
type TimeSync struct {
	// Service — какая служба стоит: systemd-timesyncd, chrony, ntpsec, ntp
	// или пусто.
	Service   string `json:"service,omitempty"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	// NTP — флаг timedatectl; Synchronized — часы действительно сверены.
	NTP          bool     `json:"ntp"`
	Synchronized bool     `json:"synchronized"`
	Servers      []string `json:"servers"`
	// Server — с кем сверяется сейчас; Offset — расхождение по данным
	// службы; Stratum — слой источника.
	Server  string `json:"server,omitempty"`
	Offset  string `json:"offset,omitempty"`
	Stratum string `json:"stratum,omitempty"`
	// CanConfigure — серверы меняются через файл этой службы.
	CanConfigure bool `json:"can_configure"`
	// InstallPackage — что поставить, если службы нет.
	InstallPackage string   `json:"install_package,omitempty"`
	Presets        []string `json:"presets"`
	Note           string   `json:"note,omitempty"`
}

const (
	timesyncdDropIn = "/etc/systemd/timesyncd.conf.d/nkt.conf"
	chronyDropIn    = "/etc/chrony/conf.d/nkt.conf"
	chronyMain      = "/etc/chrony/chrony.conf"
)

var ntpServerRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252}[A-Za-z0-9])?$`)

// TimeSync собирает состояние.
func (m *SysConfigManager) TimeSync(ctx context.Context) TimeSync {
	out := TimeSync{Servers: []string{}, Presets: TimeSyncPresets, InstallPackage: "systemd-timesyncd"}
	if res, err := m.c.Run(ctx, "timedatectl", "show"); err == nil && res.ExitCode == 0 {
		kv := parseKeyValue(res.Stdout)
		out.NTP = kv["NTP"] == "yes"
		out.Synchronized = kv["NTPSynchronized"] == "yes"
	}
	for _, svc := range timeSyncServices {
		res, err := m.c.Run(ctx, "dpkg-query", "-W", "-f=${Status}", svc.pkg)
		if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "install ok installed") {
			continue
		}
		out.Service, out.Installed = svc.name, true
		if res, err := m.c.Run(ctx, "systemctl", "is-active", svc.unit); err == nil {
			out.Active = strings.TrimSpace(res.Stdout) == "active"
		}
		break
	}
	switch out.Service {
	case "systemd-timesyncd":
		out.CanConfigure = true
		out.Servers = m.timesyncdServers()
		if res, err := m.c.Run(ctx, "timedatectl", "show-timesync", "--all"); err == nil && res.ExitCode == 0 {
			st := parseTimesyncStatus(res.Stdout)
			out.Server, out.Offset, out.Stratum = st.server, st.offset, st.stratum
			if len(out.Servers) == 0 && st.systemServers != "" {
				out.Servers = strings.Fields(st.systemServers)
			}
		}
	case "chrony":
		out.CanConfigure = true
		out.Servers = m.chronyServers()
		if res, err := m.c.Run(ctx, "chronyc", "-n", "tracking"); err == nil && res.ExitCode == 0 {
			st := parseChronyTracking(res.Stdout)
			out.Server, out.Offset, out.Stratum = st.server, st.offset, st.stratum
		}
	case "":
		out.Note = msgs.Tc(ctx, "control.timeServiceMissingNote")
	default:
		out.Note = msgs.Tc(ctx, "control.managedOwnConfigOnlyState", out.Service)
	}
	return out
}

// timesyncdServers — NTP= из основного файла и drop-in'ов; последний
// встреченный побеждает, как и у самого systemd.
func (m *SysConfigManager) timesyncdServers() []string {
	files := []string{"/etc/systemd/timesyncd.conf"}
	if more, err := m.c.Glob("/etc/systemd/timesyncd.conf.d/*.conf"); err == nil {
		files = append(files, more...)
	}
	var servers []string
	for _, f := range files {
		raw, err := m.c.ReadFile(f)
		if err != nil {
			continue
		}
		if v, ok := parseKeyValue(string(raw))["NTP"]; ok && v != "" {
			servers = strings.Fields(v)
		}
	}
	if servers == nil {
		servers = []string{}
	}
	return servers
}

// chronyServers — строки server/pool из chrony.conf и conf.d.
func (m *SysConfigManager) chronyServers() []string {
	files := []string{chronyMain}
	if more, err := m.c.Glob("/etc/chrony/conf.d/*.conf"); err == nil {
		files = append(files, more...)
	}
	servers := []string{}
	for _, f := range files {
		raw, err := m.c.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && (fields[0] == "server" || fields[0] == "pool") {
				servers = append(servers, fields[1])
			}
		}
	}
	return servers
}

type syncStatus struct{ server, offset, stratum, systemServers string }

// parseTimesyncStatus разбирает timedatectl show-timesync --all.
// NTPMessage — одна строка вида «{ Leap=0, …, Stratum=2, …, Offset=+1.2ms, … }».
func parseTimesyncStatus(out string) syncStatus {
	kv := parseKeyValue(out)
	st := syncStatus{server: kv["ServerName"], systemServers: kv["SystemNTPServers"]}
	if st.server == "" {
		st.server = kv["ServerAddress"]
	}
	msg := strings.Trim(kv["NTPMessage"], "{} ")
	for _, part := range strings.Split(msg, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "Stratum":
			st.stratum = v
		case "Offset":
			st.offset = v
		}
	}
	return st
}

// parseChronyTracking разбирает chronyc -n tracking.
func parseChronyTracking(out string) syncStatus {
	var st syncStatus
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "Reference ID":
			// «C0A80001 (192.168.0.1)» — адрес в скобках.
			if i := strings.Index(v, "("); i >= 0 {
				st.server = strings.Trim(v[i:], "()")
			}
		case "Stratum":
			st.stratum = v
		case "System time":
			st.offset = v
		}
	}
	return st
}

// SetTimeSyncServers записывает серверы в drop-in службы и перезапускает
// её. Пустой список — drop-in убирается, остаются умолчания дистрибутива.
func (m *SysConfigManager) SetTimeSyncServers(ctx context.Context, servers []string) error {
	for _, s := range servers {
		if !ntpServerRe.MatchString(s) {
			return msgs.Errorf("control.invalidServerName", s)
		}
	}
	st := m.TimeSync(ctx)
	var file, content, unit string
	switch st.Service {
	case "systemd-timesyncd":
		file, unit = timesyncdDropIn, "systemd-timesyncd"
		content = "# Written by NetKnownsThat: servers from System settings.\n[Time]\nNTP=" + strings.Join(servers, " ") + "\n"
	case "chrony":
		file, unit = chronyDropIn, "chrony"
		var b strings.Builder
		b.WriteString("# Written by NetKnownsThat: servers from System settings.\n")
		for _, s := range servers {
			fmt.Fprintf(&b, "server %s iburst\n", s)
		}
		content = b.String()
	case "":
		return msgs.Errorf("control.timeServiceInstalled")
	default:
		return msgs.Errorf("control.configuredThroughOwnConfig", st.Service)
	}
	if len(servers) == 0 {
		if m.c.Exists(file) {
			if res, err := m.priv(ctx, "rm", "-f", "--", file); err != nil || res.ExitCode != 0 {
				return msgs.Errorf("control.removingFailed", file)
			}
		}
	} else if err := m.c.WriteFile(file, []byte(content), 0o644); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if res, err := m.priv(ctx, "systemctl", "restart", unit); err != nil {
		return err
	} else if res.ExitCode != 0 {
		return fmt.Errorf("systemctl restart %s: %s", unit, strings.TrimSpace(res.Output()))
	}
	return nil
}

// SyncNow подталкивает сверку: включает NTP, перезапускает службу (у
// timesyncd это и есть немедленный опрос), у chrony — makestep, чтобы
// большое расхождение исправилось скачком, а не за часы.
func (m *SysConfigManager) SyncNow(ctx context.Context) error {
	st := m.TimeSync(ctx)
	if !st.Installed {
		return msgs.Errorf("control.timeServiceInstalled")
	}
	if !st.NTP {
		if err := m.SetNTP(ctx, true); err != nil {
			return err
		}
	}
	unit := st.Service
	if res, err := m.priv(ctx, "systemctl", "restart", unit); err != nil {
		return err
	} else if res.ExitCode != 0 {
		return fmt.Errorf("systemctl restart %s: %s", unit, strings.TrimSpace(res.Output()))
	}
	if st.Service == "chrony" {
		// Служба только что поднялась — дать ей секунду на первый обмен.
		time.Sleep(2 * time.Second)
		if res, err := m.priv(ctx, "chronyc", "makestep"); err == nil && res.ExitCode != 0 {
			return fmt.Errorf("chronyc makestep: %s", strings.TrimSpace(res.Output()))
		}
	}
	// timesyncd отвечает на запрос не сразу: подождать, чтобы ответ уже
	// содержал сервер и расхождение, а не «ещё не сверялись».
	select {
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
	}
	return nil
}
