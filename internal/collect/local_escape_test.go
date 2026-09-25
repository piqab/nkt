package collect

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Программы из SetEscape выполняются через переданный запускатель, по
// полному пути; остальные — как раньше.
func TestSetEscapeRoutesCommand(t *testing.T) {
	var got []string
	SetEscape([]string{"true"}, func(_ context.Context, argv ...string) (CommandResult, error) {
		got = argv
		return CommandResult{Stdout: "escaped"}, nil
	})
	defer SetEscape(nil, nil)
	l := NewLocal("", "", 0)
	res, err := l.Run(context.Background(), "true", "x")
	if err != nil || res.Stdout != "escaped" || len(got) != 2 || !strings.HasSuffix(got[0], "/true") || got[1] != "x" {
		t.Fatalf("res=%+v err=%v argv=%v", res, err, got)
	}
	if res.Argv[0] != "true" {
		t.Errorf("Argv в результате — как вызвали: %v", res.Argv)
	}
	res, _ = l.Run(context.Background(), "echo", "plain")
	if strings.TrimSpace(res.Stdout) != "plain" {
		t.Errorf("обычная команда: %+v", res)
	}
}

// /snap/bin добавляется в PATH один раз.
func TestEnsureSnapPath(t *testing.T) {
	old := os.Getenv("PATH")
	defer os.Setenv("PATH", old)
	os.Setenv("PATH", "/usr/bin:/bin")
	EnsureSnapPath()
	EnsureSnapPath()
	if os.Getenv("PATH") != "/usr/bin:/bin:/snap/bin" {
		t.Errorf("PATH = %s", os.Getenv("PATH"))
	}
}
