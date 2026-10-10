package parse

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// osCollector: в контейнере «alp» — Alpine, в «scratch» os-release нет.
type osCollector struct {
	collect.Collector
	calls atomic.Int32
}

func (f *osCollector) Run(_ context.Context, name string, args ...string) (collect.CommandResult, error) {
	f.calls.Add(1)
	if len(args) == 4 && args[0] == "exec" && args[1] == "alp" {
		return collect.CommandResult{Stdout: "NAME=\"Alpine Linux\"\nID=alpine\nPRETTY_NAME=\"Alpine Linux v3.20\"\n"}, nil
	}
	return collect.CommandResult{ExitCode: 1, Stderr: "no such file"}, nil
}

func (f *osCollector) RunTimeout(ctx context.Context, _ time.Duration, name string, args ...string) (collect.CommandResult, error) {
	return f.Run(ctx, name, args...)
}

func TestFillContainerOS(t *testing.T) {
	c := &osCollector{}
	var a, b *model.OSInfo
	fillContainerOS(context.Background(), c, "docker", map[string]**model.OSInfo{"alp": &a, "scratch": &b})
	if a == nil || a.ID != "alpine" || a.Name != "Alpine Linux v3.20" || b == nil || b.ID != "linux" {
		t.Fatalf("%+v %+v", a, b)
	}
	// Второй скан — из кэша, без exec.
	n := c.calls.Load()
	var a2 *model.OSInfo
	fillContainerOS(context.Background(), c, "docker", map[string]**model.OSInfo{"alp": &a2})
	if c.calls.Load() != n || a2 == nil || a2.ID != "alpine" {
		t.Fatalf("кэш: calls %d→%d, %+v", n, c.calls.Load(), a2)
	}
	// «scratch» исчез — из кэша забыт.
	ctOSMu.Lock()
	_, kept := ctOSCache["docker/scratch"]
	ctOSMu.Unlock()
	if kept {
		t.Fatal("исчезнувший контейнер остался в кэше")
	}
}
