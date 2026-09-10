package control

import (
	"context"
	"encoding/json"
	"fmt"
	gopath "path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
)

// «Кончилось место» — самая частая и самая внезапная проблема на любой
// машине, и до сих пор nkt о дисках не знал вообще ничего: ни занятого
// места, ни устройств, ни подкачки. Раздел закрывает именно это, тремя
// разными взглядами на одно и то же железо:
//
//   - файловые системы (df) — сколько занято там, куда реально пишут;
//   - блочные устройства (lsblk) — что вообще есть и как разбито;
//   - подкачка (swapon) — потому что забытый своп объясняет и «нет места»,
//     и внезапную медлительность.
//
// Плюс отдельный инструмент «что занимает место»: du по одному каталогу,
// по запросу — он тяжёлый и запускать его фоном на каждый показ страницы
// нельзя.

// Filesystem — строка df.
type Filesystem struct {
	Device     string `json:"device"`
	Type       string `json:"type"`
	MountPoint string `json:"mount_point"`
	Size       int64  `json:"size"`
	Used       int64  `json:"used"`
	Available  int64  `json:"available"`
	// UsePercent считается здесь, а не берётся из df: у df это проценты от
	// размера с округлением вверх, и на больших дисках «1%» и «0%» на
	// глаз отличаются сильнее, чем реальные значения.
	UsePercent float64 `json:"use_percent"`
	// Pseudo — tmpfs, overlay, devtmpfs и прочее, что живёт в памяти или
	// поверх другого: место там не кончается «навсегда», и мешать их с
	// настоящими разделами в одном списке нельзя.
	Pseudo bool `json:"pseudo"`
}

// BlockDevice — устройство или раздел из lsblk.
type BlockDevice struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	Type        string        `json:"type"`
	Size        int64         `json:"size"`
	Model       string        `json:"model,omitempty"`
	Serial      string        `json:"serial,omitempty"`
	Transport   string        `json:"transport,omitempty"`
	FSType      string        `json:"fstype,omitempty"`
	Label       string        `json:"label,omitempty"`
	MountPoints []string      `json:"mount_points,omitempty"`
	Rotational  bool          `json:"rotational"`
	Children    []BlockDevice `json:"children,omitempty"`
}

// Swap — одна область подкачки.
type Swap struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	Used int64  `json:"used"`
}

// DirEntry — один каталог в разборе «что занимает место».
type DirEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// DiskManager собирает картину дисков хоста.
type DiskManager struct {
	c collect.Collector
}

func NewDiskManager(c collect.Collector) *DiskManager { return &DiskManager{c: c} }

// pseudoFilesystems — типы, которые не являются местом на диске.
var pseudoFilesystems = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true,
	"ramfs": true, "rootfs": true, "efivarfs": true, "9p": true,
	"fuse.snapfuse": true, "fuse.portal": true, "fuse.gvfsd-fuse": true,
}

// Overview — всё сразу: страница показывает три списка вместе, и три
// отдельных запроса ради этого гонять незачем.
type Overview struct {
	Filesystems []Filesystem  `json:"filesystems"`
	Devices     []BlockDevice `json:"devices"`
	Swap        []Swap        `json:"swap"`
	Errors      []string      `json:"errors,omitempty"`
}

// Overview собирает данные, не падая целиком из-за одной недоступной
// команды: на урезанном образе может не быть lsblk, но df там есть, и
// показать хотя бы место — уже польза.
func (m *DiskManager) Overview(ctx context.Context) Overview {
	var out Overview

	if res, err := m.c.Run(ctx, "df", "-P", "-B1", "-T"); err != nil || res.ExitCode != 0 {
		out.Errors = append(out.Errors, "df: "+commandError(res, err))
	} else {
		out.Filesystems = parseDF(res.Stdout)
	}

	if res, err := m.c.Run(ctx, "lsblk", "-J", "-b", "-o",
		"NAME,PATH,TYPE,SIZE,MODEL,SERIAL,TRAN,FSTYPE,LABEL,MOUNTPOINTS,ROTA"); err != nil || res.ExitCode != 0 {
		out.Errors = append(out.Errors, "lsblk: "+commandError(res, err))
	} else {
		devices, err := parseLsblk(res.Stdout)
		if err != nil {
			out.Errors = append(out.Errors, "lsblk: "+err.Error())
		}
		out.Devices = devices
	}

	// Подкачки может не быть вовсе — это не ошибка, а обычное состояние.
	if res, err := m.c.Run(ctx, "swapon", "--show=NAME,TYPE,SIZE,USED", "--bytes", "--noheadings"); err == nil && res.ExitCode == 0 {
		out.Swap = parseSwapon(res.Stdout)
	}
	return out
}

func commandError(res collect.CommandResult, err error) string {
	if err != nil {
		return err.Error()
	}
	if out := strings.TrimSpace(res.Output()); out != "" {
		return out
	}
	return fmt.Sprintf("код возврата %d", res.ExitCode)
}

// parseDF разбирает вывод `df -P -B1 -T`. -P гарантирует одну строку на
// файловую систему (без -P длинное имя устройства переносится, и разбор
// по полям разъезжается), -B1 — байты вместо блоков, -T — тип.
func parseDF(out string) []Filesystem {
	var list []Filesystem
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// Заголовок «Filesystem Type 1-blocks Used Available Capacity
		// Mounted on» и пустые строки.
		if i == 0 || len(fields) < 7 {
			continue
		}
		size, err1 := strconv.ParseInt(fields[2], 10, 64)
		used, err2 := strconv.ParseInt(fields[3], 10, 64)
		avail, err3 := strconv.ParseInt(fields[4], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		fs := Filesystem{
			Device: fields[0], Type: fields[1],
			Size: size, Used: used, Available: avail,
			// Точка монтирования может содержать пробелы — она идёт
			// последней, поэтому склеивается обратно, а не берётся полем.
			MountPoint: strings.Join(fields[6:], " "),
			Pseudo:     pseudoFilesystems[fields[1]],
		}
		if size > 0 {
			// Считается от занятого плюс доступного, а не от size: на ext4
			// часть места зарезервирована для root, и «занято 100%» при
			// свободных пяти процентах резерва — верно по сути.
			if denom := used + avail; denom > 0 {
				fs.UsePercent = float64(used) / float64(denom) * 100
			}
		}
		list = append(list, fs)
	}
	return list
}

// lsblkJSON повторяет форму вывода `lsblk -J`.
type lsblkJSON struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

type lsblkDevice struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	Type        string        `json:"type"`
	Size        int64         `json:"size"`
	Model       *string       `json:"model"`
	Serial      *string       `json:"serial"`
	Tran        *string       `json:"tran"`
	FSType      *string       `json:"fstype"`
	Label       *string       `json:"label"`
	MountPoints []*string     `json:"mountpoints"`
	Rota        bool          `json:"rota"`
	Children    []lsblkDevice `json:"children"`
}

// parseLsblk разбирает JSON lsblk. Половина полей у него приходит как
// null (у диска без метки, у виртуального без транспорта), поэтому все
// они читаются указателями — иначе строгий разбор ломается на первом же
// пустом значении.
func parseLsblk(out string) ([]BlockDevice, error) {
	var doc lsblkJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &doc); err != nil {
		return nil, fmt.Errorf("не удалось разобрать вывод lsblk: %w", err)
	}
	return convertLsblk(doc.BlockDevices), nil
}

func convertLsblk(in []lsblkDevice) []BlockDevice {
	out := make([]BlockDevice, 0, len(in))
	for _, d := range in {
		dev := BlockDevice{
			Name: d.Name, Path: d.Path, Type: d.Type, Size: d.Size,
			Model: deref(d.Model), Serial: deref(d.Serial), Transport: deref(d.Tran),
			FSType: deref(d.FSType), Label: deref(d.Label), Rotational: d.Rota,
			Children: convertLsblk(d.Children),
		}
		for _, mp := range d.MountPoints {
			if s := deref(mp); s != "" {
				dev.MountPoints = append(dev.MountPoints, s)
			}
		}
		out = append(out, dev)
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// parseSwapon разбирает `swapon --show ... --noheadings --bytes`.
func parseSwapon(out string) []Swap {
	var list []Swap
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		size, err1 := strconv.ParseInt(fields[2], 10, 64)
		used, err2 := strconv.ParseInt(fields[3], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		list = append(list, Swap{Name: fields[0], Type: fields[1], Size: size, Used: used})
	}
	return list
}

// duTimeout ограничивает обход каталога. du по большому дереву — минуты
// работы и заметная нагрузка на диск, поэтому у инструмента свой потолок,
// а не общий таймаут команд.
const duTimeout = 60 * time.Second

// DirUsage отвечает на вопрос «что съело место»: размеры подкаталогов
// одного уровня, по убыванию.
//
// -x не выпускает обход за пределы файловой системы: без него du из / уйдёт
// в /proc, /sys и примонтированные сетевые шары и будет считать их вечно.
// -d1 — только один уровень: дальше человек уточняет сам, кликая вглубь,
// и каждый шаг остаётся быстрым.
func (m *DiskManager) DirUsage(ctx context.Context, path string) ([]DirEntry, error) {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || gopath.Clean(path) != path {
		return nil, fmt.Errorf("некорректный путь: %q", path)
	}
	ctx, cancel := context.WithTimeout(ctx, duTimeout)
	defer cancel()

	res, err := m.c.Run(ctx, "du", "-x", "-b", "-d1", path)
	if err != nil {
		return nil, err
	}
	// du возвращает ненулевой код, если хотя бы один подкаталог не
	// прочитался, но остальное при этом посчитано — результат отбрасывать
	// из-за этого не за что.
	entries := parseDU(res.Stdout, path)
	if len(entries) == 0 && res.ExitCode != 0 {
		return nil, fmt.Errorf("du: %s", strings.TrimSpace(res.Output()))
	}
	return entries, nil
}

// parseDU разбирает `du -b -d1`: размер, табуляция, путь. Сам каталог
// (последняя строка с тем же путём) из списка убирается — он не «занимает
// место внутри себя», а равен сумме остальных.
func parseDU(out, root string) []DirEntry {
	var list []DirEntry
	for _, line := range strings.Split(out, "\n") {
		size, path, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
		if err != nil {
			continue
		}
		path = strings.TrimSpace(path)
		if path == "" || path == root || path == strings.TrimSuffix(root, "/") {
			continue
		}
		list = append(list, DirEntry{Path: path, Size: n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Size > list[j].Size })
	return list
}
