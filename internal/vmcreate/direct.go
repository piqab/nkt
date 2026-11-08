// Прямое подключение машины к устройству хоста — macvtap поверх
// интерфейса, который у хоста уже есть.
package vmcreate

import (
	"context"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Причины, по которым устройство не годится быть нижним для macvtap.
// Пустая строка — «годится». Коды, а не готовые сообщения: той же
// проверкой пользуется хаб (проверки кластера передаются ему кодом и он
// переводит их своими словами), и у перевода один источник.
const (
	DeviceOK       = ""
	DeviceMissing  = "missing"
	DeviceDown     = "down"
	DeviceLoopback = "loopback"
	DeviceMacvlan  = "macvlan"
)

// DirectVerdict спрашивает хост, почему устройство годится или не
// годится. Один источник правды: им пользуются и создание машины, и
// проверки кластера, — иначе «проверки зелёные, создание упало» стало бы
// обычным делом.
//
// Состояние берётся у iproute2, а не из снимка сканера: снимок мог быть
// снят час назад, а кабель вытащить минут назад, и отказ по устаревшему
// снимку был бы неправдой в обе стороны.
func DirectVerdict(ctx context.Context, run Runner, dev string) string {
	if run == nil || dev == "" {
		return DeviceOK
	}
	res, err := run(ctx, "ip", "-d", "-o", "link", "show", "dev", dev)
	if err != nil {
		// Спросить не удалось — не выдумываем отказ там, где его может не
		// быть: создание машины проверит ещё раз и на своём месте.
		return DeviceOK
	}
	line := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || line == "" {
		return DeviceMissing
	}
	return directVerdictLine(line)
}

// DirectDevices отвечает пригодность всех устройств хоста сразу: одной
// командой, — проверки кластера спрашивают не одно имя, а всё, что
// оператор выбрал (и то, что мог бы выбрать).
//
// nil — «спросить не удалось»: хаб переводит это в предупреждение, а не в
// отказ «устройства нет», которого агент мог и не видеть.
func DirectDevices(ctx context.Context, run Runner) map[string]string {
	if run == nil {
		return nil
	}
	res, err := run(ctx, "ip", "-d", "-o", "link", "show")
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := directNameOf(line)
		if name == "" {
			continue
		}
		out[name] = directVerdictLine(line)
	}
	return out
}

// directNameOf достаёт имя устройства из начала строки «ip -o link»:
// «2: eth0: <…>» или «7: macvtap0@eth0: <…>» — там, где есть @, имя
// соседа не относится к делу.
func directNameOf(line string) string {
	parts := strings.SplitN(line, ": ", 3)
	if len(parts) < 2 {
		return ""
	}
	name, _, _ := strings.Cut(parts[1], "@")
	return name
}

// directVerdictLine разбирает одну строку вывода iproute2.
func directVerdictLine(line string) string {
	if directHasFlag(line, "LOOPBACK") {
		return DeviceLoopback
	}
	// UP — включено административно, LOWER_UP — на линке есть carrier.
	// Без второго macvtap не поднимется, и virsh упадёт на старте.
	if !directHasFlag(line, "UP") || !directHasFlag(line, "LOWER_UP") {
		return DeviceDown
	}
	if directIsMacvlan(line) {
		return DeviceMacvlan
	}
	return DeviceOK
}

// CheckDirectDevice превращает вердикт хоста в отказ с причиной.
//
// Проверяется не то, что имя существует: об этом virsh сказал бы и сам.
// Проверяется то, о чём он молчит: устройство есть, но кабель вытащен;
// это loopback; или это сам macvtap, второго поверх которого ядро не
// делает. Отказ называет устройство — оператор выбирает его из списка
// интерфейсов хоста, и «устройство не подходит» без имени ничего не
// объясняет.
func CheckDirectDevice(ctx context.Context, run Runner, dev string) error {
	switch DirectVerdict(ctx, run, dev) {
	case DeviceMissing:
		return msgs.Errorf("vmcreate.directDeviceMissing", dev)
	case DeviceDown:
		return msgs.Errorf("vmcreate.directDeviceDown", dev)
	case DeviceLoopback:
		return msgs.Errorf("vmcreate.directDeviceLoopback", dev)
	case DeviceMacvlan:
		return msgs.Errorf("vmcreate.directDeviceIsMacvtap", dev)
	}
	return nil
}

// directHasFlag видит нужный флаг в строке «ip -o link»: флаги стоят
// между '<' и '>' через запятую.
func directHasFlag(line, want string) bool {
	start := strings.IndexByte(line, '<')
	if start < 0 {
		return false
	}
	rest := line[start+1:]
	end := strings.IndexByte(rest, '>')
	if end < 0 {
		return false
	}
	for _, f := range strings.Split(rest[:end], ",") {
		if f == want {
			return true
		}
	}
	return false
}

// directIsMacvlan видит, что устройство само сделано из macvlan:
// iproute2 печатает его тип после флагов, а имя устройства в этой части
// строки стоять не может — поэтому «похоже на macvtap» здесь случается
// только там, где оно и есть macvtap.
func directIsMacvlan(line string) bool {
	if i := strings.IndexByte(line, '>'); i >= 0 {
		line = line[i:]
	}
	for _, f := range strings.Fields(strings.ReplaceAll(line, `\    `, " ")) {
		if f == "macvlan" || f == "macvtap" {
			return true
		}
	}
	return false
}
