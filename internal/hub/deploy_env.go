package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// История .env compose-стека конвейера. Каждая смена в «Секретах» — версия
// (зашифрована, как сам .env). Разница между версиями — только по именам
// переменных: значения — секреты; показать значения можно отдельно,
// администратору, с записью в журнал действий. Выкладка помнит, с какой
// версией шла, и откат по галочке возвращает её.

// envSHA — sha256 содержимого .env (сравнение с тем, что лежит на хосте).
func envSHA(env string) string {
	sum := sha256.Sum256([]byte(env))
	return hex.EncodeToString(sum[:])
}

// envVars — переменные .env: имя → значение («export» и комментарии
// пропускаются, как у docker compose).
func envVars(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, _ := strings.Cut(line, "=")
		if k = strings.TrimSpace(k); k != "" {
			out[k] = v
		}
	}
	return out
}

// envValues — переменные .env со значениями, как их видит compose:
// кавычки вокруг значения снимаются.
func envValues(env *string) map[string]string {
	if env == nil {
		return nil
	}
	out := envVars(*env)
	for k, v := range out {
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out
}

// envNameDiff — какие имена появились, пропали и чьи значения сменились.
func envNameDiff(old, cur map[string]string) (added, removed, changed []string) {
	for k, v := range cur {
		if ov, ok := old[k]; !ok {
			added = append(added, k)
		} else if ov != v {
			changed = append(changed, k)
		}
	}
	for k := range old {
		if _, ok := cur[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return
}

// ensureEnvVersion — номер текущей версии .env конвейера. .env,
// заданный до появления истории, становится её первой версией.
func (s *Server) ensureEnvVersion(ctx context.Context, pl store.Pipeline) int64 {
	if id := s.db.LatestEnvVersion(ctx, pl.ID); id > 0 || len(pl.EnvEnc) == 0 {
		return id
	}
	id, _ := s.db.AddEnvVersion(ctx, store.EnvVersion{PipelineID: pl.ID, Author: pl.Author,
		Note: msgs.Tc(ctx, "deploy.envVersionInitial"), EnvEnc: pl.EnvEnc})
	return id
}

// setPipelineEnv — новый .env конвейера (nil — убрать) и его версия.
func (s *Server) setPipelineEnv(ctx context.Context, pl store.Pipeline, user, note string, env *string) error {
	var enc []byte
	if env != nil {
		var err error
		if enc, err = secretbox.Encrypt(s.hub.key, []byte(*env)); err != nil {
			return err
		}
	}
	s.ensureEnvVersion(ctx, pl)
	if err := s.db.SetPipelineEnv(ctx, pl.ID, enc); err != nil {
		return err
	}
	_, err := s.db.AddEnvVersion(ctx, store.EnvVersion{PipelineID: pl.ID, Author: user, Note: note, EnvEnc: enc})
	return err
}

// envVersionJSON — версия .env для интерфейса: без значений.
type envVersionJSON struct {
	ID      int64    `json:"id"`
	TS      string   `json:"ts"`
	Author  string   `json:"author,omitempty"`
	Note    string   `json:"note,omitempty"`
	Cleared bool     `json:"cleared,omitempty"`
	Names   []string `json:"names"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Current bool     `json:"current,omitempty"`
}

func (s *Server) decryptEnv(enc []byte) (map[string]string, bool) {
	if len(enc) == 0 {
		return map[string]string{}, false
	}
	raw, err := secretbox.Decrypt(s.hub.key, enc)
	if err != nil {
		return map[string]string{}, false
	}
	return envVars(string(raw)), true
}

// handlePipelineEnvVersions — GET /hub/pipelines/{id}/env/versions.
func (s *Server) handlePipelineEnvVersions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	s.ensureEnvVersion(ctx, p)
	list, err := s.db.EnvVersions(ctx, p.ID, 200)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	out := make([]envVersionJSON, 0, len(list))
	for i, v := range list {
		cur, has := s.decryptEnv(v.EnvEnc)
		item := envVersionJSON{ID: v.ID, TS: v.TS, Author: v.Author, Note: v.Note, Cleared: !has, Current: i == 0}
		for k := range cur {
			item.Names = append(item.Names, k)
		}
		sort.Strings(item.Names)
		old := map[string]string{}
		if i+1 < len(list) {
			old, _ = s.decryptEnv(list[i+1].EnvEnc)
		}
		item.Added, item.Removed, item.Changed = envNameDiff(old, cur)
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out})
}

func (s *Server) envVersionFromReq(w http.ResponseWriter, r *http.Request, p store.Pipeline) (store.EnvVersion, bool) {
	vid, _ := strconv.ParseInt(chi.URLParam(r, "vid"), 10, 64)
	v, err := s.db.EnvVersionByID(r.Context(), vid)
	if err != nil || v.PipelineID != p.ID {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("deploy.envVersionMissing", vid))
		return v, false
	}
	return v, true
}

// handlePipelineEnvReveal — POST /hub/pipelines/{id}/env/versions/{vid}/reveal:
// значения версии (администратору; показ — в журнал действий).
func (s *Server) handlePipelineEnvReveal(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	v, ok := s.envVersionFromReq(w, r, p)
	if !ok {
		return
	}
	content := ""
	if len(v.EnvEnc) > 0 {
		raw, err := secretbox.Decrypt(s.hub.key, v.EnvEnc)
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		content = string(raw)
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "pipeline.env.reveal", p.Name, "ok", map[string]any{"version": v.ID})
	writeJSON(w, http.StatusOK, map[string]any{"content": content, "cleared": len(v.EnvEnc) == 0})
}

// handlePipelineEnvRestore — POST /hub/pipelines/{id}/env/versions/{vid}/restore:
// сделать версию текущей (новой версией; на хосты — со следующей выкладкой).
func (s *Server) handlePipelineEnvRestore(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	v, ok := s.envVersionFromReq(w, r, p)
	if !ok {
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	err := s.restoreEnvVersion(ctx, p, v, user, msgs.Tc(ctx, "deploy.envVersionRestored", v.ID))
	s.db.Audit(ctx, user, "pipeline.env.restore", p.Name, auditOutcome(err), map[string]any{"version": v.ID})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) restoreEnvVersion(ctx context.Context, p store.Pipeline, v store.EnvVersion, user, note string) error {
	var env *string
	if len(v.EnvEnc) > 0 {
		raw, err := secretbox.Decrypt(s.hub.key, v.EnvEnc)
		if err != nil {
			return err
		}
		e := string(raw)
		env = &e
	}
	return s.setPipelineEnv(ctx, p, user, note, env)
}

// missingEnvKeys — имена из compose.env_keys, которых нет в .env конвейера
// (nil — .env не задан вовсе).
func missingEnvKeys(keys []string, env *string) []string {
	have := map[string]string{}
	if env != nil {
		have = envVars(*env)
	}
	var out []string
	for _, k := range keys {
		if v, ok := have[k]; !ok || strings.TrimSpace(v) == "" {
			out = append(out, k)
		}
	}
	return out
}

var envRefRe = regexp.MustCompile(`\$(?:\{([A-Za-z_][A-Za-z0-9_]*)|([A-Za-z_][A-Za-z0-9_]*))`)

// envForwardRefs — ссылки вперёд в .env: значение использует ${X} (или $X),
// а X задана ниже. docker compose подставляет в .env только заданное
// выше, поэтому такое значение молча получится не тем (например,
// «https://» вместо адреса). Значения в одинарных кавычках не
// подставляются и не считаются; «$$» — буквальный знак.
func envForwardRefs(text string) [][2]string {
	type line struct{ key, val string }
	var lines []line
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		l = strings.TrimPrefix(l, "export ")
		k, v, ok := strings.Cut(l, "=")
		if k = strings.TrimSpace(k); ok && k != "" {
			lines = append(lines, line{k, strings.TrimSpace(v)})
		}
	}
	defined := map[string]bool{}
	later := map[string]bool{}
	for _, l := range lines {
		later[l.key] = true
	}
	var out [][2]string
	for _, l := range lines {
		if !strings.HasPrefix(l.val, "'") {
			for _, m := range envRefRe.FindAllStringSubmatchIndex(strings.ReplaceAll(l.val, "$$", "  "), -1) {
				v := strings.ReplaceAll(l.val, "$$", "  ")
				name := ""
				if m[2] >= 0 {
					name = v[m[2]:m[3]]
				} else {
					name = v[m[4]:m[5]]
				}
				if name != l.key && !defined[name] && later[name] && !slices.ContainsFunc(out, func(p [2]string) bool { return p == [2]string{l.key, name} }) {
					out = append(out, [2]string{l.key, name})
				}
			}
		}
		defined[l.key] = true
	}
	return out
}

// usesDotEnvFile — какой-то сервис подключает .env целиком (env_file:
// строкой, списком строк или списком {path: …}): тогда в контейнер идут
// все переменные.
func usesDotEnvFile(composeText string) bool {
	var doc struct {
		Services map[string]struct {
			EnvFile any `yaml:"env_file"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(composeText), &doc) != nil {
		return false
	}
	isDotEnv := func(v any) bool {
		p, _ := v.(string)
		if m, ok := v.(map[string]any); ok {
			p, _ = m["path"].(string)
		}
		return path.Clean(strings.TrimSpace(p)) == ".env"
	}
	for _, svc := range doc.Services {
		switch v := svc.EnvFile.(type) {
		case string:
			if isDotEnv(v) {
				return true
			}
		case []any:
			for _, it := range v {
				if isDotEnv(it) {
					return true
				}
			}
		}
	}
	return false
}

// envUnused — переменные .env, которые не попадут ни в один контейнер:
// docker compose берёт .env только для подстановки ${ИМЯ} в сам файл, а
// не передаёт его в контейнеры. Нет ни ссылки на имя, ни env_file с .env —
// значение никто не увидит. Служебные COMPOSE_* и DOCKER_* — для самого
// compose, их не считаем.
func envUnused(env, composeText string) []string {
	if usesDotEnvFile(composeText) {
		return nil
	}
	var out []string
	for name := range envVars(env) {
		if strings.HasPrefix(name, "COMPOSE_") || strings.HasPrefix(name, "DOCKER_") {
			continue
		}
		if regexp.MustCompile(`\$\{?` + regexp.QuoteMeta(name) + `\b`).MatchString(composeText) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
