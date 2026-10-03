package fail2ban

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// Проверка конфигурации fail2ban до записи: копия каталога настроек во
// временный каталог, туда — будущие файлы, и fail2ban-client -c <копия>
// -t. Так видно и то, что сломает сам шаблон, и то, что конфигурация
// хоста сломана уже сейчас (джейл без фильтра и т. п.).

// ConfigTest — итог проверки.
type ConfigTest struct {
	OK bool `json:"ok"`
	// Output — что ответил fail2ban (строки ошибок).
	Output string `json:"output,omitempty"`
	// Preexisting — текущая конфигурация хоста (без изменений) тоже не
	// проходит: сломано не шаблоном.
	Preexisting bool `json:"preexisting,omitempty"`
	// Skipped — проверить нечем (снимок fixtures).
	Skipped bool `json:"skipped,omitempty"`
}

// configTestTimeout — предел одной проверки.
const configTestTimeout = 2 * time.Minute

// TestConfig проверяет конфигурацию root с изменениями changes (путь
// относительно root → содержимое).
func TestConfig(ctx context.Context, c collect.Collector, root string, changes map[string]string) ConfigTest {
	if c.Mode() == "fixtures" {
		return ConfigTest{OK: true, Skipped: true}
	}
	ok, out := testCopy(ctx, c, root, changes)
	if ok {
		return ConfigTest{OK: true}
	}
	t := ConfigTest{Output: out}
	if len(changes) > 0 {
		if base, _ := testCopy(ctx, c, root, nil); !base {
			t.Preexisting = true
		}
	}
	return t
}

// CheckCurrent — проходит ли проверку текущая конфигурация (для находки).
func CheckCurrent(ctx context.Context, c collect.Collector) (bool, string) {
	if c.Mode() == "fixtures" {
		return true, ""
	}
	res, err := c.RunTimeout(ctx, configTestTimeout, Client, "-t")
	if err != nil {
		return false, err.Error()
	}
	return res.OK(), errorLines(res.Output())
}

func testCopy(ctx context.Context, c collect.Collector, root string, changes map[string]string) (bool, string) {
	tmp, err := os.MkdirTemp("", "nkt-f2b-test-")
	if err != nil {
		return false, err.Error()
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(c, root, tmp); err != nil {
		return false, err.Error()
	}
	for rel, content := range changes {
		rel = path.Clean("/" + rel)[1:]
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return false, err.Error()
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return false, err.Error()
		}
	}
	res, err := c.RunTimeout(ctx, configTestTimeout, Client, "-c", tmp, "-t")
	if err != nil {
		return false, err.Error()
	}
	return res.OK(), errorLines(strings.ReplaceAll(res.Output(), tmp, root))
}

// copyTree — каталог настроек целиком (только обычные файлы и каталоги).
func copyTree(c collect.Collector, src, dst string) error {
	entries, err := c.ListDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := path.Base(e.Path)
		to := filepath.Join(dst, name)
		if e.IsDir {
			if err := os.MkdirAll(to, 0o755); err != nil {
				return err
			}
			if err := copyTree(c, e.Path, to); err != nil {
				return err
			}
			continue
		}
		raw, err := c.ReadFile(e.Path)
		if err != nil {
			continue
		}
		if err := os.WriteFile(to, raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// errorLines — строки с ошибками (остальное у fail2ban -t — шум).
func errorLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "ERROR") || strings.Contains(l, "Error") {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	if len(keep) == 0 {
		return strings.TrimSpace(out)
	}
	if len(keep) > 20 {
		keep = keep[:20]
	}
	return strings.Join(keep, "\n")
}

// UnbanIP — снять бан адреса во всех джейлах.
func UnbanIP(ctx context.Context, c collect.Collector, ip string) error {
	_, err := run(ctx, c, "unban", ip)
	return err
}

// BannedIn — джейлы, где адрес сейчас забанен.
func BannedIn(st *model.Fail2banState, ip string) []string {
	var out []string
	for _, j := range st.Jails {
		for _, b := range j.Bans {
			if b.IP == ip {
				out = append(out, j.Name)
				break
			}
		}
	}
	return out
}

// RunningJails — имена запущенных джейлов.
func RunningJails(st *model.Fail2banState) []string {
	out := make([]string, 0, len(st.Jails))
	for _, j := range st.Jails {
		out = append(out, j.Name)
	}
	return out
}
