package vmcreate

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
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
	// Пустой срез, а не nil: nil становится null в JSON, и страница,
	// считающая длину списка, падает целиком.
	if run == nil {
		return []HostImage{}
	}
	// find, а не ls: его вывод разбирается однозначно, без разбора
	// колонок и локалей.
	res, err := run(ctx, "find", imagesRoot, "-maxdepth", "1", "-type", "f", "-printf", "%f\\t%s\\n")
	if err != nil || res.ExitCode != 0 {
		return []HostImage{}
	}

	owners, running := domainDisks(ctx, run)
	out := []HostImage{}
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

// ImagesRoot отдаёт каталог дисков libvirt — туда кладут свои образы и
// оттуда их удаляют.
func ImagesRoot() string { return imagesRoot }

// PutHostImage переносит готовый файл в каталог дисков libvirt.
//
// Именно переносит, а не копирует: файл уже лежит во временном месте
// каталога данных nkt, и вторая копия образа на сотни мегабайт никому
// не нужна. Перенос идёт вне песочницы — писать в /var/lib/libvirt
// изнутри юнита нельзя.
func PutHostImage(ctx context.Context, run Runner, tmpPath, name string) (string, error) {
	if run == nil {
		return "", errNoRunner
	}
	if !validHostImageName(name) {
		return "", fmt.Errorf("недопустимое имя файла: %q", name)
	}
	target := filepath.Join(imagesRoot, name)
	if res, err := run(ctx, "test", "-e", target); err == nil && res.ExitCode == 0 {
		return "", fmt.Errorf("файл %s уже есть — удалите старый или выберите другое имя", name)
	}
	// install, а не mv: он же выставит права, с которыми qemu сможет
	// прочитать файл.
	res, err := run(ctx, "install", "-m", "0644", tmpPath, target)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("перенос образа: %s", strings.TrimSpace(res.Output()))
	}
	if rm, err := run(ctx, "rm", "-f", tmpPath); err == nil && rm.ExitCode != 0 {
		// Временный файл не убрался — это не повод считать перенос
		// неудачным, но место он займёт, и молчать не стоит.
		return target, nil
	}
	return target, nil
}

// DeleteHostImage убирает файл из каталога дисков libvirt.
//
// Занятость машиной проверяет вызывающий: удаление диска работающей
// машины — не то, что стоит делать молча, но и запрещать его
// окончательно нельзя (машину могли уже удалить, а диск остаться).
func DeleteHostImage(ctx context.Context, run Runner, name string) error {
	if run == nil {
		return errNoRunner
	}
	if !validHostImageName(name) {
		return fmt.Errorf("недопустимое имя файла: %q", name)
	}
	res, err := run(ctx, "rm", "-f", filepath.Join(imagesRoot, name))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("удаление %s: %s", name, strings.TrimSpace(res.Output()))
	}
	return nil
}

// validHostImageName — имя приходит от оператора и становится путём в
// каталоге дисков: ни косых черт, ни «..», ни пустоты.
func validHostImageName(name string) bool {
	return hostImageNameRe.MatchString(name) && !strings.Contains(name, "..")
}

var (
	hostImageNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	errNoRunner     = fmt.Errorf("работа с файлами хоста недоступна в этом режиме")
)
