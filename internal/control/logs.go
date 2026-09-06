package control

import (
	"context"
	"errors"
	"fmt"
	gopath "path"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/inventory"
)

// LogRoot is the only directory plain log files may be read from.
//
// Deliberately narrow. An administrator can already read anything through
// the terminal, so this is not a security boundary against them — it is a
// boundary against this feature turning into a general "read any file as
// root" endpoint, which is a much larger thing to expose than "show me the
// logs" and would be reached by an entirely different set of mistakes
// (a typo'd path, a stored view pointing at /etc/shadow, a copied URL).
const LogRoot = "/var/log"

// ErrLogPathNotAllowed is returned for anything outside [LogRoot].
var ErrLogPathNotAllowed = errors.New("читать можно только файлы внутри " + LogRoot)

// LogSourceKind distinguishes the two things worth calling a log here.
const (
	LogKindUnit = "unit" // systemd journal for one unit
	LogKindFile = "file" // a plain file under LogRoot
)

// LogSource is one selectable log.
type LogSource struct {
	Kind string `json:"kind"`
	// Name is the unit name ("nginx.service") or the absolute file path.
	Name string `json:"name"`
	// Size is only meaningful for files; journald has no equivalent.
	Size int64 `json:"size,omitempty"`
	// Service ties a file back to whichever service the inventory thinks
	// owns it, so the picker can group "the nginx logs" together.
	Service string `json:"service,omitempty"`
}

// LogManager lists and streams logs.
type LogManager struct {
	c       collect.Collector
	scanner *inventory.Scanner
}

func NewLogManager(c collect.Collector, scanner *inventory.Scanner) *LogManager {
	return &LogManager{c: c, scanner: scanner}
}

// CheckLogPath accepts only clean absolute paths inside [LogRoot].
//
// Rejecting "." and ".." before cleaning matters as much as the prefix
// check: "/var/log/../etc/shadow" has the right prefix and cleans to
// something else entirely.
func CheckLogPath(path string) error {
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return ErrLogPathNotAllowed
	}
	if gopath.Clean(path) != path {
		return ErrLogPathNotAllowed
	}
	if path != LogRoot && !strings.HasPrefix(path, LogRoot+"/") {
		return ErrLogPathNotAllowed
	}
	return nil
}

// ListSources returns the journald units worth offering plus the readable
// files under [LogRoot].
func (m *LogManager) ListSources() []LogSource {
	out := make([]LogSource, 0, 32)

	// Units come from the last inventory scan rather than `systemctl
	// list-units`: those are the services this host is actually understood
	// to run, which is a far shorter and more useful list.
	if snap := m.scanner.Latest(); snap != nil {
		for _, svc := range snap.Services {
			unit := svc.Unit
			if unit == "" {
				unit = svc.Name
			}
			if unit == "" {
				continue
			}
			out = append(out, LogSource{Kind: LogKindUnit, Name: unit, Service: svc.Name})
		}
	}

	files := m.listFiles(LogRoot, 0)
	out = append(out, files...)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == LogKindUnit
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// listFiles walks LogRoot a couple of levels deep — nginx and friends keep
// their logs one directory down, and going deeper than that finds mostly
// rotated archives nobody wants to tail.
func (m *LogManager) listFiles(dir string, depth int) []LogSource {
	if depth > 2 {
		return nil
	}
	entries, err := m.c.ListDir(dir)
	if err != nil {
		return nil
	}
	var out []LogSource
	for _, e := range entries {
		if e.IsDir {
			out = append(out, m.listFiles(e.Path, depth+1)...)
			continue
		}
		if !looksLikeLog(e.Path) {
			continue
		}
		out = append(out, LogSource{Kind: LogKindFile, Name: e.Path, Size: e.Size})
	}
	return out
}

// looksLikeLog skips the compressed and numbered leftovers of log rotation:
// they cannot be followed and only bury the file actually being written.
func looksLikeLog(path string) bool {
	base := gopath.Base(path)
	for _, suffix := range []string{".gz", ".xz", ".bz2", ".zst", ".old"} {
		if strings.HasSuffix(base, suffix) {
			return false
		}
	}
	// "error.log.1", "syslog.2" and so on.
	if i := strings.LastIndex(base, "."); i >= 0 {
		if rest := base[i+1:]; rest != "" && strings.IndexFunc(rest, func(r rune) bool {
			return r < '0' || r > '9'
		}) == -1 {
			return false
		}
	}
	if strings.HasSuffix(base, ".log") || base == "syslog" || base == "messages" ||
		base == "auth.log" || base == "kern.log" || base == "dmesg" {
		return true
	}
	// Anything else under /var/log that is plainly a file is still offered —
	// hosts put all sorts of things there, and an operator asking for a
	// specific name should not be told it does not exist.
	return !strings.Contains(base, ".")
}

// StreamArgv is the command that follows [source], newest [lines] first.
//
// Both forms keep printing as the file grows: `tail -F` also survives log
// rotation replacing the file underneath it, which is exactly what happens
// to any log worth watching for more than a day.
func (m *LogManager) StreamArgv(source LogSource, lines int) ([]string, error) {
	if lines <= 0 {
		lines = 500
	}
	switch source.Kind {
	case LogKindUnit:
		if source.Name == "" || strings.ContainsAny(source.Name, " \t\n") {
			return nil, fmt.Errorf("некорректное имя юнита")
		}
		return []string{
			"journalctl", "--no-pager", "--output=short-iso",
			"-n", fmt.Sprint(lines), "-f", "-u", source.Name,
		}, nil
	case LogKindFile:
		if err := CheckLogPath(source.Name); err != nil {
			return nil, err
		}
		return []string{"tail", "-n", fmt.Sprint(lines), "-F", source.Name}, nil
	default:
		return nil, fmt.Errorf("неизвестный вид источника %q", source.Kind)
	}
}

// Snapshot returns the last [lines] of a source without following it — the
// fallback for clients that cannot hold a WebSocket open.
func (m *LogManager) Snapshot(ctx context.Context, source LogSource, lines int) (string, error) {
	argv, err := m.StreamArgv(source, lines)
	if err != nil {
		return "", err
	}
	// Drop the follow flag: same command, one shot.
	filtered := make([]string, 0, len(argv))
	for _, a := range argv {
		if a == "-f" || a == "-F" {
			continue
		}
		filtered = append(filtered, a)
	}
	res, err := m.c.Run(ctx, filtered[0], filtered[1:]...)
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}
