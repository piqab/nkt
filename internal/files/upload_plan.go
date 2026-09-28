package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	gopath "path"
	"strings"
)

// План загрузки: что из присланного списка ляжет новым, что заменит
// существующий файл, что совпадает и загружать незачем, что защищено.

// DefaultProtected — файлы, которые загрузка по умолчанию не
// перезаписывает: настройки и данные, живущие только на сервере.
var DefaultProtected = []string{
	".env", ".env.*", "*.local.*", "local_settings.*", "wp-config.php", "config.local.*",
	"uploads/", "storage/", "media/", "data/",
}

// Protected — путь rel (относительно каталога загрузки) подпадает под
// шаблон: «имя/» — каталог с этим именем на любом уровне; шаблон со «/» —
// весь путь; иначе — имя файла (glob).
func Protected(rel string, patterns []string) bool {
	parts := strings.Split(rel, "/")
	base := parts[len(parts)-1]
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		switch {
		case strings.HasSuffix(p, "/"):
			dir := strings.TrimSuffix(p, "/")
			for _, seg := range parts[:len(parts)-1] {
				if ok, _ := gopath.Match(dir, seg); ok {
					return true
				}
			}
		case strings.Contains(p, "/"):
			if ok, _ := gopath.Match(strings.TrimPrefix(p, "/"), rel); ok {
				return true
			}
		default:
			if ok, _ := gopath.Match(p, base); ok {
				return true
			}
		}
	}
	return false
}

// PlanEntry — файл из загружаемого списка.
type PlanEntry struct {
	Rel    string `json:"rel"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"` // пусто — браузер не считал (большой файл)
}

// Статусы плана.
const (
	PlanNew      = "new"
	PlanSame     = "same"
	PlanChanged  = "changed"
	PlanConflict = "conflict" // на месте файла каталог или наоборот
	PlanInvalid  = "invalid"
)

// PlanItem — что будет с файлом.
type PlanItem struct {
	Rel       string `json:"rel"`
	Path      string `json:"path,omitempty"`
	Status    string `json:"status"`
	Protected bool   `json:"protected,omitempty"`
	// Text — на хосте текстовый файл до MaxEditBytes: для него есть дифф.
	Text         bool   `json:"text,omitempty"`
	ExistingSize int64  `json:"existing_size,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Plan сравнивает список с тем, что уже лежит в каталоге dir.
func (m *Manager) Plan(ctx context.Context, dir string, entries []PlanEntry, protect []string) ([]PlanItem, error) {
	dir, err := m.Check(dir)
	if err != nil {
		return nil, err
	}
	out := make([]PlanItem, 0, len(entries))
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		it := PlanItem{Rel: e.Rel, Status: PlanNew, Protected: Protected(e.Rel, protect)}
		target, err := m.uploadTarget(dir, e.Rel)
		if err != nil {
			it.Status, it.Error = PlanInvalid, err.Error()
			out = append(out, it)
			continue
		}
		it.Path = target
		st, err := m.c.Stat(target)
		if err != nil {
			// Нет файла — но, может, по дороге файл вместо каталога.
			if m.blockedByFile(dir, target) {
				it.Status = PlanConflict
			}
			out = append(out, it)
			continue
		}
		if st.IsDir {
			it.Status = PlanConflict
			out = append(out, it)
			continue
		}
		it.ExistingSize = st.Size
		it.Text = st.Size <= MaxEditBytes
		switch {
		case st.Size != e.Size:
			it.Status = PlanChanged
		case e.SHA256 == "":
			// Размер тот же, суммы нет — считать изменённым надёжнее.
			it.Status = PlanChanged
		default:
			if sum, err := m.hash(target); err == nil && sum == strings.ToLower(e.SHA256) {
				it.Status = PlanSame
			} else {
				it.Status = PlanChanged
			}
		}
		out = append(out, it)
	}
	return out, nil
}

// blockedByFile — один из каталогов по дороге к target на хосте — файл.
func (m *Manager) blockedByFile(dir, target string) bool {
	for p := gopath.Dir(target); p != dir && strings.HasPrefix(p, dir+"/"); p = gopath.Dir(p) {
		if st, err := m.c.Stat(p); err == nil && !st.IsDir {
			return true
		}
	}
	return false
}

// hash — SHA-256 файла на хосте, потоком.
func (m *Manager) hash(p string) (string, error) {
	rc, err := m.c.Open(p)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Hash — SHA-256 и размер файла на хосте (для истории загрузок).
func (m *Manager) Hash(p string) (string, error) {
	p, err := m.Check(p)
	if err != nil {
		return "", err
	}
	return m.hash(p)
}

// UploadTarget — полный путь файла загрузки (с проверками пути).
func (m *Manager) UploadTarget(dir, name string) (string, error) {
	dir, err := m.Check(dir)
	if err != nil {
		return "", err
	}
	return m.uploadTarget(dir, name)
}

// Restore кладёт на место target файл src из локальной ФС (прежнюю
// версию из истории) — той же командой снаружи песочницы, что и загрузка.
func (m *Manager) Restore(ctx context.Context, target, src string) error {
	if err := m.mutable(); err != nil {
		return err
	}
	target, err := m.Check(target)
	if err != nil {
		return err
	}
	if err := m.exec(ctx, "mkdir", "-p", "--", gopath.Dir(target)); err != nil {
		return err
	}
	return m.exec(ctx, "install", "-m", "0644", "--", src, target)
}

// Remove удаляет файл (не каталог) — откат загрузки убирает то, что она
// добавила.
func (m *Manager) Remove(ctx context.Context, p string) error {
	if err := m.mutable(); err != nil {
		return err
	}
	p, err := m.Check(p)
	if err != nil {
		return err
	}
	if st, err := m.c.Stat(p); err != nil || st.IsDir {
		return err
	}
	return m.exec(ctx, "rm", "-f", "--", p)
}
