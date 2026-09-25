package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	// Сценарий — файлом, а не «bash -c <текст>»: вне песочницы команда
	// идёт через systemd-run, а systemd сам подставляет ${…} в аргументах
	// ExecStart — ${#DISKS[@]}, ${d%% *} и "${specs[@]}" доходили до bash
	// пустыми. Файл в каталоге данных виден и снаружи песочницы.
	path, err := writeScript(r.root, jc.Job.ID, script)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	// Прогресс: служебные строки сценария и проценты qemu-img/tar уходят
	// в шаг задания (N из 100) — окно журнала рисует по нему полосу.
	prog := NewProgress(func(step int, name string) {
		if step < 0 {
			jc.Step(0, 0, name)
			return
		}
		jc.Step(step, 100, name)
	})
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if prog.Line(line) {
			return
		}
		jc.Logf("%s", line)
	}
	code, err := r.exec(ctx, logf, "bash", path)
	if err != nil {
		return err
	}
	if code != 0 {
		return msgs.Errorf("backup.failed", code)
	}
	return nil
}

// writeScript кладёт сценарий в <root>/.run/<id>.sh (0700).
func writeScript(root string, id int64, script string) (string, error) {
	dir := filepath.Join(root, ".run")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("job-%d.sh", id))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", err
	}
	return path, nil
}
