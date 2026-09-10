package control

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/collect"
)

// На сервере сеть задаётся файлом (netplan, systemd-networkd) и правится
// в «Конфигурациях». На рабочей машине ею почти всегда управляет
// NetworkManager, и там файл править бессмысленно: он его перезапишет.
// Этот раздел — про второй случай: соединения, устройства, Wi-Fi.
//
// Всё читается через `nmcli -t` — терминальный машинный формат с
// разделителем «:», который не переводится на язык системы, в отличие от
// обычного вывода nmcli.

// NMConnection — сохранённое соединение.
type NMConnection struct {
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	Type   string `json:"type"`
	Device string `json:"device,omitempty"`
	Active bool   `json:"active"`
}

// NMDevice — сетевое устройство и его состояние.
type NMDevice struct {
	Device     string `json:"device"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Connection string `json:"connection,omitempty"`
}

// WiFiNetwork — точка доступа из результатов сканирования.
type WiFiNetwork struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security,omitempty"`
	InUse    bool   `json:"in_use"`
}

// NetworkState — всё, что показывает раздел.
type NetworkState struct {
	Available   bool           `json:"available"`
	Connections []NMConnection `json:"connections,omitempty"`
	Devices     []NMDevice     `json:"devices,omitempty"`
	WiFi        []WiFiNetwork  `json:"wifi,omitempty"`
	Note        string         `json:"note,omitempty"`
}

// NetworkManagerControl читает и меняет состояние NetworkManager.
type NetworkManagerControl struct {
	c collect.Collector
}

func NewNetworkManagerControl(c collect.Collector) *NetworkManagerControl {
	return &NetworkManagerControl{c: c}
}

// State собирает картину. Отсутствие nmcli — обычное состояние сервера, а
// не ошибка: раздел просто говорит, что сетью управляет не NM.
func (m *NetworkManagerControl) State(ctx context.Context) NetworkState {
	if !collect.Which(ctx, m.c, "nmcli") {
		return NetworkState{Note: "NetworkManager не установлен — сетью управляет что-то другое (netplan, systemd-networkd); их конфигурация правится в разделе «Конфигурации»"}
	}
	state := NetworkState{Available: true}

	if res, err := m.c.Run(ctx, "nmcli", "-t", "-f", "NAME,UUID,TYPE,DEVICE,ACTIVE", "connection", "show"); err == nil && res.ExitCode == 0 {
		state.Connections = parseNMConnections(res.Stdout)
	}
	if res, err := m.c.Run(ctx, "nmcli", "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status"); err == nil && res.ExitCode == 0 {
		state.Devices = parseNMDevices(res.Stdout)
	}
	// Список сетей запрашивается без --rescan: свежее сканирование
	// занимает секунды и рвёт текущее соединение на слабых адаптерах.
	// Показывается то, что NM уже знает.
	if res, err := m.c.Run(ctx, "nmcli", "-t", "-f", "IN-USE,SSID,SIGNAL,SECURITY", "device", "wifi", "list"); err == nil && res.ExitCode == 0 {
		state.WiFi = parseNMWiFi(res.Stdout)
	}
	return state
}

// splitNM разбирает строку `nmcli -t`: поля разделены «:», а двоеточие
// внутри значения экранируется обратным слешем (имя соединения вполне
// может его содержать).
func splitNM(line string) []string {
	var fields []string
	var cur strings.Builder
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	fields = append(fields, cur.String())
	return fields
}

func parseNMConnections(out string) []NMConnection {
	var list []NMConnection
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := splitNM(line)
		if len(f) < 5 {
			continue
		}
		list = append(list, NMConnection{
			Name: f[0], UUID: f[1], Type: f[2],
			// Неактивное соединение показывает «--» вместо устройства.
			Device: strings.TrimPrefix(f[3], "--"),
			Active: f[4] == "yes",
		})
	}
	return list
}

func parseNMDevices(out string) []NMDevice {
	var list []NMDevice
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := splitNM(line)
		if len(f) < 4 {
			continue
		}
		list = append(list, NMDevice{
			Device: f[0], Type: f[1], State: f[2],
			Connection: strings.TrimPrefix(f[3], "--"),
		})
	}
	return list
}

func parseNMWiFi(out string) []WiFiNetwork {
	var list []WiFiNetwork
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := splitNM(line)
		if len(f) < 4 {
			continue
		}
		signal, _ := strconv.Atoi(f[2])
		ssid := f[1]
		if ssid == "" {
			// Скрытая сеть: подключиться к ней из списка всё равно нельзя,
			// а строка без имени только мешает.
			continue
		}
		list = append(list, WiFiNetwork{
			InUse: strings.TrimSpace(f[0]) == "*", SSID: ssid,
			Signal: signal, Security: f[3],
		})
	}
	return list
}

// nmUUIDRe — идентификатор соединения уходит в команду, поэтому
// принимается только в том виде, в каком его печатает сам nmcli.
var nmUUIDRe = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

// Connection поднимает или гасит соединение по его UUID.
//
// Именно по UUID, а не по имени: имена не уникальны, и «поднять Wi-Fi»
// при двух одинаково названных профилях подняло бы не тот.
func (m *NetworkManagerControl) Connection(ctx context.Context, uuid string, up bool) error {
	if !nmUUIDRe.MatchString(uuid) {
		return fmt.Errorf("некорректный идентификатор соединения: %q", uuid)
	}
	action := "down"
	if up {
		action = "up"
	}
	res, err := m.c.Run(ctx, "nmcli", "connection", action, uuid)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("nmcli: %s", strings.TrimSpace(res.Output()))
	}
	return nil
}

// ConnectWiFi подключается к сети по имени и паролю.
func (m *NetworkManagerControl) ConnectWiFi(ctx context.Context, ssid, password string) error {
	if ssid == "" || strings.ContainsAny(ssid, "\n\r") {
		return fmt.Errorf("некорректное имя сети")
	}
	argv := []string{"device", "wifi", "connect", ssid}
	if password != "" {
		argv = append(argv, "password", password)
	}
	res, err := m.c.Run(ctx, "nmcli", argv...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		// Пароль в выводе nmcli не печатает, но обрезка на всякий случай:
		// сообщение уходит в интерфейс и в журнал аудита.
		return fmt.Errorf("nmcli: %s", strings.TrimSpace(res.Output()))
	}
	return nil
}
