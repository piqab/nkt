package control

import (
	"strings"
	"testing"
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

func TestLooksLikeLog(t *testing.T) {
	yes := []string{
		"/var/log/syslog",
		"/var/log/nginx/error.log",
		"/var/log/auth.log",
		"/var/log/dmesg",
	}
	for _, p := range yes {
		if !looksLikeLog(p) {
			t.Errorf("looksLikeLog(%q) = false, want true", p)
		}
	}

	// Rotated copies cannot be followed and only bury the live file.
	no := []string{
		"/var/log/syslog.1",
		"/var/log/nginx/error.log.2",
		"/var/log/syslog.gz",
		"/var/log/nginx/access.log.1.gz",
		"/var/log/wtmp.old",
	}
	for _, p := range no {
		if looksLikeLog(p) {
			t.Errorf("looksLikeLog(%q) = true, want false", p)
		}
	}
}
