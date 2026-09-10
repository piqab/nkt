package vmcreate

import (
	"context"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/jobs"
)

// KindTools — вид задания «доставить недостающее для создания машин».
const KindTools = "vm.tools.install"

// Runner выполняет команду там же, где потом будут выполняться qemu-img
// и virsh, — вне песочницы юнита.
type Runner func(ctx context.Context, argv ...string) (collect.CommandResult, error)

// CheckTools спрашивает, что из нужного уже стоит.
//
// Спрашивает тем же способом, каким команды потом и выполняются: путь
// внутри песочницы юнита и снаружи может отличаться, и проверять в одном
// месте, а запускать в другом — верный способ получить «есть» там, где
// на деле нет.
func CheckTools(ctx context.Context, run Runner) []Tool {
	tools := Tools()
	if run == nil {
		return tools
	}
	for i, t := range tools {
		// command -v встроен в оболочку — отдельного исполняемого файла
		// у него может не быть, поэтому спрашиваем через sh.
		res, err := run(ctx, "sh", "-c", "command -v "+t.Command)
		tools[i].Present = err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != ""
	}
	return tools
}

// ToolsRunner доставляет недостающие пакеты.
type ToolsRunner struct {
	escape Runner
}

// NewToolsRunner строит исполнителя.
func NewToolsRunner(escape Runner) *ToolsRunner {
	return &ToolsRunner{escape: escape}
}

// Resumable — да: apt на середине не продолжить, но повторная установка
// уже установленного ничего не портит, а список пересчитывается заново.
func (r *ToolsRunner) Resumable() bool { return true }

// Run ставит то, чего не хватает.
func (r *ToolsRunner) Run(ctx context.Context, jc *jobs.Context) error {
	jc.Step(1, 1, "установка")
	return InstallTools(ctx, r.escape, jc.Logf)
}

// InstallTools доставляет недостающие программы.
//
// Общая для отдельного задания и для создания машины: машину без них всё
// равно не создать, и отказывать вместо установки значило бы отправлять
// оператора делать руками ровно то, что nkt умеет сам.
func InstallTools(ctx context.Context, run Runner, logf func(string, ...any)) error {
	if run == nil {
		return fmt.Errorf("установка пакетов недоступна в этом режиме")
	}
	missing := MissingTools(CheckTools(ctx, run))
	if len(missing) == 0 {
		logf("Всё нужное уже установлено.")
		return nil
	}
	// Из пары взаимозаменяемых ставится одна: вторая ничего не добавит.
	altTaken := false
	var pkgs []string
	for _, t := range missing {
		if t.Alternative != "" {
			if altTaken {
				continue
			}
			altTaken = true
		}
		pkgs = append(pkgs, t.Package)
	}

	logf("Не хватает: %s. Ставлю пакеты: %s", toolNames(missing), strings.Join(pkgs, ", "))
	if res, err := run(ctx, "apt-get", "update"); err != nil {
		return err
	} else if res.ExitCode != 0 {
		logf("apt-get update ответил кодом %d, продолжаю", res.ExitCode)
	}

	argv := append([]string{"apt-get", "install", "-y"}, pkgs...)
	res, err := run(ctx, argv...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("apt-get install %s: %s", strings.Join(pkgs, " "), firstLine(res.Output()))
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if line != "" {
			logf("%s", line)
		}
	}

	if still := MissingTools(CheckTools(ctx, run)); len(still) > 0 {
		return fmt.Errorf("после установки всё ещё нет: %s", toolNames(still))
	}
	logf("Готово: всё нужное на месте.")
	return nil
}

// toolNames перечисляет команды через запятую.
func toolNames(tools []Tool) string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Command)
	}
	return strings.Join(names, ", ")
}
