package control

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"io"
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
var ErrLogPathNotAllowed = msgs.Errorf("control.logPathNotAllowed", LogRoot)

// ErrArchivedNotFollowable is returned when something asks to follow a
// rotated file. Reading it once is the only thing that makes sense.
var ErrArchivedNotFollowable = msgs.Errorf("control.archivedFileDoesGrowCan")

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
	// Archived marks a rotated generation ("syslog.1", "syslog.2.gz").
	// These never grow, so they are read once rather than followed — and
	// they are often where the content actually is, since the active file
	// can sit empty for days after a rotation.
	Archived bool `json:"archived,omitempty"`
	// Compressed archives have to be decoded before they are text at all;
	// `tail` on one returns binary noise.
	Compressed bool `json:"compressed,omitempty"`
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
		if out[i].Archived != out[j].Archived {
			return !out[i].Archived
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
		isLog, archived, compressed := classifyLog(e.Path)
		if !isLog {
			continue
		}
		out = append(out, LogSource{
			Kind: LogKindFile, Name: e.Path, Size: e.Size,
			Archived: archived, Compressed: compressed,
		})
	}
	return out
}

// decompressors maps an archive suffix to the command that prints it as
// text. gzip is handled in Go instead (stdlib), so it is absent here.
var decompressors = map[string]string{
	".xz":  "xzcat",
	".bz2": "bzcat",
	".zst": "zstdcat",
}

// classifyLog decides whether a path is a log at all and, if so, whether it
// is a rotated generation and whether it is compressed.
//
// Rotation produces two different things and they need different handling:
// "syslog.1" is plain text that `tail` reads fine, while "syslog.2.gz" is
// binary until something decodes it.
func classifyLog(path string) (isLog, archived, compressed bool) {
	base := gopath.Base(path)

	for suffix := range decompressors {
		if strings.HasSuffix(base, suffix) {
			return true, true, true
		}
	}
	if strings.HasSuffix(base, ".gz") {
		return true, true, true
	}
	if strings.HasSuffix(base, ".old") {
		return true, true, false
	}
	// "error.log.1", "syslog.2" — a numeric last segment is logrotate's
	// uncompressed generation.
	if i := strings.LastIndex(base, "."); i >= 0 {
		if rest := base[i+1:]; rest != "" && strings.IndexFunc(rest, func(r rune) bool {
			return r < '0' || r > '9'
		}) == -1 {
			return true, true, false
		}
	}

	if strings.HasSuffix(base, ".log") || base == "syslog" || base == "messages" ||
		base == "auth.log" || base == "kern.log" || base == "dmesg" {
		return true, false, false
	}
	// Anything else under /var/log that is plainly a file is still offered —
	// hosts put all sorts of things there, and an operator asking for a
	// specific name should not be told it does not exist.
	return !strings.Contains(base, "."), false, false
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
			return nil, msgs.Errorf("control.invalidUnitName")
		}
		return []string{
			"journalctl", "--no-pager", "--output=short-iso",
			"-n", fmt.Sprint(lines), "-f", "-u", source.Name,
		}, nil
	case LogKindFile:
		if err := CheckLogPath(source.Name); err != nil {
			return nil, err
		}
		if _, archived, _ := classifyLog(source.Name); archived {
			// A rotated file never grows again; following it would hold a
			// process open forever printing nothing.
			return nil, ErrArchivedNotFollowable
		}
		return []string{"tail", "-n", fmt.Sprint(lines), "-F", source.Name}, nil
	default:
		return nil, msgs.Errorf("control.unknownSourceKind", source.Kind)
	}
}

// Snapshot returns the last [lines] of a source without following it.
//
// This is the only way to read a rotated generation: it does not grow, and a
// compressed one is not text until it is decoded. Only the tail is kept in
// memory — an archive can be tens of megabytes decompressed, and none of it
// except the end is wanted.
func (m *LogManager) Snapshot(ctx context.Context, source LogSource, lines int) (string, error) {
	if lines <= 0 {
		lines = 500
	}
	if source.Kind == LogKindUnit {
		argv, err := m.StreamArgv(source, lines)
		if err != nil {
			return "", err
		}
		return m.runWithout(ctx, argv, "-f")
	}

	if err := CheckLogPath(source.Name); err != nil {
		return "", err
	}
	_, _, compressed := classifyLog(source.Name)
	if !compressed {
		res, err := m.c.Run(ctx, "tail", "-n", fmt.Sprint(lines), source.Name)
		if err != nil {
			return "", err
		}
		return res.Stdout, nil
	}
	return m.readCompressed(ctx, source.Name, lines)
}

func (m *LogManager) runWithout(ctx context.Context, argv []string, drop string) (string, error) {
	filtered := make([]string, 0, len(argv))
	for _, a := range argv {
		if a == drop {
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

// readCompressed decodes an archive and returns its last [lines] lines.
//
// gzip is decoded in-process because it is what logrotate writes by default
// and Go has it in the standard library — no external tool to depend on, and
// the decode is streamed rather than buffered whole. The rarer formats fall
// back to their usual command-line decoder, passed the path as its own
// argument so nothing is ever handed to a shell.
func (m *LogManager) readCompressed(ctx context.Context, path string, lines int) (string, error) {
	base := gopath.Base(path)
	for suffix, tool := range decompressors {
		if strings.HasSuffix(base, suffix) {
			res, err := m.c.Run(ctx, tool, path)
			if err != nil {
				return "", fmt.Errorf("%s: %w", tool, err)
			}
			return lastLines(strings.NewReader(res.Stdout), lines)
		}
	}

	raw, err := m.c.ReadFile(path)
	if err != nil {
		return "", err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", msgs.Errorf("control.couldExtract", path, err)
	}
	defer zr.Close()
	return lastLines(zr, lines)
}

// lastLines keeps a ring of the final n lines, so decoding a large archive
// never holds more than that in memory.
func lastLines(r io.Reader, n int) (string, error) {
	ring := make([]string, 0, n)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(ring, "\n"), nil
}
