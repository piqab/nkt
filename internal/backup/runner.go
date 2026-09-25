package backup

import (
	"context"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Exec выполняет команду вне песочницы юнита, передавая каждую строку
// вывода в logf, и возвращает код выхода.
type Exec func(ctx context.Context, logf func(string, ...any), argv ...string) (int, error)

// Runner — исполнитель заданий бэкапа и восстановления.
type Runner struct {
	root    string
	exec    Exec
	restore bool
}

// NewRunner — исполнитель бэкапа (restore=false) или восстановления.
func NewRunner(root string, exec Exec, restore bool) *Runner {
	return &Runner{root: root, exec: exec, restore: restore}
}

// Resumable — нет: полусделанный бэкап или восстановление повторять с
// середины нельзя, только заново.
func (r *Runner) Resumable() bool { return false }

// Run — один бэкап или одно восстановление.
func (r *Runner) Run(ctx context.Context, jc *jobs.Context) error {
	if r.exec == nil {
		return msgs.Errorf("backup.unavailable")
	}
	var script string
	if r.restore {
		var p RestoreParams
		if err := jc.Params(&p); err != nil {
			return err
		}
		sc, err := RestoreScript(r.root, p)
		if err != nil {
			return err
		}
		script = sc
		jc.StepKey(1, 1, "backup.stepRestore", p.Path)
	} else {
		var p Params
		if err := jc.Params(&p); err != nil {
			return err
		}
		sc, out, err := Script(r.root, p, time.Now())
		if err != nil {
			return err
		}
		script = sc
		jc.StepKey(1, 1, "backup.stepCreate", p.Kind, p.Name, out)
	}
	code, err := r.exec(ctx, jc.Logf, "bash", "-c", script)
	if err != nil {
		return err
	}
	if code != 0 {
		return msgs.Errorf("backup.failed", code)
	}
	return nil
}
