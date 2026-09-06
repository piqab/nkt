package control

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// The path check is what keeps "show me the logs" from becoming "read any
// file as root", so it is tested against the ways that boundary is usually
// crossed rather than only the happy path.
func TestCheckLogPath(t *testing.T) {
	allowed := []string{
		"/var/log",
		"/var/log/syslog",
		"/var/log/nginx/error.log",
		"/var/log/journal/remote/x.journal",
	}
	for _, p := range allowed {
		if err := CheckLogPath(p); err != nil {
			t.Errorf("CheckLogPath(%q) = %v, want nil", p, err)
		}
	}

	rejected := []string{
		"",
		"var/log/syslog",              // relative
		"/etc/shadow",                 // outside the root
		"/var/logs/syslog",            // prefix looks right, directory is not
		"/var/log/../etc/shadow",      // climbs out after a valid prefix
		"/var/log/./syslog",           // unclean, and cleaning changes it
		"/var/log//syslog",            // ditto
		"/var/log/nginx/../../secret", // deeper climb
	}
	for _, p := range rejected {
		if err := CheckLogPath(p); err == nil {
			t.Errorf("CheckLogPath(%q) = nil, want an error", p)
		}
	}
}

func TestStreamArgv(t *testing.T) {
	m := &LogManager{}

	argv, err := m.StreamArgv(LogSource{Kind: LogKindUnit, Name: "nginx.service"}, 200)
	if err != nil {
		t.Fatalf("unit: %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "journalctl") || !strings.Contains(joined, "-u nginx.service") {
		t.Errorf("unit argv = %q", joined)
	}
	if !strings.Contains(joined, "-n 200") || !strings.Contains(joined, "-f") {
		t.Errorf("unit argv should follow and honour the line count: %q", joined)
	}

	argv, err = m.StreamArgv(LogSource{Kind: LogKindFile, Name: "/var/log/syslog"}, 0)
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	joined = strings.Join(argv, " ")
	// -F rather than -f: a log worth watching gets rotated, and the follow
	// has to survive the file being replaced underneath it.
	if !strings.Contains(joined, "tail") || !strings.Contains(joined, "-F") {
		t.Errorf("file argv = %q", joined)
	}
	if !strings.Contains(joined, "-n 500") {
		t.Errorf("a zero line count should fall back to a default: %q", joined)
	}

	if _, err := m.StreamArgv(LogSource{Kind: LogKindFile, Name: "/etc/shadow"}, 100); err == nil {
		t.Error("a file outside /var/log must be refused")
	}
	// A unit name is interpolated into an argv, never a shell line, but a
	// name with whitespace is still nonsense and worth refusing outright.
	if _, err := m.StreamArgv(LogSource{Kind: LogKindUnit, Name: "a b"}, 100); err == nil {
		t.Error("a unit name with whitespace must be refused")
	}
	if _, err := m.StreamArgv(LogSource{Kind: "nonsense", Name: "x"}, 100); err == nil {
		t.Error("an unknown source kind must be refused")
	}
}

func TestClassifyLog(t *testing.T) {
	cases := []struct {
		path                        string
		isLog, archived, compressed bool
	}{
		{"/var/log/syslog", true, false, false},
		{"/var/log/nginx/error.log", true, false, false},
		{"/var/log/dmesg", true, false, false},

		// logrotate leaves two different things behind, and they are read
		// in two different ways.
		{"/var/log/syslog.1", true, true, false},
		{"/var/log/nginx/error.log.2", true, true, false},
		{"/var/log/syslog.2.gz", true, true, true},
		{"/var/log/apt/history.log.1.gz", true, true, true},
		{"/var/log/journal.xz", true, true, true},
		{"/var/log/wtmp.old", true, true, false},
	}
	for _, c := range cases {
		isLog, archived, compressed := classifyLog(c.path)
		if isLog != c.isLog || archived != c.archived || compressed != c.compressed {
			t.Errorf("classifyLog(%q) = (%v,%v,%v), want (%v,%v,%v)",
				c.path, isLog, archived, compressed, c.isLog, c.archived, c.compressed)
		}
	}
}

func TestArchivedIsNotFollowable(t *testing.T) {
	m := &LogManager{}
	// Following a file that will never grow again would hold a process open
	// printing nothing at all.
	if _, err := m.StreamArgv(LogSource{Kind: LogKindFile, Name: "/var/log/syslog.1"}, 100); err == nil {
		t.Error("a rotated file must not be followable")
	}
	if _, err := m.StreamArgv(LogSource{Kind: LogKindFile, Name: "/var/log/syslog.2.gz"}, 100); err == nil {
		t.Error("a compressed archive must not be followable")
	}
	if _, err := m.StreamArgv(LogSource{Kind: LogKindFile, Name: "/var/log/syslog"}, 100); err != nil {
		t.Errorf("an active file must still be followable: %v", err)
	}
}

func TestLastLinesKeepsOnlyTheTail(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	out, err := lastLines(strings.NewReader(sb.String()), 3)
	if err != nil {
		t.Fatalf("lastLines: %v", err)
	}
	if out != "line 998\nline 999\nline 1000" {
		t.Errorf("lastLines = %q", out)
	}
}

func TestSnapshotDecompressesGzip(t *testing.T) {
	// The case that matters on a real host: after a rotation the active file
	// is empty and everything worth reading is inside the .gz.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(zw, "archived line %d\n", i)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	m := &LogManager{c: fakeCollector{data: buf.Bytes()}}
	out, err := m.Snapshot(context.Background(),
		LogSource{Kind: LogKindFile, Name: "/var/log/test.log.1.gz"}, 3)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if out != "archived line 8\narchived line 9\narchived line 10" {
		t.Errorf("Snapshot = %q", out)
	}
}

// fakeCollector serves one canned file; nothing else is reached by these
// tests.
type fakeCollector struct {
	collect.Collector
	data []byte
}

func (f fakeCollector) ReadFile(string) ([]byte, error) { return f.data, nil }
