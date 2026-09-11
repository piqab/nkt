package profile

import (
	"context"
	"fmt"

	"github.com/piqab/nkt/internal/jobs"
)

// KindApply — вид фонового задания «применить профиль».
const KindApply = "profile.apply"

// ApplyParams — вход задания.
type ApplyParams struct {
	ProfileID int64 `json:"profile_id"`
	Name      string `json:"name"`
	// Changes — отмеченные оператором пункты плана, целиком. План
	// сохраняется в задании, а не перестраивается при запуске: между
	// показом и нажатием состояние могло измениться, и применять надо
	// ровно то, что человек видел и одобрил.
	Changes []Change `json:"changes"`
}

// applyResume — что уже сделано. Достаточно номера пункта: пункты идут по
// порядку, и повторять сделанное незачем.
type applyResume struct {
	Done int `json:"done"`
}

// ApplyRunner выполняет задание «применить профиль».
type ApplyRunner struct {
	// applier строится на каждое задание: он помнит, от чьего имени
	// пишется история, а это у каждого запуска своё.
	applier func(user string) Applier
	// rescan пересобирает снимок инвентаря после применения. Профиль
	// меняет ровно то, что в этом снимке и показывается — поднятый
	// стек compose, включённую службу, установленный пакет, — и без
	// пересборки раздел показывает состояние до применения, пока не
	// сработает очередное плановое сканирование.
	rescan func(ctx context.Context) error
}

// NewApplyRunner строит исполнителя. rescan может быть nil — тогда снимок
// обновится сам по расписанию.
func NewApplyRunner(applier func(user string) Applier, rescan func(ctx context.Context) error) *ApplyRunner {
	return &ApplyRunner{applier: applier, rescan: rescan}
}

// Resumable — да. Каждый пункт плана идемпотентен: установка уже
// установленного пакета, включение включённой службы и запись того же
// содержимого ничего не портят, а номер последнего сделанного пункта
// сохраняется, поэтому продолжение не делает лишнего.
func (r *ApplyRunner) Resumable() bool { return true }

// Run применяет отмеченные пункты по порядку.
func (r *ApplyRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ApplyParams
	if err := jc.Params(&p); err != nil {
		return fmt.Errorf("разбор задания: %w", err)
	}
	if len(p.Changes) == 0 {
		jc.Logf("Применять нечего: пунктов не отмечено.")
		return nil
	}
	var done applyResume
	if err := jc.LoadResume(&done); err != nil {
		return fmt.Errorf("разбор состояния продолжения: %w", err)
	}
	if done.Done > 0 {
		jc.Logf("Продолжаю с пункта %d из %d.", done.Done+1, len(p.Changes))
	}

	applier := r.applier(jc.Job.Author)
	for i := done.Done; i < len(p.Changes); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := p.Changes[i]
		jc.Step(i+1, len(p.Changes), c.Action+" "+c.Target)
		jc.Logf("[%d/%d] %s %s", i+1, len(p.Changes), c.Action, c.Target)

		msg, err := applier.Apply(ctx, c)
		if err != nil {
			// Останавливаемся на первой ошибке: следующие пункты часто
			// зависят от предыдущих (служба не запустится без пакета), и
			// длинный список одинаковых отказов хуже одного понятного.
			return fmt.Errorf("%s %s: %w", c.Action, c.Target, err)
		}
		jc.Logf("      %s", msg)
		done.Done = i + 1
		jc.SaveResume(done)
	}
	if r.rescan != nil {
		// Не ошибка задания: пункты применены, а несобравшийся снимок
		// соберётся по расписанию.
		if err := r.rescan(ctx); err != nil {
			jc.Logf("Снимок состояния пересобрать не удалось (%v) — раздел обновится позже.", err)
		}
	}
	jc.Logf("Готово: применено пунктов — %d.", len(p.Changes))
	return nil
}
