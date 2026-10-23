package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/store"
)

// scriptedCollector отвечает на docker compose заданным выводом и
// запоминает команды.
type scriptedCollector struct {
	collect.Collector
	calls  []string
	answer func(args []string) collect.CommandResult
}

func (s *scriptedCollector) Run(ctx context.Context, name string, args ...string) (collect.CommandResult, error) {
	s.calls = append(s.calls, name+" "+strings.Join(args, " "))
	return s.answer(args), nil
}

// docker stack … start: up --pull never (registry не трогается); compose
// без «up --pull» — start; образа нет — ошибка с подсказкой; stop — stop.
func TestComposeStartStop(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{Mode: config.ModeFixtures, FixturesRoot: root}
	path := "/srv/compose/shop/compose.yaml"

	ok := &scriptedCollector{Collector: collect.NewFixtures(root), answer: func([]string) collect.CommandResult { return collect.CommandResult{} }}
	m := NewServiceManager(cfg, ok, db)
	if _, err := m.ComposeStart(context.Background(), "admin", path); err != nil || len(ok.calls) != 1 || !strings.HasSuffix(ok.calls[0], "up -d --pull never") {
		t.Fatalf("start: %v %v", err, ok.calls)
	}
	if _, err := m.ComposeStop(context.Background(), "admin", path); err != nil || !strings.HasSuffix(ok.calls[1], "stop") {
		t.Fatalf("stop: %v %v", err, ok.calls)
	}

	old := &scriptedCollector{Collector: collect.NewFixtures(root), answer: func(args []string) collect.CommandResult {
		if strings.Contains(strings.Join(args, " "), "--pull") {
			return collect.CommandResult{ExitCode: 1, Stderr: "unknown flag: --pull"}
		}
		return collect.CommandResult{}
	}}
	m = NewServiceManager(cfg, old, db)
	if _, err := m.ComposeStart(context.Background(), "admin", path); err != nil || len(old.calls) != 2 || !strings.HasSuffix(old.calls[1], " start") {
		t.Fatalf("old compose: %v %v", err, old.calls)
	}

	missing := &scriptedCollector{Collector: collect.NewFixtures(root), answer: func([]string) collect.CommandResult {
		return collect.CommandResult{ExitCode: 1, Stderr: "Error: No such image: registry.example.com/shop:1.4"}
	}}
	m = NewServiceManager(cfg, missing, db)
	if _, err := m.ComposeStart(context.Background(), "admin", path); err == nil || !strings.Contains(err.Error(), "deploy") {
		t.Fatalf("missing image: %v", err)
	}
}
