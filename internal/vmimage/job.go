package vmimage

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"strings"

	"github.com/piqab/nkt/internal/jobs"
)

// KindDownload — вид задания «скачать образ».
const KindDownload = "vmimage.download"

// DownloadParams — вход задания.
//
// Либо образ каталога по идентификатору, либо свой по ссылке: второе
// нужно тем, у кого свой подготовленный образ или зеркало внутри сети.
type DownloadParams struct {
	ImageID string `json:"image_id"`
	// URL, FileName и Checksum описывают свой образ. Имя файла — то, под
	// которым он ляжет в кэш и будет виден в списке.
	URL      string `json:"url,omitempty"`
	FileName string `json:"file_name,omitempty"`
	Checksum string `json:"checksum,omitempty"`
	// ChecksumKind — sha256 (по умолчанию) или sha512.
	ChecksumKind string `json:"checksum_kind,omitempty"`
	// ToHost — положить скачанное в каталог дисков libvirt, а не в кэш
	// nkt. Так добавляют свои образы: там их ждут и qemu, и оператор.
	ToHost bool `json:"to_host,omitempty"`
}

// DownloadRunner качает образ в фоне.
type DownloadRunner struct {
	store *Store
	// move переносит скачанное в каталог дисков libvirt. Отдельно от
	// хранилища: писать туда можно только вне песочницы юнита.
	move func(ctx context.Context, tmpPath, name string) (string, error)
}

// NewDownloadRunner строит исполнителя. move может быть nil — тогда всё
// остаётся в кэше nkt.
func NewDownloadRunner(store *Store,
	move func(ctx context.Context, tmpPath, name string) (string, error)) *DownloadRunner {
	return &DownloadRunner{store: store, move: move}
}

// Resumable — да, и это здесь не формальность: образ весит сотни
// мегабайт, недокачанный кусок остаётся на диске, а докачка запрашивает
// у зеркала только остаток.
func (r *DownloadRunner) Resumable() bool { return true }

// Run качает образ и проверяет сумму.
func (r *DownloadRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p DownloadParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	img, err := resolveDownload(p)
	if err != nil {
		return err
	}

	jc.Step(1, 3, msgs.T(jc.Lang(), "vmimage.stepChecksum"))
	jc.Log("vmimage.image", img.Name)
	jc.Log("vmimage.source", img.URL)
	if img.Custom && img.Checksum == "" {
		// Сказать вслух: образ из каталога проверяется всегда, а свой по
		// ссылке — только если сумму дали. Молчаливая разница в том,
		// чему можно доверять, хуже отсутствия проверки.
		jc.Log("vmimage.checksumGivenImageTakenAs")
	}

	jc.Step(2, 3, msgs.T(jc.Lang(), "vmimage.stepDownload"))
	path, err := r.store.Download(ctx, img, func(pr Progress) {
		if pr.Total > 0 {
			jc.Log("vmimage.downloaded", humanBytes(jc.Lang(), pr.Done), humanBytes(jc.Lang(), pr.Total),
				pr.Done*100/pr.Total)
			return
		}
		jc.Log("vmimage.downloaded2", humanBytes(jc.Lang(), pr.Done))
	})
	if err != nil {
		return err
	}

	jc.Step(3, 3, msgs.T(jc.Lang(), "vmimage.stepVerify"))
	if p.ToHost {
		if r.move == nil {
			return msgs.Errorf("vmimage.movingDiskDirectoryUnavailableMode")
		}
		target, err := r.move(ctx, path, img.FileName)
		if err != nil {
			return err
		}
		jc.Log("vmimage.done", target)
		return nil
	}
	jc.Log("vmimage.doneChecksumMatched", path, img.ChecksumKind)
	return nil
}

// resolveDownload превращает вход задания в запись образа.
func resolveDownload(p DownloadParams) (Image, error) {
	if p.URL == "" {
		img, ok := ByID(p.ImageID)
		if !ok {
			return Image{}, msgs.Errorf("vmimage.suchImageCatalog", p.ImageID)
		}
		return img, nil
	}
	name := strings.TrimSpace(p.FileName)
	if name == "" {
		return Image{}, msgs.Errorf("vmimage.fileNameGivenImage")
	}
	if !validFileName(name) {
		return Image{}, msgs.Errorf("vmcreate.invalidFileName", name)
	}
	kind := strings.ToLower(strings.TrimSpace(p.ChecksumKind))
	if kind == "" {
		kind = SHA256
	}
	img := CustomImage(name)
	img.URL = p.URL
	img.Checksum = strings.TrimSpace(p.Checksum)
	img.ChecksumKind = kind
	return img, nil
}

// humanBytes показывает размер так, как его читают, а не в байтах.
func humanBytes(lang msgs.Lang, n int64) string {
	const unit = 1024
	if n < unit {
		return msgs.T(lang, "vmimage.bytes", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), msgs.T(lang, []string{"vmimage.unitKB", "vmimage.unitMB", "vmimage.unitGB", "vmimage.unitTB"}[exp]))
}
