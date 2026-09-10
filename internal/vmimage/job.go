package vmimage

import (
	"context"
	"fmt"

	"github.com/piqab/nkt/internal/jobs"
)

// KindDownload — вид задания «скачать образ».
const KindDownload = "vmimage.download"

// DownloadParams — вход задания.
type DownloadParams struct {
	ImageID string `json:"image_id"`
}

// DownloadRunner качает образ в фоне.
type DownloadRunner struct {
	store *Store
}

// NewDownloadRunner строит исполнителя.
func NewDownloadRunner(store *Store) *DownloadRunner { return &DownloadRunner{store: store} }

// Resumable — да, и это здесь не формальность: образ весит сотни
// мегабайт, недокачанный кусок остаётся на диске, а докачка запрашивает
// у зеркала только остаток.
func (r *DownloadRunner) Resumable() bool { return true }

// Run качает образ и проверяет сумму.
func (r *DownloadRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p DownloadParams
	if err := jc.Params(&p); err != nil {
		return fmt.Errorf("разбор задания: %w", err)
	}
	img, ok := ByID(p.ImageID)
	if !ok {
		return fmt.Errorf("нет такого образа в каталоге: %q", p.ImageID)
	}

	jc.Step(1, 3, "контрольная сумма")
	jc.Logf("Образ: %s", img.Name)
	jc.Logf("Источник: %s", img.URL)

	jc.Step(2, 3, "скачивание")
	path, err := r.store.Download(ctx, img, func(pr Progress) {
		if pr.Total > 0 {
			jc.Logf("скачано %s из %s (%d%%)", humanBytes(pr.Done), humanBytes(pr.Total),
				pr.Done*100/pr.Total)
			return
		}
		jc.Logf("скачано %s", humanBytes(pr.Done))
	})
	if err != nil {
		return err
	}

	jc.Step(3, 3, "проверка")
	jc.Logf("Готово: %s (сумма %s сошлась)", path, img.ChecksumKind)
	return nil
}

// humanBytes показывает размер так, как его читают, а не в байтах.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), []string{"КБ", "МБ", "ГБ", "ТБ"}[exp])
}
