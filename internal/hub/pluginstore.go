package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/plugin"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Секреты, которые создаёт плагин (ключ API LiteLLM): показать человеку
// один раз (Response.Reveal — только окну браузера) или сразу положить в
// .env конвейера (Response.Store — пишет хаб, проверив права того, кто
// вызвал). Значение не попадает ни в журнал действий, ни в журнал
// задания, ни к токену.

// pluginStoreEnvRe — имя переменной .env.
var pluginStoreEnvRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// pluginStoreTarget — куда положить секрет: аргументы операции
// to_pipeline и env.
func pluginStoreTarget(args map[string]any) (pipeline, env string) {
	p, _ := args["to_pipeline"].(string)
	e, _ := args["env"].(string)
	return strings.TrimSpace(p), strings.TrimSpace(e)
}

// scriptStoreTarget — то же в команде сценария: «to-pipeline P env K».
func scriptStoreTarget(argv []string) (pipeline, env string) {
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "to-pipeline":
			pipeline = argv[i+1]
		case "env":
			env = argv[i+1]
		}
	}
	return pipeline, env
}

// pluginStoreCheck — можно ли тому, кто вызывает (ctx), положить секрет в
// .env этого конвейера: администратор; токен — с правом править
// конвейеры, и конвейер — в его пределах (свой или в списке).
func (s *Server) pluginStoreCheck(ctx context.Context, author, pipeline, env string) (store.Pipeline, error) {
	if !pluginStoreEnvRe.MatchString(env) && env != plugin.StoreRegistry {
		return store.Pipeline{}, msgs.Errorf("plugin.storeBadEnv", env)
	}
	list, err := s.db.ListPipelines(ctx)
	if err != nil {
		return store.Pipeline{}, err
	}
	i := slices.IndexFunc(list, func(p store.Pipeline) bool { return p.Name == pipeline })
	if i < 0 {
		return store.Pipeline{}, msgs.Errorf("plugin.storeNoPipeline", pipeline)
	}
	p := list[i]
	t, isTok := tokenFromContext(ctx)
	if !isTok && strings.HasPrefix(author, "token:") {
		// Сценарий, запущенный токеном: права — его.
		toks, _ := s.db.ListAPITokens(ctx)
		if j := slices.IndexFunc(toks, func(x store.APIToken) bool { return "token:"+x.Name == author }); j >= 0 {
			t, isTok = toks[j], true
		} else {
			return p, msgs.Errorf("auth.adminOnly")
		}
	}
	if isTok {
		if !t.Can(store.TokenActPipelineCreate) {
			return p, tokenDeny(http.StatusForbidden, "auth.tokenActionDenied", store.TokenActPipelineCreate)
		}
		if t.Limited() && !t.Owns(p.Author) && !slices.Contains(t.Perms.Pipelines, p.Name) {
			return p, tokenDeny(http.StatusForbidden, "auth.tokenNotOwnPipeline", p.Name)
		}
		return p, nil
	}
	if u, ok := auth.UserFromContext(ctx); ok && u.IsAdmin() && u.Username == author {
		return p, nil
	}
	if s.actingUser(ctx, author) != "" {
		return p, nil
	}
	return p, msgs.Errorf("auth.adminOnly")
}

// pluginStoreApply — записать секрет из ответа плагина в .env конвейера
// (только туда, куда просил запрос); строка для человека — без значения.
func (s *Server) pluginStoreApply(ctx context.Context, author string, plName string, p store.Pipeline, env string, st *plugin.Store) (string, error) {
	if st.Pipeline != p.Name || st.Env != env || p.Name == "" {
		return "", msgs.Errorf("plugin.storeMismatch", plName)
	}
	v := strings.TrimSpace(st.Value)
	if env == plugin.StoreRegistry {
		return s.pluginStoreRegistry(ctx, author, plName, p, st)
	}
	if v == "" || strings.ContainsAny(v, "\r\n\"'\\$`") {
		return "", msgs.Errorf("plugin.storeBadValue", env)
	}
	text := ""
	if len(p.EnvEnc) > 0 {
		raw, err := secretbox.Decrypt(s.hub.key, p.EnvEnc)
		if err != nil {
			return "", err
		}
		text = string(raw)
	}
	next := deploy.MergeEnv(text, []string{env}, map[string]string{env: v})
	if err := s.setPipelineEnv(ctx, p, author, msgs.Tc(ctx, "plugin.storeNote", plName, env), &next); err != nil {
		return "", err
	}
	s.db.Audit(ctx, author, "plugin.store", plName, "ok", map[string]any{"pipeline": p.Name, "env": env})
	return msgs.Tc(ctx, "plugin.stored", env, p.Name), nil
}

// pluginStoreRegistry — ключ registry конвейера от плагина (робот rgstr):
// ключ с тем же адресом заменяется, CA прежнего ключа остаётся.
func (s *Server) pluginStoreRegistry(ctx context.Context, author, plName string, p store.Pipeline, st *plugin.Store) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(st.Registry), "https://"), "http://"), "/"))
	user, token := strings.TrimSpace(st.User), strings.TrimSpace(st.Value)
	if !registryHostRe.MatchString(host) || user == "" || strings.ContainsAny(user, ": \t\r\n") || token == "" || strings.ContainsAny(token, "\r\n") {
		return "", msgs.Errorf("plugin.storeBadValue", plugin.StoreRegistry)
	}
	out := []registryKey{}
	ca := ""
	for _, k := range s.pipelineRegistries(p) {
		if k.Host == host {
			ca = k.CA
			continue
		}
		out = append(out, k)
	}
	out = append(out, registryKey{Host: host, User: user, Token: token, CA: ca})
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	raw, _ := json.Marshal(out)
	enc, err := secretbox.Encrypt(s.hub.key, raw)
	if err != nil {
		return "", err
	}
	if err := s.db.SetPipelineRegistries(ctx, p.ID, enc); err != nil {
		return "", err
	}
	s.db.Audit(ctx, author, "plugin.store", plName, "ok", map[string]any{"pipeline": p.Name, "registry": host, "user": user})
	return msgs.Tc(ctx, "plugin.storedRegistry", host, user, p.Name), nil
}
