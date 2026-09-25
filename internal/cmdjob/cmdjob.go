// Package cmdjob — общее фоновое задание «выполнить команды»: установка
// пакетов, создание инстансов, скачивание образов. Вывод идёт в журнал
// задания, проценты из строк («Retrieving image: rootfs: 45%»,
// «Progress: [ 45%]») — в шаг, окно журнала рисует по ним полосу.
//
// Команды собирает только серверный обработчик: клиент присылает
// параметры операции, а не argv. Секретов в параметрах нет — они видны
// в списке заданий; пароль передаётся через Secrets по ключу.
package cmdjob

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Kind — вид задания.
const Kind = "cmd.run"

// Command — одна команда задания.
type Command struct {
	Argv []string `json:"argv"`
	// StepKey — название шага из каталога msgs (аргументы — StepArgs).
	StepKey  string `json:"step_key,omitempty"`
	StepArgs []any  `json:"step_args,omitempty"`
	// Optional — неудача не валит задание, а пишется в журнал.
	Optional bool `json:"optional,omitempty"`
	// SecretRef — ключ секрета, который подставляется вместо «{secret}» в
	// argv прямо перед запуском; в журнал argv не пишется.
	SecretRef string `json:"secret_ref,omitempty"`
}

// Params — вход задания.
type Params struct {
	Commands []Command `json:"commands"`
	// Refresh — после успеха пересобрать инвентарь.
	Refresh bool `json:"refresh,omitempty"`
}

// Exec выполняет argv вне песочницы, отдавая строки вывода в logf.
type Exec func(ctx context.Context, logf func(string, ...any), argv ...string) (int, error)

// Secrets отдаёт секрет по ключу (пароль гостя из хранилища nkt).
type Secrets func(ctx context.Context, ref string) (string, error)

// Runner — исполнитель.
type Runner struct {
	exec      Exec
	collector collect.Collector
	secrets   Secrets
	refresh   func(ctx context.Context)
}

// New — exec nil (демо-режим) — команды идут через заготовки сборщика.
func New(exec Exec, c collect.Collector, secrets Secrets, refresh func(context.Context)) *Runner {
	return &Runner{exec: exec, collector: c, secrets: secrets, refresh: refresh}
}

// Resumable — нет: команду с середины не продолжить.
func (r *Runner) Resumable() bool { return false }

var percentRe = regexp.MustCompile(`^(.*?)[\s:\[(]*(\d{1,3})(?:\.\d+)?%`)

// ParsePercent — процент и подпись из строки прогресса; ok=false — не она.
func ParsePercent(line string) (label string, pct int, ok bool) {
	m := percentRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n > 100 {
		return "", 0, false
	}
	label = strings.TrimRight(strings.TrimSpace(m[1]), ":[( ")
	return label, n, true
}

// Run выполняет команды по очереди.
func (r *Runner) Run(ctx context.Context, jc *jobs.Context) error {
	var p Params
	if err := jc.Params(&p); err != nil {
		return err
	}
	if len(p.Commands) == 0 {
		return msgs.Errorf("cmdjob.empty")
	}
	for i, c := range p.Commands {
		if len(c.Argv) == 0 {
			continue
		}
		stepName := ""
		if c.StepKey != "" {
			jc.StepKey(i+1, len(p.Commands), c.StepKey, c.StepArgs...)
			stepName = msgs.T(jc.Lang(), c.StepKey, c.StepArgs...)
		} else {
			jc.Step(i+1, len(p.Commands), strings.Join(c.Argv, " "))
		}
		argv := append([]string{}, c.Argv...)
		if c.SecretRef != "" {
			if r.secrets == nil {
				return msgs.Errorf("cmdjob.noSecrets")
			}
			secret, err := r.secrets(ctx, c.SecretRef)
			if err != nil {
				return err
			}
			for j := range argv {
				argv[j] = strings.ReplaceAll(argv[j], "{secret}", secret)
			}
		} else {
			jc.Logf("$ %s", strings.Join(argv, " "))
		}
		code, err := r.run(ctx, jc, stepName, i, len(p.Commands), argv)
		if err == nil && code != 0 {
			err = msgs.Errorf("cmdjob.exitCode", code)
		}
		if err != nil {
			if c.Optional {
				jc.Logf("! %v", err)
				continue
			}
			return err
		}
	}
	if p.Refresh && r.refresh != nil {
		r.refresh(ctx)
	}
	return nil
}

func (r *Runner) run(ctx context.Context, jc *jobs.Context, stepName string, i, n int, argv []string) (int, error) {
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if label, pct, ok := ParsePercent(line); ok {
			// Процент команды — в шаг задания (N из 100); при нескольких
			// командах подпись держит и название шага.
			name := label
			if n > 1 && stepName != "" {
				name = fmt.Sprintf("%d/%d %s · %s", i+1, n, stepName, label)
			}
			jc.Step(pct, 100, name)
			return
		}
		jc.Logf("%s", line)
	}
	if r.exec != nil {
		return r.exec(ctx, logf, argv...)
	}
	if r.collector == nil {
		return -1, msgs.Errorf("cmdjob.unavailable")
	}
	res, err := r.collector.Run(ctx, argv[0], argv[1:]...)
	if err != nil {
		return -1, err
	}
	for _, l := range strings.Split(strings.TrimRight(res.Output(), "\n"), "\n") {
		if l != "" {
			logf("%s", l)
		}
	}
	return res.ExitCode, nil
}
