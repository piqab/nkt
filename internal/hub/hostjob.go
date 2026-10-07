package hub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// runHostJob — долгая операция на хосте, которую хаб вызывает изнутри
// своего задания: заданием хоста (?job=1), а не одним запросом. Запрос
// упирался в сроки хоста (4 минуты на маршрут, 2 минуты на запись
// ответа) и обрывал apt-get или kubeadm на полпути; задание идёт сколько
// нужно, видно в «Заданиях» хоста, а его журнал переносится в logf (в
// журнал задания хаба). Старый хост, не знающий ?job=1, выполняет вызов
// как раньше — сразу, ответом на запрос.
func (m *Manager) runHostJob(ctx context.Context, hostID int64, path string, body any, logf func(string)) error {
	call := hostAPIForJobs
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var start struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := call(m, ctx, hostID, "POST", path+sep+"job=1", body, &start); err != nil {
		return err
	}
	if start.JobID == 0 {
		return nil
	}
	var after int64
	failures := 0
	for {
		var res struct {
			Job   store.Job          `json:"job"`
			Lines []store.JobLogLine `json:"lines"`
		}
		_, err := call(m, ctx, hostID, "GET", fmt.Sprintf("/api/jobs/%d/log?after=%d", start.JobID, after), nil, &res)
		if err != nil {
			if ctx.Err() != nil {
				m.cancelHostJob(hostID, start.JobID)
				return ctx.Err()
			}
			// Связь с хостом моргнула — задание там идёт своим ходом;
			// сдаться, только если хост долго не отвечает.
			failures++
			if failures > 30 {
				return msgs.Errorf("hub.hostJobLost", start.JobID, err)
			}
		} else {
			failures = 0
			for _, l := range res.Lines {
				if logf != nil {
					logf(l.Text)
				}
				after = l.Seq
			}
			switch res.Job.Status {
			case store.JobSucceeded:
				return nil
			case store.JobFailed, store.JobCanceled, store.JobInterrupted:
				msg := res.Job.Error
				if msg == "" {
					msg = res.Job.Status
				}
				return msgs.Errorf("hub.hostJobFailed", msg)
			}
		}
		select {
		case <-ctx.Done():
			m.cancelHostJob(hostID, start.JobID)
			return ctx.Err()
		case <-time.After(hostJobPollEvery):
		}
	}
}

// hostAPIForJobs — вызов API хоста для runHostJob; подменяется в тестах.
var hostAPIForJobs = (*Manager).HostAPI

// hostJobPollEvery — как часто спрашивать задание хоста.
var hostJobPollEvery = 2 * time.Second

// cancelHostJob — задание хаба отменено: отменить и задание хоста, а не
// оставлять его работать без присмотра.
func (m *Manager) cancelHostJob(hostID, jobID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = hostAPIForJobs(m, ctx, hostID, "POST", fmt.Sprintf("/api/jobs/%d/cancel", jobID), nil, nil)
}

// hostLog — строки задания хоста в журнал задания хаба, с именем хоста.
func hostLog(jc *jobs.Context, host string) func(string) {
	return func(line string) { jc.Logf("  %s: %s", host, line) }
}
