// Package files — проводник по каталогам хоста: папки, загрузка, архивы,
// git clone.
//
// Не весь диск: набор корней (по умолчанию /home, /srv, /opt, /var/www,
// /tmp), за которые не выйти. С правами root «удалить» в /etc или /usr —
// не файловый менеджер, а способ снести хост одним кликом; для системных
// каталогов есть «Конфигурации» с проверкой и историей.
//
// Чтение — изнутри песочницы юнита (ProtectSystem=strict читать не
// мешает), любое изменение — через выход из неё: mkdir, mv, rm, tar и
// git там, где юниту писать нельзя.
package files

import (
	"context"
	"fmt"
	"io"
	"os"
	gopath "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
)

// Runner выполняет команду вне песочницы.
type Runner func(ctx context.Context, argv ...string) (collect.CommandResult, error)

// RunnerEnv — то же с окружением: git получает секрет и ключ через него.
type RunnerEnv func(ctx context.Context, env map[string]string, argv ...string) (collect.CommandResult, error)

// DefaultRoots — где проводнику можно.
var DefaultRoots = []string{"/home", "/srv", "/opt", "/var/www", "/tmp"}

// MaxUploadBytes — потолок одной загрузки. int64 явно: на 32-битных
// сборках 2 ГиБ в int не помещается.
const MaxUploadBytes int64 = 2 << 30

// TransferTimeout — сколько даётся одной загрузке или скачиванию. Обычный
// запрос укладывается в секунды, а файл в сотни мегабайт по медленному
// каналу — нет; и хост, и прокси хаба продлевают сроки до этого.
const TransferTimeout = 5 * time.Minute

// Manager — операции над файлами в пределах корней.
type Manager struct {
	roots  []string
	c      collect.Collector
	run    Runner
	runEnv RunnerEnv
	// tmpDir — куда падает загрузка перед переносом: внутрь песочницы
	// писать можно только в каталог данных, а файл нужен снаружи.
	tmpDir string
}

// NewManager строит проводник. run может быть nil — тогда изменения
// недоступны (fixtures-режим), а обход и скачивание работают.
func NewManager(roots []string, c collect.Collector, run Runner, runEnv RunnerEnv, tmpDir string) *Manager {
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		r = gopath.Clean(r)
		if r != "/" && strings.HasPrefix(r, "/") {
			clean = append(clean, r)
		}
	}
	if len(clean) == 0 {
		clean = append(clean, DefaultRoots...)
	}
	sort.Strings(clean)
	return &Manager{roots: clean, c: c, run: run, runEnv: runEnv, tmpDir: tmpDir}
}

// Roots — корни проводника.
func (m *Manager) Roots() []string { return append([]string(nil), m.roots...) }

// Check отвергает путь вне корней и с подвохом («..», относительный).
func (m *Manager) Check(p string) (string, error) {
	if p == "" || !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || gopath.Clean(p) != p {
		return "", fmt.Errorf("путь должен быть абсолютным и без «..»: %q", p)
	}
	for _, root := range m.roots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return p, nil
		}
	}
	return "", fmt.Errorf("путь %q вне разрешённых каталогов (%s)", p, strings.Join(m.roots, ", "))
}

// Entry — элемент каталога.
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
	// Archive — распаковываемый архив: по нему показывается «распаковать».
	Archive bool `json:"archive,omitempty"`
}

// List читает каталог. Папки первыми, внутри — по имени.
func (m *Manager) List(p string) ([]Entry, error) {
	p, err := m.Check(p)
	if err != nil {
		return nil, err
	}
	infos, err := m.c.ListDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		e := Entry{Name: gopath.Base(fi.Path), Path: fi.Path, IsDir: fi.IsDir, Size: fi.Size, Mode: fi.Mode,
			ModTime: fi.ModTime.UTC().Format("2006-01-02T15:04:05Z07:00")}
		e.Archive = !fi.IsDir && ArchiveKind(fi.Path) != ""
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (m *Manager) mutable() error {
	if m.run == nil {
		return fmt.Errorf("изменение файлов недоступно в этом режиме")
	}
	return nil
}

func (m *Manager) exec(ctx context.Context, argv ...string) error {
	res, err := m.run(ctx, argv...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: %s", argv[0], lastLine(res.Output()))
	}
	return nil
}

// Mkdir создаёт каталог (и родителей внутри корня).
func (m *Manager) Mkdir(ctx context.Context, p string) error {
	if err := m.mutable(); err != nil {
		return err
	}
	p, err := m.Check(p)
	if err != nil {
		return err
	}
	return m.exec(ctx, "mkdir", "-p", "--", p)
}

// Rename переименовывает или переносит в пределах корней, не затирая
// существующее: «-n» — отказ, а не молчаливая замена.
func (m *Manager) Rename(ctx context.Context, from, to string) error {
	if err := m.mutable(); err != nil {
		return err
	}
	from, err := m.Check(from)
	if err != nil {
		return err
	}
	to, err = m.Check(to)
	if err != nil {
		return err
	}
	if m.c.Exists(to) {
		return fmt.Errorf("%s уже существует", to)
	}
	return m.exec(ctx, "mv", "-n", "--", from, to)
}

// Delete удаляет файл или каталог целиком. Сам корень удалить нельзя.
func (m *Manager) Delete(ctx context.Context, p string) error {
	if err := m.mutable(); err != nil {
		return err
	}
	p, err := m.Check(p)
	if err != nil {
		return err
	}
	for _, root := range m.roots {
		if p == root {
			return fmt.Errorf("%s — корень проводника, его удалять нельзя", p)
		}
	}
	return m.exec(ctx, "rm", "-rf", "--", p)
}

// Upload принимает файл: сначала во временный файл каталога данных (туда
// писать можно изнутри юнита), потом переносом на место.
//
// name — имя или относительный путь (src/app.py): так грузится целая
// папка, файл за файлом, и промежуточные каталоги создаются по дороге.
// Путь проверяется целиком: «..» и абсолютный — отказ, итог обязан
// остаться внутри корня.
func (m *Manager) Upload(ctx context.Context, dir, name string, r io.Reader) (string, error) {
	if err := m.mutable(); err != nil {
		return "", err
	}
	dir, err := m.Check(dir)
	if err != nil {
		return "", err
	}
	target, err := m.uploadTarget(dir, name)
	if err != nil {
		return "", err
	}
	dir = gopath.Dir(target)
	if err := os.MkdirAll(m.tmpDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(m.tmpDir, "upload-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, io.LimitReader(r, MaxUploadBytes+1)); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if st, _ := tmp.Stat(); st != nil && st.Size() > MaxUploadBytes {
		_ = tmp.Close()
		return "", fmt.Errorf("файл больше %d ГиБ", MaxUploadBytes>>30)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := m.exec(ctx, "mkdir", "-p", "--", dir); err != nil {
		return "", err
	}
	// install, а не mv: временный файл лежит на другой файловой системе
	// или с правами юнита — копия с нормальными правами надёжнее.
	if err := m.exec(ctx, "install", "-m", "0644", "--", tmpPath, target); err != nil {
		return "", err
	}
	return target, nil
}

// uploadTarget — куда положить загруженный файл по имени или
// относительному пути.
func (m *Manager) uploadTarget(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("некорректное имя файла: %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("некорректное имя файла: %q", name)
		}
	}
	target := gopath.Join(dir, name)
	if _, err := m.Check(target); err != nil {
		return "", err
	}
	return target, nil
}

// Open открывает файл для скачивания.
func (m *Manager) Open(p string) (io.ReadCloser, int64, error) {
	p, err := m.Check(p)
	if err != nil {
		return nil, 0, err
	}
	st, err := m.c.Stat(p)
	if err != nil {
		return nil, 0, err
	}
	if st.IsDir {
		return nil, 0, fmt.Errorf("%s — каталог, скачать можно файл", p)
	}
	rc, err := m.c.Open(p)
	if err != nil {
		return nil, 0, err
	}
	return rc, st.Size, nil
}

// ArchiveKind отвечает, чем распаковывать: «zip», «tar» или пусто.
func ArchiveKind(p string) string {
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"),
		strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"), strings.HasSuffix(lower, ".tar.bz2"),
		strings.HasSuffix(lower, ".tbz2"), strings.HasSuffix(lower, ".tar.zst"):
		return "tar"
	}
	return ""
}

// Extract распаковывает архив в dest.
//
// Сначала список: путь с «..» или абсолютный внутри архива — отказ до
// распаковки (zip slip), иначе архив, собранный кем-то ещё, положил бы
// файл в /etc.
func (m *Manager) Extract(ctx context.Context, archive, dest string) error {
	if err := m.mutable(); err != nil {
		return err
	}
	archive, err := m.Check(archive)
	if err != nil {
		return err
	}
	dest, err = m.Check(dest)
	if err != nil {
		return err
	}
	kind := ArchiveKind(archive)
	if kind == "" {
		return fmt.Errorf("%s не похож на архив (zip, tar, tar.gz, tar.xz, tar.bz2)", gopath.Base(archive))
	}
	var listArgv, extractArgv []string
	switch kind {
	case "zip":
		listArgv = []string{"unzip", "-Z1", "--", archive}
		extractArgv = []string{"unzip", "-o", "-q", "--", archive, "-d", dest}
	default:
		listArgv = []string{"tar", "-tf", archive}
		extractArgv = []string{"tar", "-xf", archive, "-C", dest}
	}
	res, err := m.run(ctx, listArgv...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("не удалось прочитать архив: %s", lastLine(res.Output()))
	}
	if bad := UnsafeArchivePath(res.Stdout); bad != "" {
		return fmt.Errorf("в архиве путь %q — он вышел бы за пределы каталога; такой архив не распаковывается", bad)
	}
	if err := m.exec(ctx, "mkdir", "-p", "--", dest); err != nil {
		return err
	}
	return m.exec(ctx, extractArgv...)
}

// UnsafeArchivePath находит в списке файлов архива путь, ведущий наружу.
func UnsafeArchivePath(listing string) string {
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			return line
		}
		for _, part := range strings.Split(filepath.ToSlash(line), "/") {
			if part == ".." {
				return line
			}
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
