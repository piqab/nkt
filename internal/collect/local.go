package collect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Local reads the real filesystem and runs real commands. Intended for the
// Linux host being managed.
type Local struct {
	dockerSocket   string
	podmanSocket   string
	commandTimeout time.Duration
	docker         *http.Client
	podman         *http.Client
	escape         Escape
}

// Escape выполняет команду вне песочницы собственного юнита, передавая ей
// stdin. Подставляется снаружи (cmd/nkt даёт api.RunUnrestrictedInput) —
// весь код выхода из песочницы живёт в internal/api, а импортировать его
// отсюда нельзя: api сам зависит от collect.
type Escape func(ctx context.Context, stdin []byte, argv ...string) (CommandResult, error)

// SetEscape включает запись мимо песочницы. Без неё Local ведёт себя как
// раньше: пишет тем, что доступно изнутри юнита, и честно возвращает
// отказ, если каталог смонтирован только для чтения.
func (l *Local) SetEscape(e Escape) { l.escape = e }

// NewLocal builds a collector bound to the running machine.
func NewLocal(dockerSocket, podmanSocket string, commandTimeout time.Duration) *Local {
	l := &Local{dockerSocket: dockerSocket, podmanSocket: podmanSocket, commandTimeout: commandTimeout}
	l.docker = unixSocketClient(func() string { return l.dockerSocket })
	l.podman = unixSocketClient(func() string { return l.podmanSocket })
	return l
}

// unixSocketClient builds an http.Client that dials a unix socket instead of
// TCP — both Docker's and Podman's engine APIs are exposed this way. socket
// is a func rather than a plain string so the client can be built before the
// Local struct's field is fully populated (see NewLocal).
func unixSocketClient(socket func() string) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket())
			},
		},
	}
}

// Mode implements Collector.
func (l *Local) Mode() string { return "local" }

func (l *Local) Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (l *Local) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

func (l *Local) Open(p string) (io.ReadCloser, error) { return os.Open(p) }

func (l *Local) Stat(p string) (FileInfo, error) {
	st, err := os.Stat(p)
	if err != nil {
		return FileInfo{}, err
	}
	return fileInfoFrom(p, st), nil
}

func fileInfoFrom(p string, st os.FileInfo) FileInfo {
	return FileInfo{
		Path:     p,
		Size:     st.Size(),
		Mode:     st.Mode().String(),
		ModTime:  st.ModTime().UTC(),
		IsDir:    st.IsDir(),
		Readable: true,
	}
}

func (l *Local) ListDir(p string) ([]FileInfo, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		st, err := e.Info()
		if err != nil {
			out = append(out, FileInfo{Path: path.Join(p, e.Name()), IsDir: e.IsDir(), Readable: false})
			continue
		}
		out = append(out, fileInfoFrom(path.Join(p, e.Name()), st))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (l *Local) Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	for i := range matches {
		matches[i] = filepath.ToSlash(matches[i])
	}
	sort.Strings(matches)
	return matches, nil
}

func (l *Local) WriteFile(p string, data []byte, mode fs.FileMode) error {
	// Write to a sibling temp file and rename, so a crash mid-write can never
	// leave nginx or haproxy with a truncated config.
	dir := filepath.Dir(p)
	// Mirrors Fixtures.WriteFile: most writers target an existing config
	// directory, but a first-time write (a freshly generated certificate, for
	// instance) may need its directory created.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Каталога нет, а создать его изнутри песочницы нельзя. Снаружи —
		// можно: скрипт ниже делает mkdir -p сам.
		if escErr := l.writeUnrestricted(p, data, mode); escErr == nil {
			return nil
		}
		return msgs.Errorf("collect.creatingDirectory", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".nkt-*")
	if err != nil {
		// Каталог недоступен на запись, а сам файл — возможно, да. Так
		// выглядит /etc/hosts или /etc/resolv.conf под ProtectSystem=strict:
		// ReadWritePaths может открыть на запись файл, но не каталог, в
		// котором он лежит, а временный файл создаётся именно там.
		//
		// Тогда пишем на месте. Это не атомарно: обрыв на середине оставит
		// файл обрезанным — но выбор здесь не между атомарностью и её
		// отсутствием, а между записью на месте и невозможностью записать
		// вовсе. Для целевых файлов (одна-две строки настроек) риск
		// несопоставим с потерей возможности их править.
		if writeInPlace(p, data, mode) == nil {
			return nil
		}
		// Каталог закрыт целиком — ни временного файла, ни записи на
		// месте. Это ровно тот случай, ради которого существует выход из
		// песочницы: снаружи юнита каталог обычный и доступен на запись.
		if escErr := l.writeUnrestricted(p, data, mode); escErr == nil {
			return nil
		} else if !errors.Is(escErr, errNoEscape) {
			return escErr
		}
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

// errNoEscape — выхода из песочницы нет (SetEscape не вызывали), значит
// запасного пути тоже нет и звать его бессмысленно.
var errNoEscape = msgs.Errorf("collect.writingOutsideSandboxUnavailable")

// privilegedWriteScript пишет файл там, куда изнутри юнита не дотянуться.
//
// Выполняется вне песочницы, поэтому каталог виден обычным, доступным на
// запись. Содержимое приходит на stdin: промежуточный файл не годится, у
// юнита свой /tmp (PrivateTmp=yes), которого в пространстве монтирования
// хоста нет.
//
// Запись атомарная — тот же приём, что и в основном пути: временный файл
// рядом с целевым и переименование. Права и владелец существующего файла
// сохраняются (chmod/chown --reference): иначе правка, скажем,
// /etc/ssh/sshd_config или файла fail2ban незаметно расширила бы доступ к
// нему до переданного режима по умолчанию.
const privilegedWriteScript = `set -e
d=$(dirname "$1")
mkdir -p "$d"
t="$d/.nkt-write.$$"
cat > "$t"
if [ -e "$1" ]; then
	chmod --reference="$1" "$t"
	chown --reference="$1" "$t"
else
	chmod "$2" "$t"
fi
mv -f "$t" "$1"
`

// writeUnrestricted — запасной путь записи через выход из песочницы.
// Возвращает errNoEscape, если выхода нет: вызывающий тогда сообщает свою
// исходную ошибку, а не подменяет её отсутствием механизма.
func (l *Local) writeUnrestricted(p string, data []byte, mode fs.FileMode) error {
	if l.escape == nil {
		return errNoEscape
	}
	timeout := l.commandTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	res, err := l.escape(ctx, data, "sh", "-c", privilegedWriteScript, "sh", p,
		fmt.Sprintf("%04o", mode.Perm()))
	// Путь в текст не добавляется: вызывающий (control.describeWriteError)
	// уже назвал файл, и повтор превращал бы сообщение в кашу.
	if err != nil {
		return msgs.Errorf("collect.writingOutsideSandbox", err)
	}
	if res.ExitCode != 0 {
		// Код возврата — тоже каталожной ошибкой: так он переводится вместе
		// с внешним сообщением.
		var detail any = strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = msgs.Errorf("collect.exitCode", res.ExitCode)
		}
		return msgs.Errorf("collect.writingOutsideSandbox2", detail)
	}
	return nil
}

// Writable реализует Collector. Проверяется и сам файл, и его каталог:
// записать можно либо через временный файл рядом (нужен доступный на
// запись каталог), либо на месте (нужен доступный на запись файл) — см.
// WriteFile выше.
//
// syscall.Access, а не попытка открыть на запись: access(2) на файловой
// системе, смонтированной только для чтения, честно отвечает EROFS, и при
// этом ничего не создаёт и не меняет времена доступа.
func (l *Local) Writable(p string) bool {
	if syscall.Access(p, wOK) == nil {
		return true
	}
	if syscall.Access(filepath.Dir(p), wOK) == nil {
		return true
	}
	// Отказ вида EROFS — это песочница собственного юнита, а не сама
	// файловая система: снаружи каталог обычный, и запасной путь записи
	// туда дотянется. Отличать важно, иначе редактор пометит «только
	// чтение» файлы, которые на деле правятся (и наоборот — обещать
	// правку там, где диск действительно только для чтения, тоже нельзя,
	// поэтому проверяется именно EROFS, а не любой отказ).
	return l.escape != nil && readOnlyBySandbox(p)
}

// readOnlyBySandbox отвечает, упирается ли запись именно в EROFS. Для
// ещё не созданного файла смотрит на каталог: именно он в этот момент и
// закрыт.
func readOnlyBySandbox(p string) bool {
	err := syscall.Access(p, wOK)
	if errors.Is(err, syscall.ENOENT) {
		err = syscall.Access(filepath.Dir(p), wOK)
	}
	return errors.Is(err, syscall.EROFS)
}

// wOK — константа access(2) «проверить право на запись».
const wOK = 0x2

func (l *Local) DeleteFile(p string) error {
	return os.Remove(p)
}

func (l *Local) Mkdir(p string) error {
	return os.MkdirAll(p, 0o755)
}

// cEnv — окружение для команд, вывод которых разбирается.
//
// Каждый разборщик в этом приложении читает английский вывод: «State:
// running», «Persistent: yes», «active (running)». На хосте с русской
// локалью те же команды отвечают по-русски, и разбор молча даёт пустоту:
// список машин приходил без состояний, и в интерфейсе все машины
// выглядели неактивными. Локаль задаётся здесь одним местом, а не
// вспоминается в каждом вызове; ровно так же поступает хаб с командами
// по SSH (см. runRemote).
func cEnv() []string {
	return append(os.Environ(), "LC_ALL=C", "LANG=C", "LANGUAGE=C")
}

func (l *Local) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	return l.RunTimeout(ctx, l.commandTimeout, name, args...)
}

func (l *Local) RunTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) (CommandResult, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = cEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()

	res := CommandResult{
		Argv:     append([]string{name}, args...),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(started).Round(time.Millisecond).String(),
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, msgs.Errorf("collect.running", name, err)
	}
	return res, nil
}

func (l *Local) DockerAPI(ctx context.Context, method, apiPath string, body []byte) ([]byte, int, error) {
	if runtime.GOOS == "windows" {
		return nil, 0, msgs.Errorf("collect.dockerOverUnixSocket", ErrNotSupported)
	}
	return engineAPI(ctx, l.docker, "docker", method, apiPath, body)
}

func (l *Local) PodmanAPI(ctx context.Context, method, apiPath string, body []byte) ([]byte, int, error) {
	if runtime.GOOS == "windows" {
		return nil, 0, msgs.Errorf("collect.podmanOverUnixSocket", ErrNotSupported)
	}
	return engineAPI(ctx, l.podman, "podman", method, apiPath, body)
}

// engineAPI performs one request against a Docker-API-shaped unix-socket
// engine — Docker and Podman both speak this protocol, just over different
// sockets. host is only used to build a syntactically valid URL; the actual
// destination is whatever the http.Client's DialContext dials.
func engineAPI(ctx context.Context, client *http.Client, host, method, apiPath string, body []byte) ([]byte, int, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	url := "http://" + host + apiPath
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s api %s %s: %w", host, method, apiPath, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

func (l *Local) HostInfo(ctx context.Context) HostInfo {
	info := HostInfo{Mode: l.Mode(), OS: runtime.GOOS}
	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}
	if res, err := l.Run(ctx, "uname", "-sr"); err == nil && res.OK() {
		info.Kernel = strings.TrimSpace(res.Stdout)
	}
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
				break
			}
		}
	}
	if os.Geteuid() != 0 {
		info.Notes = append(info.Notes, msgs.Tc(ctx, "collect.notRoot"))
	}
	return info
}

// writeInPlace перезаписывает существующий файл без временного: запасной
// путь для случая, когда каталог только на чтение (см. WriteFile).
// Именно существующий: если файла нет, создать его в закрытом на запись
// каталоге всё равно нельзя, и молча делать вид, что получилось, нельзя
// тем более.
func writeInPlace(path string, data []byte, mode fs.FileMode) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}
