package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Fixtures serves a canned host snapshot from a directory tree, so the whole
// application — parsers, analyzers, dashboard — can run on a developer machine.
//
// Layout of the snapshot root:
//
//	etc/nginx/...            files as they would appear on the host
//	etc/haproxy/...
//	srv/docker/...
//	var/log/...
//	.commands/index.json     canned command outputs
//	.docker/*.json           canned Docker Engine API responses
type Fixtures struct {
	root string

	once     sync.Once
	commands []fixtureCommand
	loadErr  error

	mu    sync.Mutex
	units map[string]string // simulated systemd unit states
}

type fixtureCommand struct {
	Match      []string `json:"match"`
	Stdout     string   `json:"stdout"`
	StdoutFile string   `json:"stdout_file"`
	Stderr     string   `json:"stderr"`
	ExitCode   int      `json:"exit_code"`
	Simulated  bool     `json:"simulated"`
}

type fixtureIndex struct {
	Commands []fixtureCommand `json:"commands"`
}

// NewFixtures builds a collector backed by a snapshot directory.
func NewFixtures(root string) *Fixtures {
	return &Fixtures{root: root, units: map[string]string{}}
}

// Mode implements Collector.
func (f *Fixtures) Mode() string { return "fixtures" }

// resolve maps a host path onto the snapshot, refusing to escape the root.
func (f *Fixtures) resolve(hostPath string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(hostPath, "\\", "/"))
	local := filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	rootAbs, err := filepath.Abs(f.root)
	if err != nil {
		return "", err
	}
	localAbs, err := filepath.Abs(local)
	if err != nil {
		return "", err
	}
	if localAbs != rootAbs && !strings.HasPrefix(localAbs, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("путь %s выходит за пределы снапшота", hostPath)
	}
	return localAbs, nil
}

// unresolve maps a snapshot path back to the host path it represents.
func (f *Fixtures) unresolve(localPath string) string {
	rootAbs, _ := filepath.Abs(f.root)
	rel, err := filepath.Rel(rootAbs, localPath)
	if err != nil {
		return filepath.ToSlash(localPath)
	}
	return "/" + filepath.ToSlash(rel)
}

func (f *Fixtures) Exists(p string) bool {
	local, err := f.resolve(p)
	if err != nil {
		return false
	}
	_, err = os.Stat(local)
	return err == nil
}

func (f *Fixtures) ReadFile(p string) ([]byte, error) {
	local, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(local)
	if err != nil {
		// Report the host path, not the snapshot path, so errors read naturally.
		return nil, fmt.Errorf("чтение %s: %w", p, err)
	}
	return data, nil
}

func (f *Fixtures) Open(p string) (io.ReadCloser, error) {
	local, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	return os.Open(local)
}

func (f *Fixtures) Stat(p string) (FileInfo, error) {
	local, err := f.resolve(p)
	if err != nil {
		return FileInfo{}, err
	}
	st, err := os.Stat(local)
	if err != nil {
		return FileInfo{}, err
	}
	info := fileInfoFrom(p, st)
	return info, nil
}

func (f *Fixtures) ListDir(p string) ([]FileInfo, error) {
	local, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(local)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // snapshot metadata directories are not part of the host
		}
		hostChild := path.Join(path.Clean("/"+p), e.Name())
		st, err := e.Info()
		if err != nil {
			out = append(out, FileInfo{Path: hostChild, IsDir: e.IsDir(), Readable: false})
			continue
		}
		out = append(out, fileInfoFrom(hostChild, st))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (f *Fixtures) Glob(pattern string) ([]string, error) {
	local, err := f.resolve(pattern)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(local)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, f.unresolve(m))
	}
	sort.Strings(out)
	return out, nil
}

// WriteFile writes into the snapshot, so the edit → validate → rollback flow is
// fully exercisable without a Linux host.
func (f *Fixtures) WriteFile(p string, data []byte, mode fs.FileMode) error {
	local, err := f.resolve(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	return os.WriteFile(local, data, mode)
}

// --------------------------------------------------------------------- commands

func (f *Fixtures) loadCommands() {
	f.once.Do(func() {
		raw, err := os.ReadFile(filepath.Join(f.root, ".commands", "index.json"))
		if err != nil {
			if !os.IsNotExist(err) {
				f.loadErr = err
			}
			return
		}
		var idx fixtureIndex
		if err := json.Unmarshal(raw, &idx); err != nil {
			f.loadErr = fmt.Errorf("разбор .commands/index.json: %w", err)
			return
		}
		f.commands = idx.Commands
		// Longer prefixes win, so a specific entry beats a catch-all.
		sort.SliceStable(f.commands, func(i, j int) bool {
			return len(f.commands[i].Match) > len(f.commands[j].Match)
		})
	})
}

func matchesPrefix(argv, prefix []string) bool {
	if len(prefix) == 0 || len(prefix) > len(argv) {
		return false
	}
	for i, p := range prefix {
		if argv[i] != p {
			return false
		}
	}
	return true
}

func (f *Fixtures) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	f.loadCommands()
	if f.loadErr != nil {
		return CommandResult{}, f.loadErr
	}
	argv := append([]string{name}, args...)
	res := CommandResult{Argv: argv, Simulated: true, Duration: "0s"}

	// systemd state is simulated statefully so that stopping a unit in the UI is
	// reflected by the next status read.
	if handled, out := f.systemd(argv); handled {
		res.Stdout = out
		return res, nil
	}

	for _, c := range f.commands {
		if !matchesPrefix(argv, c.Match) {
			continue
		}
		res.ExitCode = c.ExitCode
		res.Stderr = c.Stderr
		res.Simulated = c.Simulated || c.StdoutFile == ""
		res.Stdout = c.Stdout
		if c.StdoutFile != "" {
			raw, err := os.ReadFile(filepath.Join(f.root, ".commands", c.StdoutFile))
			if err != nil {
				return res, fmt.Errorf("чтение вывода фикстуры %s: %w", c.StdoutFile, err)
			}
			res.Stdout = string(raw)
			res.Simulated = false
		}
		res.Stdout = f.applyUnitOverride(argv, res.Stdout)
		return res, nil
	}

	res.ExitCode = 127
	res.Stderr = fmt.Sprintf("в снапшоте нет заготовленного вывода для команды: %s", strings.Join(argv, " "))
	return res, nil
}

// systemd emulates the small subset of systemctl the dashboard relies on.
func (f *Fixtures) systemd(argv []string) (bool, string) {
	if len(argv) < 3 || argv[0] != "systemctl" {
		return false, ""
	}
	verb, unit := argv[1], strings.TrimSuffix(argv[2], ".service")

	f.mu.Lock()
	defer f.mu.Unlock()

	switch verb {
	case "start", "restart", "reload", "reload-or-restart":
		f.units[unit] = "active"
		return true, ""
	case "stop":
		f.units[unit] = "inactive"
		return true, ""
	case "is-active":
		if state, ok := f.units[unit]; ok {
			return true, state + "\n"
		}
		return false, "" // fall through to the canned answer
	}
	return false, ""
}

// applyUnitOverride rewrites a canned "systemctl show" answer so that a unit
// stopped or started through the dashboard is reflected in the next status read.
func (f *Fixtures) applyUnitOverride(argv []string, stdout string) string {
	if len(argv) < 3 || argv[0] != "systemctl" || argv[1] != "show" {
		return stdout
	}
	unit := strings.TrimSuffix(argv[2], ".service")

	f.mu.Lock()
	state, ok := f.units[unit]
	f.mu.Unlock()
	if !ok {
		return stdout
	}
	sub := "running"
	if state != "active" {
		sub = "dead"
	}

	lines := strings.Split(stdout, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "ActiveState="):
			lines[i] = "ActiveState=" + state
		case strings.HasPrefix(line, "SubState="):
			lines[i] = "SubState=" + sub
		}
	}
	return strings.Join(lines, "\n")
}

// --------------------------------------------------------------------- docker

func (f *Fixtures) DockerAPI(_ context.Context, method, apiPath string, _ []byte) ([]byte, int, error) {
	clean := apiPath
	if i := strings.IndexByte(clean, '?'); i >= 0 {
		clean = clean[:i]
	}
	clean = strings.Trim(clean, "/")

	if method != "GET" {
		// Mutations (restart/stop/start) succeed silently, like the real API.
		return nil, 204, nil
	}

	slug := strings.ReplaceAll(clean, "/", "_")
	candidates := []string{slug + ".json"}
	if strings.HasSuffix(slug, "_stats") {
		candidates = append(candidates, "stats.json")
	}
	if strings.HasSuffix(slug, "_json") && strings.HasPrefix(slug, "containers_") {
		candidates = append(candidates, "inspect.json")
	}
	for _, c := range candidates {
		raw, err := os.ReadFile(filepath.Join(f.root, ".docker", c))
		if err == nil {
			return raw, 200, nil
		}
	}
	return []byte(`{"message":"no such fixture"}`), 404, nil
}

func (f *Fixtures) HostInfo(_ context.Context) HostInfo {
	info := HostInfo{
		Mode:     f.Mode(),
		Hostname: "fixture-host",
		Kernel:   "Linux 6.8.0-generic",
		OS:       "Debian GNU/Linux 12 (bookworm)",
		Notes: []string{
			"Режим снапшота: данные читаются из " + filepath.ToSlash(f.root) + ", а не с реального хоста.",
			"Команды управления выполняются в симуляции и ничего не меняют.",
		},
	}
	if raw, err := os.ReadFile(filepath.Join(f.root, "etc", "hostname")); err == nil {
		if h := strings.TrimSpace(string(raw)); h != "" {
			info.Hostname = h
		}
	}
	if runtime.GOOS == "windows" {
		info.Notes = append(info.Notes, "Приложение запущено на Windows — это нормально для режима fixtures.")
	}
	return info
}
