package hub

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
)

// Блок env описания конвейера (deploy/compose_env.go): при сохранении
// значения уходят в зашифрованный .env конвейера, в описании остаётся
// «@secret». В базу (описание и его версии), журнал действий и параметры
// заданий значения не попадают.

// envMove — что делает сохранение с блоком env.
type envMove struct {
	// Content — описание без значений (то, что ляжет в базу).
	Content string
	// Env — новый .env конвейера (nil — не меняется).
	Env *string
	// Moved / Generated / Skipped — имена: перенесены значением, созданы
	// хабом, пропущены (generate: у уже заданного — пароль базы не
	// меняется молча).
	Moved, Generated, Skipped []string
	// Conflicts — уже заданы другим значением: замена только с
	// подтверждения (replace).
	Conflicts []string
}

// planEnvMove — перенос значений блока env в .env (cur — нынешний .env
// конвейера, nil — нет). Ошибка — блок записан не так.
func planEnvMove(content string, cur *string, replace bool) (envMove, error) {
	m := envMove{Content: content}
	entries, err := deploy.EnvBlock(content)
	if err != nil || !deploy.HasEnvValues(entries) {
		return m, err
	}
	have := envValues(cur)
	set := map[string]string{}
	var order []string
	for _, e := range entries {
		if e.Kept() {
			continue
		}
		old, exists := have[e.Key]
		exists = exists && strings.TrimSpace(old) != ""
		if g := e.Generate(); g != "" {
			if exists {
				m.Skipped = append(m.Skipped, e.Key)
				continue
			}
			set[e.Key] = deploy.GenerateEnvValue(g)
			m.Generated = append(m.Generated, e.Key)
		} else {
			if exists && old == e.Value {
				continue
			}
			if exists && !replace {
				m.Conflicts = append(m.Conflicts, e.Key)
				continue
			}
			set[e.Key] = e.Value
			m.Moved = append(m.Moved, e.Key)
		}
		order = append(order, e.Key)
	}
	m.Content = deploy.StripEnvValues(content, entries)
	if len(set) > 0 {
		text := ""
		if cur != nil {
			text = *cur
		}
		next := deploy.MergeEnv(text, order, set)
		m.Env = &next
	}
	return m, nil
}

// envMoveNote — пометка к версии .env: какие имена пришли из описания.
func envMoveNote(ctx context.Context, m envMove) string {
	names := append(append([]string{}, m.Moved...), m.Generated...)
	sort.Strings(names)
	return msgs.Tc(ctx, "deploy.envFromSpec", strings.Join(names, ", "))
}

// writeEnvConflict — 409: имена уже заданы другим значением; интерфейс
// спрашивает и повторяет запрос с replace_env.
func writeEnvConflict(w http.ResponseWriter, r *http.Request, keys []string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":        msgs.Tc(r.Context(), "deploy.envConflict", strings.Join(keys, ", ")),
		"env_conflict": keys,
	})
}

// envMoveReply — что сказать интерфейсу после сохранения.
func envMoveReply(out map[string]any, m envMove, spec deploy.Spec) {
	if len(m.Moved)+len(m.Generated)+len(m.Skipped) > 0 {
		out["content"] = m.Content
		out["env_moved"] = m.Moved
		out["env_generated"] = m.Generated
		out["env_skipped"] = m.Skipped
	}
	if spec.Compose != nil {
		if hints := spec.Compose.ConfigSecretHints(); len(hints) > 0 {
			out["config_secret_hints"] = hints
		}
	}
}

// dryEnvOverride — .env для сухого прогона с несохранённым блоком env:
// нынешний .env и значения из описания (generate: — временные). Текст
// уходит в одноразовое хранилище памяти; в параметры задания — жетон и
// описание без значений.
func (s *Server) dryEnvOverride(ctx context.Context, pipelineID int64, content string) (string, string, []string, error) {
	entries, err := deploy.EnvBlock(content)
	if err != nil || !deploy.HasEnvValues(entries) {
		return content, "", nil, err
	}
	var cur *string
	if pipelineID > 0 {
		if pl, err := s.db.PipelineByID(ctx, pipelineID); err == nil {
			cur, _ = s.pipelineEnv(pl)
		}
	}
	have := envValues(cur)
	set := map[string]string{}
	var order, names []string
	for _, e := range entries {
		if e.Kept() {
			continue
		}
		if g := e.Generate(); g != "" {
			if strings.TrimSpace(have[e.Key]) != "" {
				continue
			}
			set[e.Key] = deploy.GenerateEnvValue(g)
		} else {
			set[e.Key] = e.Value
		}
		order = append(order, e.Key)
		names = append(names, e.Key)
	}
	text := ""
	if cur != nil {
		text = *cur
	}
	tok := oneTime.Put([]byte(deploy.MergeEnv(text, order, set)))
	return deploy.StripEnvValues(content, entries), tok, names, nil
}

// requiredEnv — имена, без которых стек не выложить: env_keys, блок env
// и ${ИМЯ} в конфигах.
func requiredEnv(c *deploy.ComposeSpec, cfgs []deploy.ConfigFile) []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range c.AllEnvKeys() {
		add(k)
	}
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add(k)
	}
	for _, cf := range cfgs {
		for _, k := range deploy.ConfigVars(cf.Content) {
			add(k)
		}
	}
	return out
}

// composeConfigs — конфиги описания: шаблоны из описания и репозитория
// ({{nkt.…}} подставлены) и тома с метками в копии compose-файла.
func composeConfigs(src string, c *deploy.ComposeSpec, vars deploy.Vars, files map[string]string, main string) ([]deploy.ConfigFile, error) {
	if len(c.Configs) == 0 {
		return nil, nil
	}
	cfgs, err := c.ConfigFiles(func(p string) (string, error) {
		t, err := deploy.ReadFile(src, p)
		return vars.Substitute(t), err
	})
	if err != nil {
		return nil, err
	}
	for i := range cfgs {
		cfgs[i].Content = vars.Substitute(cfgs[i].Content)
	}
	out, err := deploy.MountConfigs(files[main], cfgs)
	if err != nil {
		return nil, err
	}
	files[main] = out
	return cfgs, nil
}

// hostConfigsError — хост не знает конфигов (старый nkt): понятная
// причина вместо «unknown field».
func hostConfigsError(err error) error {
	if err != nil && strings.Contains(msgs.Localize(msgs.EN, err), "unknown field") && strings.Contains(msgs.Localize(msgs.EN, err), "configs") {
		return msgs.Errorf("deploy.configsHostOld")
	}
	return err
}

// pipelineEnvAfterSave — записать .env, пришедший из описания.
func (s *Server) pipelineEnvAfterSave(ctx context.Context, id int64, user string, m envMove) error {
	if m.Env == nil {
		return nil
	}
	pl, err := s.db.PipelineByID(ctx, id)
	if err != nil {
		return err
	}
	return s.setPipelineEnv(ctx, pl, user, envMoveNote(ctx, m), m.Env)
}

// envAuditDetail — журнал действий: только имена из блока env.
func envAuditDetail(m envMove) any {
	if len(m.Moved)+len(m.Generated)+len(m.Skipped) == 0 {
		return ""
	}
	return map[string]any{"env_moved": m.Moved, "env_generated": m.Generated, "env_skipped": m.Skipped}
}

// envAuditNote — пометка правки и имена из блока env.
func envAuditNote(note string, m envMove) any {
	if d := envAuditDetail(m); d != "" {
		return map[string]any{"note": note, "env": d}
	}
	return note
}
