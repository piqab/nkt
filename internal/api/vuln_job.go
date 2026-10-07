package api

import (
	"context"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Сканирование уязвимостей хоста с ?job=1 — ещё и заданием: само
// сканирование то же (горутина с ходом в s.vuln, его показывает страница
// «Уязвимости»), а задание следит за ним и пишет ход в журнал — так скан
// виден в «Заданиях» и в индикаторе фоновых операций.

// KindVulnScan — вид задания.
const KindVulnScan = "vuln.scan"

type vulnScanRunner struct{ s *Server }

// Resumable — нет: скан после перезапуска запускают заново.
func (v *vulnScanRunner) Resumable() bool { return false }

func (v *vulnScanRunner) Run(ctx context.Context, jc *jobs.Context) error {
	s := v.s
	jc.StepKey(1, 1, "vulns.scanJob")
	// Скан запускает обработчик, задание за ним только следит: успей он
	// закончиться раньше, итог — его последняя ошибка (или её нет).
	return watchProgress(ctx, jc, func() (bool, string, string) {
		s.vuln.mu.Lock()
		defer s.vuln.mu.Unlock()
		return s.vuln.scanning, s.vuln.progress, s.vuln.lastErr
	})
}

// watchProgress переносит ход фоновой работы (running, progress, lastErr)
// в журнал задания, пока она не кончится; её ошибка — ошибка задания.
func watchProgress(ctx context.Context, jc *jobs.Context, state func() (bool, string, string)) error {
	last := ""
	for {
		running, progress, lastErr := state()
		if progress != "" && progress != last {
			last = progress
			jc.Logf("%s", progress)
		}
		if !running {
			if lastErr != "" {
				return msgs.Errorf("vulns.scanFailed", lastErr)
			}
			jc.Log("hostop.done")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
