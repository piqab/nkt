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

// CheckTools спрашивает у хоста, что из нужного уже стоит.
func CheckTools(ctx context.Context, c collect.Collector) []Tool {
	tools := Tools()
	for i, t := range tools {
		res, err := c.Run(ctx, "command", "-v", t.Command)
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != "" {
			tools[i].Present = true
			continue
		}
		// command -v встроен в оболочку, и отдельного исполняемого файла
		// у него может не быть — тогда спрашиваем иначе.
		res, err = c.Run(ctx, "sh", "-c", "command -v "+t.Command)
		tools[i].Present = err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != ""
	}
	return tools
}

// ToolsRunner доставляет недостающие пакеты.
type ToolsRunner struct {
	c      collect.Collector
	escape func(ctx context.Context, argv ...string) (collect.CommandResult, error)
}

// NewToolsRunner строит исполнителя.
func NewToolsRunner(c collect.Collector,
	escape func(ctx context.Context, argv ...string) (collect.CommandResult, error)) *ToolsRunner {
	return &ToolsRunner{c: c, escape: escape}
}

// Resumable — да: apt на середине не продолжить, но повторная установка
// уже установленного ничего не портит, а список пересчитывается заново.
func (r *ToolsRunner) Resumable() bool { return true }

// Run ставит то, чего не хватает.
func (r *ToolsRunner) Run(ctx context.Context, jc *jobs.Context) error {
	if r.escape == nil {
		return fmt.Errorf("установка пакетов недоступна в этом режиме")
	}
	missing := MissingTools(CheckTools(ctx, r.c))
	if len(missing) == 0 {
		jc.Logf("Всё нужное уже установлено.")
		return nil
	}
	// Из пары взаимозаменяемых ставится одна: вторая ничего не добавит.
	seen := map[string]bool{}
	var pkgs []string
	for _, t := range missing {
		if t.Alternative != "" && seen["alt"] {
			continue
		}
		if t.Alternative != "" {
			seen["alt"] = true
		}
		pkgs = append(pkgs, t.Package)
	}

	jc.Step(1, 2, "обновление списка пакетов")
	jc.Logf("Ставлю: %s", strings.Join(pkgs, ", "))
	if res, err := r.escape(ctx, "apt-get", "update"); err != nil {
		return err
	} else if res.ExitCode != 0 {
		jc.Logf("apt-get update ответил кодом %d, продолжаю", res.ExitCode)
	}

	jc.Step(2, 2, "установка")
	argv := append([]string{"apt-get", "install", "-y"}, pkgs...)
	res, err := r.escape(ctx, argv...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("apt-get install: %s", firstLine(res.Output()))
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if line != "" {
			jc.Logf("%s", line)
		}
	}

	still := MissingTools(CheckTools(ctx, r.c))
	if len(still) > 0 {
		names := make([]string, 0, len(still))
		for _, t := range still {
			names = append(names, t.Command)
		}
		return fmt.Errorf("после установки всё ещё нет: %s", strings.Join(names, ", "))
	}
	jc.Logf("Готово: всё нужное на месте.")
	return nil
}
