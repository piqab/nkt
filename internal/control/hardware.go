package control

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/collect"
)

// Что за машина под интерфейсом — вопрос, на который панель обязана
// отвечать, а nkt до сих пор не отвечал: ни модели, ни процессора, ни
// температур, ни батареи. Всё читается только на просмотр; ничего из
// этого раздела ничего не меняет.
//
// Источники выбраны по принципу «работает без лишних пакетов»: /proc и
// /sys есть везде, lscpu входит в util-linux, а lspci/lsusb/sensors —
// необязательные, и их отсутствие не должно выглядеть как поломка.

// Hardware — сводка по железу хоста.
type Hardware struct {
	Machine   MachineInfo   `json:"machine"`
	CPU       CPUInfo       `json:"cpu"`
	Memory    MemoryInfo    `json:"memory"`
	Batteries []Battery     `json:"batteries,omitempty"`
	Sensors   []SensorValue `json:"sensors,omitempty"`
	PCI       []string      `json:"pci,omitempty"`
	USB       []string      `json:"usb,omitempty"`
	// Notes объясняет, чего не хватило: «нет lspci», «нет lm-sensors».
	// Не ошибка — на сервере этих пакетов обычно и не бывает.
	Notes []string `json:"notes,omitempty"`
}

// MachineInfo — то, что о себе сообщает сама машина через DMI. В
// виртуалках и контейнерах поля часто пустые: показывать нечего, и это
// нормальное состояние, а не сбой чтения.
type MachineInfo struct {
	Vendor      string `json:"vendor,omitempty"`
	Product     string `json:"product,omitempty"`
	Board       string `json:"board,omitempty"`
	BIOSVersion string `json:"bios_version,omitempty"`
	BIOSDate    string `json:"bios_date,omitempty"`
	// Virtualization — тип гипервизора по systemd-detect-virt: «kvm»,
	// «wsl», «none». Часто объясняет и пустой DMI, и отсутствие сенсоров.
	Virtualization string    `json:"virtualization,omitempty"`
	Uptime         int64     `json:"uptime_seconds"`
	LoadAvg        []float64 `json:"load_avg,omitempty"`
}

type CPUInfo struct {
	Model   string  `json:"model,omitempty"`
	Vendor  string  `json:"vendor,omitempty"`
	Arch    string  `json:"arch,omitempty"`
	CPUs    int     `json:"cpus"`
	Cores   int     `json:"cores_per_socket,omitempty"`
	Threads int     `json:"threads_per_core,omitempty"`
	Sockets int     `json:"sockets,omitempty"`
	MaxMHz  float64 `json:"max_mhz,omitempty"`
}

type MemoryInfo struct {
	TotalBytes     int64 `json:"total_bytes"`
	AvailableBytes int64 `json:"available_bytes"`
	SwapTotalBytes int64 `json:"swap_total_bytes"`
}

// Battery — состояние батареи из /sys/class/power_supply. На сервере их
// нет, на ноутбуке это первое, на что смотрят.
type Battery struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
	Status   string `json:"status"`
	Model    string `json:"model,omitempty"`
}

// SensorValue — одна температура из lm-sensors.
type SensorValue struct {
	Chip    string  `json:"chip"`
	Label   string  `json:"label"`
	Celsius float64 `json:"celsius"`
	Warn    float64 `json:"warn,omitempty"`
	Crit    float64 `json:"crit,omitempty"`
}

// HardwareManager собирает сводку по железу.
type HardwareManager struct {
	c collect.Collector
}

func NewHardwareManager(c collect.Collector) *HardwareManager { return &HardwareManager{c: c} }

// Collect собирает всё, что удалось прочитать. Ни одна недоступная
// команда не отменяет остальные: на минимальном образе нет lspci, в
// контейнере пуст DMI, на сервере нет батареи — и это нормальные
// состояния, а не отказы.
func (m *HardwareManager) Collect(ctx context.Context) Hardware {
	hw := Hardware{
		Machine: m.machine(ctx),
		CPU:     m.cpu(ctx),
		Memory:  m.memory(),
	}
	hw.Batteries = m.batteries()

	if res, err := m.c.Run(ctx, "sensors", "-j"); err == nil && res.ExitCode == 0 {
		hw.Sensors = parseSensors(res.Stdout)
	} else {
		hw.Notes = append(hw.Notes, "нет lm-sensors — температуры недоступны")
	}
	if res, err := m.c.Run(ctx, "lspci"); err == nil && res.ExitCode == 0 {
		hw.PCI = nonEmptyLines(res.Stdout)
	} else {
		hw.Notes = append(hw.Notes, "нет lspci — список PCI-устройств недоступен")
	}
	if res, err := m.c.Run(ctx, "lsusb"); err == nil && res.ExitCode == 0 {
		hw.USB = nonEmptyLines(res.Stdout)
	} else {
		hw.Notes = append(hw.Notes, "нет lsusb — список USB-устройств недоступен")
	}
	return hw
}

func (m *HardwareManager) machine(ctx context.Context) MachineInfo {
	read := func(name string) string {
		raw, err := m.c.ReadFile("/sys/class/dmi/id/" + name)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}
	info := MachineInfo{
		Vendor:      read("sys_vendor"),
		Product:     read("product_name"),
		Board:       read("board_name"),
		BIOSVersion: read("bios_version"),
		BIOSDate:    read("bios_date"),
	}
	if res, err := m.c.Run(ctx, "systemd-detect-virt"); err == nil {
		// Код возврата 1 означает «физическая машина» — это ответ, а не
		// ошибка, и слово из вывода («none») тоже осмысленно.
		info.Virtualization = strings.TrimSpace(res.Stdout)
	}
	if raw, err := m.c.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(raw)); len(f) > 0 {
			if secs, err := strconv.ParseFloat(f[0], 64); err == nil {
				info.Uptime = int64(secs)
			}
		}
	}
	if raw, err := m.c.ReadFile("/proc/loadavg"); err == nil {
		// Первые три поля — средняя нагрузка за 1, 5 и 15 минут; дальше
		// идут счётчики процессов, которые здесь не нужны.
		for i, f := range strings.Fields(string(raw)) {
			if i >= 3 {
				break
			}
			if v, err := strconv.ParseFloat(f, 64); err == nil {
				info.LoadAvg = append(info.LoadAvg, v)
			}
		}
	}
	return info
}

func (m *HardwareManager) cpu(ctx context.Context) CPUInfo {
	res, err := m.c.Run(ctx, "lscpu", "-J")
	if err != nil || res.ExitCode != 0 {
		return CPUInfo{}
	}
	return parseLscpu(res.Stdout)
}

// lscpuJSON — форма `lscpu -J`: плоский список пар «поле: значение», где
// поле включает двоеточие.
type lscpuJSON struct {
	LSCPU []struct {
		Field string `json:"field"`
		Data  string `json:"data"`
	} `json:"lscpu"`
}

func parseLscpu(out string) CPUInfo {
	var doc lscpuJSON
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &doc) != nil {
		return CPUInfo{}
	}
	info := CPUInfo{}
	for _, row := range doc.LSCPU {
		value := strings.TrimSpace(row.Data)
		switch strings.TrimSuffix(strings.TrimSpace(row.Field), ":") {
		case "Architecture":
			info.Arch = value
		case "Model name":
			info.Model = value
		case "Vendor ID":
			info.Vendor = value
		case "CPU(s)":
			info.CPUs, _ = strconv.Atoi(value)
		case "Core(s) per socket":
			info.Cores, _ = strconv.Atoi(value)
		case "Thread(s) per core":
			info.Threads, _ = strconv.Atoi(value)
		case "Socket(s)":
			info.Sockets, _ = strconv.Atoi(value)
		case "CPU max MHz":
			info.MaxMHz, _ = strconv.ParseFloat(value, 64)
		}
	}
	return info
}

func (m *HardwareManager) memory() MemoryInfo {
	raw, err := m.c.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryInfo{}
	}
	return parseMeminfo(string(raw))
}

// parseMeminfo читает /proc/meminfo. Значения там в килобайтах — в
// байтах их и отдаём, чтобы форматирование на экране было общим со всем
// остальным.
func parseMeminfo(out string) MemoryInfo {
	var info MemoryInfo
	for _, line := range strings.Split(out, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "MemTotal":
			info.TotalBytes = kb * 1024
		case "MemAvailable":
			info.AvailableBytes = kb * 1024
		case "SwapTotal":
			info.SwapTotalBytes = kb * 1024
		}
	}
	return info
}

func (m *HardwareManager) batteries() []Battery {
	entries, err := m.c.ListDir("/sys/class/power_supply")
	if err != nil {
		return nil
	}
	var out []Battery
	for _, e := range entries {
		name := e.Path[strings.LastIndex(e.Path, "/")+1:]
		// В этом каталоге лежат и адаптеры питания (AC), и батареи —
		// различаются по наличию файла capacity.
		raw, err := m.c.ReadFile(e.Path + "/capacity")
		if err != nil {
			continue
		}
		capacity, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			continue
		}
		b := Battery{Name: name, Capacity: capacity}
		if s, err := m.c.ReadFile(e.Path + "/status"); err == nil {
			b.Status = strings.TrimSpace(string(s))
		}
		if s, err := m.c.ReadFile(e.Path + "/model_name"); err == nil {
			b.Model = strings.TrimSpace(string(s))
		}
		out = append(out, b)
	}
	return out
}

// parseSensors разбирает `sensors -j`: чипы, внутри — датчики, внутри —
// поля вида tempN_input/tempN_max/tempN_crit. Имена полей у каждого чипа
// свои, поэтому разбор идёт по суффиксу, а не по фиксированному ключу.
func parseSensors(out string) []SensorValue {
	var doc map[string]map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &doc) != nil {
		return nil
	}
	var list []SensorValue
	for chip, sensors := range doc {
		for label, raw := range sensors {
			fields, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			v := SensorValue{Chip: chip, Label: label}
			found := false
			for key, val := range fields {
				num, ok := val.(float64)
				if !ok {
					continue
				}
				switch {
				case strings.HasSuffix(key, "_input"):
					v.Celsius, found = num, true
				case strings.HasSuffix(key, "_max"):
					v.Warn = num
				case strings.HasSuffix(key, "_crit"):
					v.Crit = num
				}
			}
			if found {
				list = append(list, v)
			}
		}
	}
	return list
}

func nonEmptyLines(out string) []string {
	var list []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			list = append(list, line)
		}
	}
	return list
}
