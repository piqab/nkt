package vmcreate

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// В /var/lib/libvirt/images лежит всё, с чем работает qemu на этом
// хосте: и диски существующих машин, и образы, положенные туда руками.
// Показывать это надо: место занято именно ими, а готовый образ, который
// принесли по scp, ничем не хуже скачанного.
//
// Но диск работающей машины — не образ: копия такого диска получается
// снятой на ходу, с несогласованной файловой системой. Поэтому каждый
// файл помечается тем, кому принадлежит, а не выдаётся за заготовку.

// HostImage — файл из каталога дисков libvirt.
type HostImage struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	// UsedBy — машина, которой принадлежит файл. Пусто — ничей.
	UsedBy string `json:"used_by,omitempty"`
	// Running — эта машина сейчас работает. Копировать её диск можно, но
	// предупредив: файловая система внутри копии будет снята на ходу.
	Running bool `json:"running,omitempty"`
}

// HostImages перечисляет файлы каталога дисков и сопоставляет их с
// машинами.
func HostImages(ctx context.Context, run Runner) []HostImage {
	if run == nil {
		return nil
	}
	// find, а не ls: его вывод разбирается однозначно, без разбора
	// колонок и локалей.
	res, err := run(ctx, "find", imagesRoot, "-maxdepth", "1", "-type", "f", "-printf", "%f\\t%s\\n")
	if err != nil || res.ExitCode != 0 {
		return nil
	}

	owners, running := domainDisks(ctx, run)
	var out []HostImage
	for _, line := range strings.Split(res.Stdout, "\n") {
		name, sizeStr, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || name == "" {
			continue
		}
		size, _ := strconv.ParseInt(strings.TrimSpace(sizeStr), 10, 64)
		full := filepath.Join(imagesRoot, name)
		img := HostImage{Name: name, Path: full, Size: size, UsedBy: owners[full]}
		img.Running = img.UsedBy != "" && running[img.UsedBy]
		out = append(out, img)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// domainDisks сопоставляет пути дисков машинам и отвечает, какие из
// машин работают.
func domainDisks(ctx context.Context, run Runner) (owners map[string]string, running map[string]bool) {
	owners, running = map[string]string{}, map[string]bool{}

	all, err := run(ctx, "virsh", "list", "--all", "--name")
	if err != nil || all.ExitCode != 0 {
		return owners, running
	}
	live, err := run(ctx, "virsh", "list", "--name")
	if err == nil && live.ExitCode == 0 {
		for _, name := range strings.Fields(live.Stdout) {
			running[name] = true
		}
	}

	for _, name := range strings.Fields(all.Stdout) {
		res, err := run(ctx, "virsh", "domblklist", name, "--details")
		if err != nil || res.ExitCode != 0 {
			continue
		}
		for _, path := range parseDomblklist(res.Stdout) {
			owners[path] = name
		}
	}
	return owners, running
}

// parseDomblklist достаёт пути файлов из вывода virsh domblklist
// --details.
//
// Формат: тип, устройство, цель, источник. Берутся только файловые
// источники: у сетевых дисков в последней колонке не путь, и превращать
// его в имя файла было бы выдумкой.
func parseDomblklist(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != "file" {
			continue
		}
		source := fields[len(fields)-1]
		if strings.HasPrefix(source, "/") {
			paths = append(paths, source)
		}
	}
	return paths
}
