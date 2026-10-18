package api

import (
	"context"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
)

// Конфиги стека из описания конвейера (compose.configs): хаб присылает
// шаблоны с ${ИМЯ}, хост подставляет значения из .env стека и пишет файл
// с правами и владельцем. Готовый файл (в нём секреты) не идёт ни в
// историю конфигов, ни в журнал; сухой прогон показывает разницу с
// секретами под маской.

// composeConfigFile — конфиг в запросе выкладки.
type composeConfigFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode"`
	UID     *int   `json:"uid,omitempty"`
	GID     *int   `json:"gid,omitempty"`
}

// validateConfigs — пути только в .nkt-configs/, разумные права и размеры.
func validateConfigs(list []composeConfigFile) error {
	if len(list) > 50 {
		return msgs.Errorf("compose.tooLarge")
	}
	seen := map[string]bool{}
	for _, cf := range list {
		if !deploy.ValidPath(cf.Path) || path.Dir(cf.Path) != deploy.ConfigsDir || seen[cf.Path] {
			return msgs.Errorf("compose.badPath", cf.Path)
		}
		seen[cf.Path] = true
		if len(cf.Content) > deploy.ConfigMaxBytes {
			return msgs.Errorf("compose.tooLarge")
		}
		if cf.Mode == 0 || cf.Mode > 0o777 {
			return msgs.Errorf("compose.badPath", cf.Path+" "+strconv.FormatUint(uint64(cf.Mode), 8))
		}
		for _, id := range []*int{cf.UID, cf.GID} {
			if id != nil && (*id < 0 || *id > 65535) {
				return msgs.Errorf("compose.badPath", cf.Path)
			}
		}
	}
	return nil
}

// configEnv — значения для подстановки: присланный .env или тот, что
// уже лежит в стеке.
func configEnv(c collect.Collector, dir string, env *string) map[string]string {
	if env != nil {
		return deploy.ParseEnv(*env)
	}
	if raw, err := c.ReadFile(dir + "/.env"); err == nil {
		return deploy.ParseEnv(string(raw))
	}
	return map[string]string{}
}

// writeConfigs — готовые конфиги в стек: write — запись с откатом
// (как у файлов стека), затем владелец.
func writeConfigs(ctx context.Context, c collect.Collector, dir string, list []composeConfigFile, vals map[string]string, write func(string, []byte, fs.FileMode) error) error {
	for _, cf := range list {
		p := dir + "/" + cf.Path
		if err := write(p, []byte(deploy.RenderConfig(cf.Content, vals)), fs.FileMode(cf.Mode)); err != nil {
			return err
		}
		if cf.UID == nil && cf.GID == nil {
			continue
		}
		owner := ""
		if cf.UID != nil {
			owner = strconv.Itoa(*cf.UID)
		}
		if cf.GID != nil {
			owner += ":" + strconv.Itoa(*cf.GID)
		}
		res, err := c.RunTimeout(ctx, 30*time.Second, "chown", "--", owner, p)
		if err == nil && !res.OK() {
			err = msgs.Errorf("compose.configChown", cf.Path, strings.TrimSpace(res.Output()))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ComposeConfigChange — конфиг в сухом прогоне: new, changed, same и
// разница (секреты — «••••»).
type ComposeConfigChange struct {
	Path  string `json:"path"`
	State string `json:"state"`
	Diff  string `json:"diff,omitempty"`
}

// checkConfigs — что станет с конфигами стека.
func checkConfigs(ctx context.Context, c collect.Collector, dir string, list []composeConfigFile, vals map[string]string) []ComposeConfigChange {
	var out []ComposeConfigChange
	for _, cf := range list {
		next := deploy.RenderConfig(cf.Content, vals)
		ch := ComposeConfigChange{Path: cf.Path, State: "new"}
		old, err := c.ReadFile(dir + "/" + cf.Path)
		if err == nil {
			ch.State = "same"
			if string(old) != next {
				ch.State = "changed"
				ch.Diff = control.UnifiedDiff(ctx, cf.Path, cf.Path, deploy.MaskSecrets(string(old), vals), deploy.MaskSecrets(next, vals))
			}
		}
		out = append(out, ch)
	}
	return out
}
