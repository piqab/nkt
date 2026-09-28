package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// История файлов «Дисков»: прежние версии перезаписанных загрузкой файлов
// и правки в редакторе. Индекс — та же таблица config_versions, что у
// «Конфигураций» (одна история на путь), содержимое — то же хранилище;
// большие двоичные файлы пишутся в него потоком, не через память.

// FileHistoryLimits — лимиты истории файлов.
type FileHistoryLimits struct {
	// PerUploadMB — сколько прежних двоичных версий хранит одна загрузка.
	PerUploadMB int `json:"per_upload_mb"`
	// TotalMB — весь каталог истории; сверх — вытесняется самое старое.
	TotalMB int `json:"total_mb"`
	// Days — сколько хранить версии истории файлов.
	Days int `json:"days"`
}

// DefaultFileHistoryLimits — 200 МБ на загрузку, 1 ГБ всего, 30 дней.
var DefaultFileHistoryLimits = FileHistoryLimits{PerUploadMB: 200, TotalMB: 1024, Days: 30}

const fileHistoryLimitsKey = "files.history.limits"

// FileHistoryLimits — сохранённые лимиты (или по умолчанию).
func (m *ConfigManager) FileHistoryLimits(ctx context.Context) FileHistoryLimits {
	l := DefaultFileHistoryLimits
	if raw, ok, err := m.db.KVGet(ctx, fileHistoryLimitsKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &l)
	}
	return l
}

// SetFileHistoryLimits — сохранить лимиты (с проверкой разумных границ).
func (m *ConfigManager) SetFileHistoryLimits(ctx context.Context, l FileHistoryLimits) error {
	if l.PerUploadMB < 0 || l.PerUploadMB > 100_000 || l.TotalMB < 1 || l.TotalMB > 1_000_000 || l.Days < 1 || l.Days > 3650 {
		return msgs.Errorf("files.historyBadLimits")
	}
	b, _ := json.Marshal(l)
	return m.db.KVSet(ctx, fileHistoryLimitsKey, string(b))
}

// putStream кладёт в хранилище содержимое потоком: сумма считается по
// дороге, одинаковое содержимое того же пути хранится один раз.
func (h *history) putStream(path string, in io.Reader) (name, sum string, size int64, err error) {
	if err := os.MkdirAll(h.root, 0o750); err != nil {
		return "", "", 0, msgs.Errorf("control.creatingHistoryDirectory", err)
	}
	tmp, err := os.CreateTemp(h.root, "incoming-*")
	if err != nil {
		return "", "", 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	hash := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, hash), in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", 0, msgs.Errorf("control.writingConfigVersion", err)
	}
	sum = hex.EncodeToString(hash.Sum(nil))
	name = blobName(path, sum)
	full := filepath.Join(h.root, filepath.FromSlash(name))
	if _, err := os.Stat(full); err == nil {
		return name, sum, size, nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", "", 0, msgs.Errorf("control.creatingHistoryDirectory", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		return "", "", 0, msgs.Errorf("control.writingConfigVersion", err)
	}
	return name, sum, size, nil
}

// file — путь содержимого версии на диске (для потокового восстановления).
func (h *history) file(name string) (string, error) {
	if strings.Contains(name, "..") {
		return "", msgs.Errorf("control.invalidVersionName", name)
	}
	return filepath.Join(h.root, filepath.FromSlash(name)), nil
}

// usage — сколько занимает хранилище.
func (h *history) usage() int64 {
	var total int64
	_ = filepath.WalkDir(h.root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// RecordFileSnapshot — версия файла из потока (содержимое на диске до
// перезаписи). Если последняя версия этого пути — то же содержимое, новая
// не заводится и возвращается её ID (размер — 0: места не заняла).
func (m *ConfigManager) RecordFileSnapshot(ctx context.Context, path, service, user, action, note string, in io.Reader) (int64, int64, error) {
	name, sum, size, err := m.hist.putStream(path, in)
	if err != nil {
		return 0, 0, err
	}
	if latest, err := m.db.LatestVersion(ctx, path); err == nil && latest.SHA256 == sum {
		return latest.ID, 0, nil
	}
	id, err := m.db.AddVersion(ctx, store.ConfigVersion{
		Path: path, Service: service, Author: user, Action: action, Note: note,
		Size: size, SHA256: sum, BlobName: name,
	})
	return id, size, err
}

// VersionFile — путь содержимого версии на диске: для восстановления без
// чтения большого файла в память.
func (m *ConfigManager) VersionFile(ctx context.Context, id int64) (store.ConfigVersion, string, error) {
	v, err := m.db.VersionByID(ctx, id)
	if err != nil {
		return v, "", err
	}
	if isSensitiveFile(v.Path) {
		return v, "", ErrSensitiveFile
	}
	p, err := m.hist.file(v.BlobName)
	return v, p, err
}

// DeleteFileVersions — удалить версии и содержимое, на которое больше
// никто не ссылается.
func (m *ConfigManager) DeleteFileVersions(ctx context.Context, ids []int64) error {
	blobs, err := m.db.DeleteVersions(ctx, ids)
	for _, b := range blobs {
		if p, err := m.hist.file(b); err == nil {
			_ = os.Remove(p)
		}
	}
	return err
}

// FileHistoryUsage — занято и лимит, байты.
type FileHistoryUsage struct {
	Used    int64             `json:"used"`
	Limit   int64             `json:"limit"`
	Percent int               `json:"percent"`
	Limits  FileHistoryLimits `json:"limits"`
}

// FileHistoryUsage — сколько занимает история (общее хранилище версий).
func (m *ConfigManager) FileHistoryUsage(ctx context.Context) FileHistoryUsage {
	l := m.FileHistoryLimits(ctx)
	u := FileHistoryUsage{Used: m.hist.usage(), Limit: int64(l.TotalMB) << 20, Limits: l}
	if u.Limit > 0 {
		u.Percent = int(u.Used * 100 / u.Limit)
	}
	return u
}

// PruneFileHistory — вытеснение: версии истории файлов старше срока, затем
// самые старые, пока хранилище не влезет в лимит. Версии конфигураций не
// трогаются. Возвращает число удалённых версий.
func (m *ConfigManager) PruneFileHistory(ctx context.Context) int {
	l := m.FileHistoryLimits(ctx)
	list, err := m.db.FileHistoryVersions(ctx)
	if err != nil {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -l.Days)
	var old []int64
	rest := list[:0]
	for _, v := range list {
		if ts, err := time.Parse(time.RFC3339, v.TS); err == nil && ts.Before(cutoff) {
			old = append(old, v.ID)
			continue
		}
		rest = append(rest, v)
	}
	removed := 0
	if len(old) > 0 {
		_ = m.DeleteFileVersions(ctx, old)
		removed += len(old)
	}
	limit := int64(l.TotalMB) << 20
	used := m.hist.usage()
	for _, v := range rest {
		if used <= limit {
			break
		}
		_ = m.DeleteFileVersions(ctx, []int64{v.ID})
		used -= v.Size
		removed++
	}
	return removed
}

// BigFileVersions — самые большие версии истории файлов (для ручной чистки).
func (m *ConfigManager) BigFileVersions(ctx context.Context, n int) []store.ConfigVersion {
	list, err := m.db.FileHistoryVersions(ctx)
	if err != nil {
		return nil
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Size > list[j].Size })
	if len(list) > n {
		list = list[:n]
	}
	return list
}

// FileHistoryFindings — находки о заполнении истории: с 80% — оповестить
// заранее (высокая: хаб присылает оповещение), со 100% — старые версии
// вытесняются.
func (m *ConfigManager) FileHistoryFindings(ctx context.Context) []model.Finding {
	u := m.FileHistoryUsage(ctx)
	if u.Limit <= 0 || u.Percent < 80 {
		return nil
	}
	used, limit := fmt.Sprintf("%.1f", float64(u.Used)/(1<<30)), fmt.Sprintf("%.1f", float64(u.Limit)/(1<<30))
	f := model.Finding{
		ID:            "files-history-full",
		Rule:          "files.history",
		Severity:      model.SeverityHigh,
		Service:       model.ServiceHost,
		Object:        "files-history",
		TitleKey:      "finding.filesHistoryNear.title",
		TitleArgs:     []any{u.Percent},
		Title:         fmt.Sprintf("История файлов заполнена на %d%%", u.Percent),
		DetailKey:     "finding.filesHistory.detail",
		DetailArgs:    []any{used, limit},
		Detail:        fmt.Sprintf("Занято %s ГБ из %s ГБ.", used, limit),
		SuggestionKey: "finding.filesHistory.suggestion",
		Suggestion:    "«Диски → Файлы» → «Хранилище истории»: удалите ненужные версии или увеличьте лимит.",
	}
	if u.Percent >= 100 {
		f.TitleKey, f.Title = "finding.filesHistoryFull.title", "История файлов заполнена — старые версии вытесняются"
		f.TitleArgs = nil
	}
	return []model.Finding{f}
}
