package hub

import (
	"context"
	"errors"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Установка и обновление nkt на хосте — задание хаба: журнал в
// «Заданиях» на языке читающего, живой поток по WebSocket, отмена,
// след после перезапуска. Само дело делает Manager.install, как и
// раньше; задание даёт ему журнал (installJob.jc) и контекст.

// KindHostInstall — вид задания установки/обновления хоста.
const KindHostInstall = "host.install"

// HostInstallParams — параметры задания.
type HostInstallParams struct {
	HostID int64 `json:"host_id"`
	// Force — перезаписать чужой nkt на хосте (см. checkForeignInstall).
	Force bool `json:"force,omitempty"`
	// Bootstrap — разовая подготовка хоста перед установкой; nil для
	// обычной установки и любого обновления.
	Bootstrap *BootstrapOptions `json:"bootstrap,omitempty"`
}

// HostInstallRunner выполняет KindHostInstall.
type HostInstallRunner struct{ m *Manager }

// NewHostInstallRunner — исполнитель для менеджера заданий хаба.
func NewHostInstallRunner(m *Manager) *HostInstallRunner { return &HostInstallRunner{m: m} }

// installJobTimeout — предел одной установки: кросс-сборка, заливка и
// первый вход через туннель укладываются с запасом.
const installJobTimeout = 10 * time.Minute

// Run — одна установка.
func (r *HostInstallRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p HostInstallParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	if _, err := r.m.db.HostByID(ctx, p.HostID); err != nil {
		return msgs.Errorf("hub.hostFound", err)
	}
	ctx, cancel := context.WithTimeout(ctx, installJobTimeout)
	defer cancel()

	r.m.jobsMu.Lock()
	// Запись завёл StartInstall; после перезапуска хаба или из очереди
	// её может не быть — тогда заводится здесь. Чужое ещё идущее задание
	// на этот хост снимается: два install() разом пишут статус хоста
	// наперегонки.
	job := r.m.jobByHost[p.HostID]
	if job == nil || job.jobID != jc.Job.ID {
		if job != nil && !job.isDone() {
			job.cancelNow()
		}
		job = &installJob{jobID: jc.Job.ID, created: time.Now(), hostID: p.HostID, bootstrap: p.Bootstrap}
		r.m.jobByHost[p.HostID] = job
	}
	job.mu.Lock()
	job.jc, job.cancel = jc, cancel
	job.mu.Unlock()
	r.m.jobsMu.Unlock()
	job.append("hub.startingInstall")

	// Отмена задания гасит контекст, но шаг, застрявший внутри SSH
	// (у golang.org/x/crypto/ssh контекста нет), сам этого не заметит —
	// сторож закрывает соединение, и заливка или команда обрываются.
	watch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			job.cancelNow()
		case <-watch:
		}
	}()
	err := r.m.install(ctx, p.HostID, job)
	close(watch)
	job.finish(err)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// Таймаут — понятная ошибка вместо «context deadline exceeded».
		return msgs.Errorf("hub.installTimedOut", installJobTimeout)
	}
	return err
}

// SetJobs подключает менеджер заданий хаба: StartInstall заводит через
// него задания KindHostInstall.
func (m *Manager) SetJobs(j *jobs.Manager) { m.jobs = j }
