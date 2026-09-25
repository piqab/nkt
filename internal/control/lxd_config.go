package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// LXDConfigPath — путь конфигурации инстанса в истории версий: у LXD
// это не файл, а объект демона, и история живёт под своим «адресом».
func LXDConfigPath(name string) string { return "lxd://" + name }

// LXDConfig — конфигурация инстанса в том виде, что показывает
// `lxc config show` (её же правит `lxc config edit`).
type LXDConfig struct {
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
	// Expanded — с учётом профилей, только для чтения.
	Expanded string `json:"expanded,omitempty"`
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ReadConfig — `lxc config show NAME` и `--expanded`.
func (m *LXDManager) ReadConfig(ctx context.Context, name string) (LXDConfig, error) {
	if !validLXDInstance(name) {
		return LXDConfig{}, msgs.Errorf("control.invalidInstanceName", name)
	}
	res, err := m.c.Run(ctx, "lxc", "config", "show", name)
	if err != nil {
		return LXDConfig{}, fmt.Errorf("lxc config show: %w", err)
	}
	if !res.OK() {
		return LXDConfig{}, msgs.Errorf("control.lxcCode", "config show", name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	out := LXDConfig{Content: res.Stdout, SHA256: sha(res.Stdout)}
	if ex, err := m.c.Run(ctx, "lxc", "config", "show", name, "--expanded"); err == nil && ex.OK() {
		out.Expanded = ex.Stdout
	}
	return out, nil
}

// lxdConfigBody — YAML из редактора в тело PUT /1.0/instances/NAME.
// Разбирается здесь, а не в lxc: ошибка синтаксиса видна до записи, и
// запись идёт аргументом lxc query без stdin и временных файлов.
func lxdConfigBody(content string) (string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", msgs.Errorf("control.lxdConfigYAML", err)
	}
	if doc == nil {
		return "", msgs.Errorf("control.lxdConfigYAML", errors.New("empty document"))
	}
	// Значения config в LXD — только строки: «limits.cpu: 2» без кавычек
	// YAML читает числом, lxc config edit такое тоже принимает.
	if cfg, ok := doc["config"].(map[string]any); ok {
		for k, v := range cfg {
			if v == nil {
				cfg[k] = ""
			} else if _, isStr := v.(string); !isStr {
				cfg[k] = fmt.Sprint(v)
			}
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", msgs.Errorf("control.lxdConfigYAML", err)
	}
	return string(b), nil
}

// WriteConfig записывает конфигурацию целиком (как lxc config edit).
// expected — sha256 текста, с которого начиналась правка: если его
// успели поменять, запись отклоняется. Версии — в истории конфигураций.
func (m *LXDManager) WriteConfig(ctx context.Context, cm *ConfigManager, user, name, content, note, expected string) (int64, error) {
	cur, err := m.ReadConfig(ctx, name)
	if err != nil {
		return 0, err
	}
	if expected != "" && expected != cur.SHA256 {
		return 0, ErrLXDConfigStale
	}
	body, err := lxdConfigBody(content)
	if err != nil {
		return 0, err
	}
	path := LXDConfigPath(name)
	if cm != nil {
		cm.recordBaseline(ctx, path, "lxd", user, []byte(cur.Content))
	}
	res, err := m.c.Run(ctx, "lxc", "query", "-X", "PUT", "--data", body, "/1.0/instances/"+name)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "lxd.config", name, outcome, map[string]any{
		"exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated, "note": note,
	})
	if err != nil {
		return 0, fmt.Errorf("lxc query PUT: %w", err)
	}
	if !res.OK() {
		return 0, msgs.Errorf("control.lxcCode", "config edit", name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	if cm == nil {
		return 0, nil
	}
	// В историю — то, что демон реально хранит после записи (LXD
	// нормализует порядок и кавычки); при симуляции — присланный текст.
	saved := content
	if after, err := m.ReadConfig(ctx, name); err == nil && !res.Simulated {
		saved = after.Content
	}
	return cm.recordVersion(ctx, path, "lxd", user, store.ActionEdit, note, []byte(saved))
}

// ErrLXDConfigStale — конфигурацию поменяли, пока шла правка.
var ErrLXDConfigStale = errors.New("lxd config changed since it was loaded")

// recordBaseline — при первой правке документа не-файла сохраняет
// исходное состояние, как snapshotCurrent для файлов.
func (m *ConfigManager) recordBaseline(ctx context.Context, path, service, user string, raw []byte) {
	if _, err := m.db.LatestVersion(ctx, path); errors.Is(err, store.ErrNotFound) {
		_, _ = m.recordVersion(ctx, path, service, user, store.ActionObserved,
			msgs.Tc(ctx, "control.stateBeforeFirstEdit"), raw)
	}
}

// AttachLXD — версии конфигураций инстансов LXD (lxd://имя): дифф с
// текущей и откат идут через lxc, а не через файл.
func (m *ConfigManager) AttachLXD(l *LXDManager) { m.lxd = l }

func lxdPathName(path string) (string, bool) {
	name, ok := strings.CutPrefix(path, "lxd://")
	return name, ok && validLXDInstance(name)
}
