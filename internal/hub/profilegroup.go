package hub

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/profile"
	"github.com/piqab/nkt/internal/store"
)

// Профиль на группу хостов: хаб обходит хосты по одному, на каждом
// строит план и применяет его.
//
// План строится на самом хосте, а не здесь: состояние у каждой машины
// своё, и общий план на всю группу был бы неправдой сразу для всех, кроме
// одной. Хаб же ведёт своё задание, чтобы у оператора была одна строка,
// за которой следить, — а подробности каждого хоста остаются в его
// собственном журнале.

// KindGroupApply — вид задания хаба.
const KindGroupApply = "profile.group-apply"

// hostJobPoll — как часто спрашивать хост, чем кончилось его задание.
const hostJobPoll = 2 * time.Second

// hostJobTimeout — сколько ждать одного хоста. Установка пакетов бывает
// долгой, но не бесконечной, а зависшее задание не должно держать всю
// группу.
const hostJobTimeout = 30 * time.Minute

// GroupApplyParams — вход задания.
type GroupApplyParams struct {
	ProfileID int64  `json:"profile_id"`
	Profile   string `json:"profile"`
	Group     string `json:"group"`
	Content   string `json:"content"`
	// Hosts — идентификаторы хостов на момент запуска. Список
	// фиксируется здесь, а не перечитывается на каждом шаге: хост,
	// добавленный в группу посреди раскатки, не должен молча в неё
	// попасть.
	Hosts []int64 `json:"hosts"`
}

type groupApplyResume struct {
	Done int `json:"done"`
}

// GroupApplyRunner раскатывает профиль по хостам группы.
type GroupApplyRunner struct {
	m *Manager
}

// NewGroupApplyRunner строит исполнителя.
func NewGroupApplyRunner(m *Manager) *GroupApplyRunner { return &GroupApplyRunner{m: m} }

// Resumable — да: список хостов и номер последнего пройденного
// сохранены, а повторно применённый план на хосте ничего не портит.
func (r *GroupApplyRunner) Resumable() bool { return true }

// Run обходит хосты по одному.
//
// Последовательно, а не разом: разом — это одновременный apt-get на всей
// группе, и если профиль ошибочный, ошибка достаётся сразу всем. По
// одному она видна на первом же хосте.
func (r *GroupApplyRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p GroupApplyParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	if len(p.Hosts) == 0 {
		jc.Log("hub.groupHasHosts2", p.Group)
		return nil
	}
	var done groupApplyResume
	if err := jc.LoadResume(&done); err != nil {
		return msgs.Errorf("hub.parsingResumeState", err)
	}
	if done.Done > 0 {
		jc.Log("hub.continuingHost", done.Done+1, len(p.Hosts))
	}

	for i := done.Done; i < len(p.Hosts); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		hostID := p.Hosts[i]
		host, err := r.m.db.HostByID(ctx, hostID)
		if err != nil {
			jc.Log("hub.hostSkipped", i+1, len(p.Hosts), hostID, err)
			done.Done = i + 1
			jc.SaveResume(done)
			continue
		}
		jc.Step(i+1, len(p.Hosts), host.Name)
		jc.Logf("[%d/%d] %s", i+1, len(p.Hosts), host.Name)

		if err := r.applyToHost(ctx, jc, host, p); err != nil {
			// Останавливаемся на первом отказавшем хосте: если профиль
			// плох, продолжать раскатку по остальным — это множить
			// поломку.
			return fmt.Errorf("%s: %w", host.Name, err)
		}
		done.Done = i + 1
		jc.SaveResume(done)
	}
	jc.Log("hub.doneHostsProcessed", len(p.Hosts))
	return nil
}

// applyToHost строит план на хосте и применяет его.
func (r *GroupApplyRunner) applyToHost(ctx context.Context, jc *jobs.Context,
	host store.Host, p GroupApplyParams) error {

	var plan profile.Plan
	if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/profiles/plan",
		map[string]string{"content": p.Content}, &plan); err != nil {
		return msgs.Errorf("hub.buildingPlan", err)
	}
	for _, u := range plan.Unknown {
		jc.Log("hub.couldJudge", u)
	}
	if len(plan.Changes) == 0 {
		jc.Log("hub.drift")
		return nil
	}
	jc.Log("hub.driftItems", len(plan.Changes))

	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := r.m.HostAPI(ctx, host.ID, "POST", "/api/profiles/apply",
		map[string]any{"name": p.Profile, "changes": plan.Changes}, &started); err != nil {
		return msgs.Errorf("hub.startingApply", err)
	}
	return r.waitHostJob(ctx, jc, host, started.JobID)
}

// waitHostJob ждёт, чем кончится задание на хосте, пересказывая его
// журнал в своё.
func (r *GroupApplyRunner) waitHostJob(ctx context.Context, jc *jobs.Context,
	host store.Host, jobID int64) error {

	deadline := time.Now().Add(hostJobTimeout)
	var after int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.jobHostDidFinishWithin", hostJobTimeout)
		}

		var res struct {
			Job   store.Job          `json:"job"`
			Lines []store.JobLogLine `json:"lines"`
		}
		path := fmt.Sprintf("/api/jobs/%d/log?after=%d", jobID, after)
		if _, err := r.m.HostAPI(ctx, host.ID, "GET", path, nil, &res); err != nil {
			// Связь с хостом могла моргнуть — это не повод считать
			// применение проваленным: следующий заход дочитает то же
			// самое, номер строки не сдвинулся.
			jc.Log("hub.connectionLostRetrying", err)
			if !sleepCtx(ctx, hostJobPoll) {
				return ctx.Err()
			}
			continue
		}
		for _, line := range res.Lines {
			after = line.Seq
			jc.Logf("      %s", line.Text)
		}
		switch res.Job.Status {
		case store.JobSucceeded:
			return nil
		case store.JobFailed, store.JobCanceled, store.JobInterrupted:
			return msgs.Errorf("hub.jobHost", res.Job.Status, res.Job.Error)
		}
		if !sleepCtx(ctx, hostJobPoll) {
			return ctx.Err()
		}
	}
}

// sleepCtx спит, но не мешает отмене. false — задание отменяют.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
