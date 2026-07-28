// Package collect abstracts access to the inspected host.
//
// Every parser, analyzer and control action goes through a Collector, so the
// application can run against a canned snapshot on a developer machine and
// against the real filesystem on a Linux server without any other code change.
// Host paths are always POSIX paths ("/etc/nginx/nginx.conf") regardless of the
// operating system the binary itself runs on.
package collect

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"
)

// ErrNotSupported is returned by operations a collector cannot perform.
var ErrNotSupported = errors.New("операция не поддерживается в текущем режиме")

// FileInfo describes a file on the inspected host.
type FileInfo struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	ModTime  time.Time `json:"mod_time"`
	IsDir    bool      `json:"is_dir"`
	Readable bool      `json:"readable"`
}

// CommandResult is the outcome of running a command on the host.
type CommandResult struct {
	Argv      []string `json:"argv"`
	ExitCode  int      `json:"exit_code"`
	Stdout    string   `json:"stdout"`
	Stderr    string   `json:"stderr"`
	Simulated bool     `json:"simulated"`
	Duration  string   `json:"duration"`
}

// OK reports a zero exit code.
func (r CommandResult) OK() bool { return r.ExitCode == 0 }

// Output returns stdout, falling back to stderr when stdout is empty — which is
// what most of these tools do when they complain.
func (r CommandResult) Output() string {
	if strings.TrimSpace(r.Stdout) != "" {
		return r.Stdout
	}
	return r.Stderr
}

// HostInfo describes the inspected host.
type HostInfo struct {
	Mode     string   `json:"mode"`
	Hostname string   `json:"hostname"`
	Kernel   string   `json:"kernel"`
	OS       string   `json:"os"`
	Notes    []string `json:"notes,omitempty"`
}

// Collector reads, observes and mutates the inspected host.
type Collector interface {
	// Mode is "fixtures" or "local".
	Mode() string

	Exists(path string) bool
	ReadFile(path string) ([]byte, error)
	Open(path string) (io.ReadCloser, error)
	Stat(path string) (FileInfo, error)
	// ListDir returns absolute host paths of the entries in path, non-recursive.
	ListDir(path string) ([]FileInfo, error)
	// Glob returns sorted absolute host paths matching a shell-style pattern
	// such as "/etc/nginx/conf.d/*.conf".
	Glob(pattern string) ([]string, error)

	WriteFile(path string, data []byte, mode fs.FileMode) error

	// Run executes a command on the host. It returns an error only when the
	// command could not be started; a non-zero exit lives in the result.
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)

	// DockerAPI performs a request against the Docker Engine API. apiPath is
	// everything after the version prefix, e.g. "/containers/json?all=1".
	DockerAPI(ctx context.Context, method, apiPath string, body []byte) ([]byte, int, error)

	HostInfo(ctx context.Context) HostInfo
}

// ReadText is a convenience wrapper returning file contents as a string.
func ReadText(c Collector, path string) (string, error) {
	raw, err := c.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ReadLines returns the file split into lines; tail > 0 keeps only the last N.
func ReadLines(c Collector, path string, tail int) ([]string, error) {
	text, err := ReadText(c, path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return lines, nil
}

// Which reports whether a command exists on the host.
func Which(ctx context.Context, c Collector, binary string) bool {
	res, err := c.Run(ctx, "sh", "-c", "command -v "+binary)
	return err == nil && res.OK()
}
