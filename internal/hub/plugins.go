package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/plugin"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Внешние плагины («О системе» → «Плагины», протокол — internal/plugin).
// Плагин слушает 127.0.0.1:<порт> на своём хосте; хаб ходит к нему через
// SSH-соединение с хостом (машина хаба — напрямую) и подписывает вызовы
// секретом. Подключение — с конвейера, который выложил плагин: хост —
// хост конвейера, секрет и порт — NKT_PLUGIN_SECRET и NKT_PLUGIN_PORT из
// его .env. Без подключённых плагинов хаб работает как прежде.

const (
	pluginsKV = "plugins"
	// pluginSecretEnv / pluginPortEnv — ключи .env конвейера плагина.
	pluginSecretEnv = "NKT_PLUGIN_SECRET" // #nosec G101 -- имя переменной .env, а не секрет
	pluginPortEnv   = "NKT_PLUGIN_PORT"
	// pluginDefaultPort — порт плагина по умолчанию.
	pluginDefaultPort = 9180
	// pluginActPrefix — действие токена «по правам»: plugin.<имя>.
	pluginActPrefix = "plugin."
)

// pluginConf — подключённый плагин.
type pluginConf struct {
	// Name — имя в хабе (по умолчанию — из манифеста): инструменты MCP
	// <name>_<операция>, действие токена plugin.<name>.
	Name string `json:"name"`
	// Host — имя хоста (localhost — машина хаба): по имени, чтобы
	// пережить экспорт и импорт.
	Host      string `json:"host"`
	Port      int    `json:"port"`
	SecretEnc []byte `json:"secret_enc"`
	Pipeline  string `json:"pipeline,omitempty"`
	Title     string `json:"title,omitempty"`
	Version   string `json:"version,omitempty"`
	// Token — токен хаба, выпущенный плагину (hub_actions).
	Token string `json:"token,omitempty"`
	// Words — слова команд сценариев (из манифеста при подключении:
	// разбор сценариев не ходит к плагину).
	Words []string `json:"words,omitempty"`
	// Native — поставлен хабом службой на хост (без Docker): из какого
	// пакета встроенного каталога — для «Обновить» и «Удалить с хоста».
	Native string `json:"native,omitempty"`
	// NativeSHA — отпечаток поставленного файла плагина.
	NativeSHA string `json:"native_sha,omitempty"`
	Author    string `json:"author,omitempty"`
	AddedAt   string `json:"added_at,omitempty"`
}

var pluginsMu sync.Mutex

func (s *Server) plugins(ctx context.Context) []pluginConf {
	var list []pluginConf
	if raw, ok, err := s.db.KVGet(ctx, pluginsKV); err == nil && ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	return list
}

func (s *Server) savePlugins(ctx context.Context, list []pluginConf) error {
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.db.KVSet(ctx, pluginsKV, string(raw))
}

func (s *Server) pluginByName(ctx context.Context, name string) (pluginConf, bool) {
	for _, p := range s.plugins(ctx) {
		if p.Name == name {
			return p, true
		}
	}
	return pluginConf{}, false
}

// pluginDial — соединение с 127.0.0.1:<порт> хоста плагина.
func (s *Server) pluginDial(host string, port int) plugin.Dial {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return func(ctx context.Context) (net.Conn, error) {
		if host == localHostName {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
		}
		targets, err := s.resolveHosts(ctx, []string{host}, "")
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			return nil, msgs.Errorf("plugin.hostGone", host)
		}
		client, err := s.hub.clientFor(ctx, targets[0].ID)
		if err != nil {
			// Через туннель хоста (без SSH) до порта плагина не дойти.
			return nil, msgs.Errorf("plugin.noSSH", host, err)
		}
		return client.Dial("tcp", addr)
	}
}

func (s *Server) pluginClient(ctx context.Context, p pluginConf) (plugin.Client, error) {
	secret, err := secretbox.Decrypt(s.hub.key, p.SecretEnc)
	if err != nil {
		return plugin.Client{}, err
	}
	return plugin.Client{Dial: s.pluginDial(p.Host, p.Port), Secret: string(secret), Lang: string(msgs.FromContext(ctx))}, nil
}

// Манифесты — в памяти на минуту: раздел, MCP и сценарии спрашивают
// часто, а меняется манифест с новой версией плагина.
type pluginManifestEntry struct {
	m   plugin.Manifest
	err error
	at  time.Time
}

var (
	manifestMu    sync.Mutex
	manifestCache = map[string]pluginManifestEntry{}
)

const manifestTTL = time.Minute

func forgetManifests(name string) {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	for k := range manifestCache {
		if strings.HasPrefix(k, name+"|") {
			delete(manifestCache, k)
		}
	}
}

// pluginManifest — манифест на языке контекста (кэш на минуту, ошибка —
// на 10 секунд).
func (s *Server) pluginManifest(ctx context.Context, p pluginConf) (plugin.Manifest, error) {
	key := p.Name + "|" + string(msgs.FromContext(ctx))
	manifestMu.Lock()
	e, ok := manifestCache[key]
	manifestMu.Unlock()
	if ok && (e.err == nil && time.Since(e.at) < manifestTTL || e.err != nil && time.Since(e.at) < 10*time.Second) {
		return e.m, e.err
	}
	c, err := s.pluginClient(ctx, p)
	var m plugin.Manifest
	if err == nil {
		c.Timeout = 15 * time.Second
		m, err = c.Manifest(ctx)
	}
	manifestMu.Lock()
	manifestCache[key] = pluginManifestEntry{m: m, err: err, at: time.Now()}
	manifestMu.Unlock()
	return m, err
}

// pluginCall — вызов операции от имени текущего пользователя.
func (s *Server) pluginCall(ctx context.Context, p pluginConf, req plugin.Request) (plugin.Response, error) {
	c, err := s.pluginClient(ctx, p)
	if err != nil {
		return plugin.Response{}, err
	}
	req.User = auth.Username(ctx)
	return c.Call(ctx, req)
}

// pluginOpAllowed — операция доступна: только чтение — всем, кто вошёл;
// прочие — администратору или токену с действием plugin.<имя>.
func (s *Server) pluginOpAllowed(ctx context.Context, p pluginConf, op plugin.Op) error {
	if op.ReadOnly {
		return nil
	}
	if t, ok := tokenFromContext(ctx); ok {
		if t.Can(pluginActPrefix + p.Name) {
			return nil
		}
		return tokenDeny(http.StatusForbidden, "auth.tokenActionDenied", pluginActPrefix+p.Name)
	}
	if u, ok := auth.UserFromContext(ctx); ok && u.IsAdmin() {
		return nil
	}
	return msgs.Errorf("auth.adminOnly")
}

// pluginError — ответ об ошибке плагина: его собственная — 400 с его
// текстом, нет связи — 502.
func pluginError(w http.ResponseWriter, r *http.Request, p pluginConf, err error) {
	var ce *plugin.CallError
	if errors.As(err, &ce) {
		writeError(w, http.StatusBadRequest, ce.Msg)
		return
	}
	writeError(w, http.StatusBadGateway, msgs.Tc(r.Context(), "plugin.unreachable", p.Name, p.Host, p.Port, err))
}

// pluginJSON — плагин в списке: подключение и манифест (или ошибка).
type pluginJSON struct {
	Name     string           `json:"name"`
	Host     string           `json:"host"`
	Port     int              `json:"port"`
	Pipeline string           `json:"pipeline,omitempty"`
	Title    string           `json:"title"`
	Version  string           `json:"version,omitempty"`
	Token    string           `json:"token,omitempty"`
	Native   string           `json:"native,omitempty"`
	Outdated bool             `json:"outdated,omitempty"`
	Author   string           `json:"author,omitempty"`
	AddedAt  string           `json:"added_at,omitempty"`
	Error    string           `json:"error,omitempty"`
	Manifest *plugin.Manifest `json:"manifest,omitempty"`
}

// handlePlugins — GET /hub/plugins: подключённые плагины с манифестами.
func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	list := s.plugins(r.Context())
	out := make([]pluginJSON, len(list))
	var wg sync.WaitGroup
	for i, p := range list {
		out[i] = pluginJSON{Name: p.Name, Host: p.Host, Port: p.Port, Pipeline: p.Pipeline, Title: p.Title, Version: p.Version,
			Token: p.Token, Native: p.Native, Outdated: nativeOutdated(p), Author: p.Author, AddedAt: p.AddedAt}
		wg.Add(1)
		go func(i int, p pluginConf) {
			defer wg.Done()
			m, err := s.pluginManifest(r.Context(), p)
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].Manifest = &m
			out[i].Title, out[i].Version = m.Title, m.Version
		}(i, p)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"plugins": out})
}

// pluginConnectReq — подключение: с конвейера (хост, секрет и порт — из
// него) или вручную.
type pluginConnectReq struct {
	Name       string `json:"name"`
	PipelineID int64  `json:"pipeline_id"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Secret     string `json:"secret"`
	// HubURL — адрес хаба для плагина, если он просит права (hub_actions).
	HubURL string `json:"hub_url"`
	// GrantHub — администратор согласился выдать плагину hub_actions.
	GrantHub bool `json:"grant_hub"`
}

// resolveConnect — хост, порт и секрет подключения.
func (s *Server) resolveConnect(ctx context.Context, req *pluginConnectReq) (pluginConf, string, error) {
	conf := pluginConf{Host: strings.TrimSpace(req.Host), Port: req.Port}
	secret := req.Secret
	if req.PipelineID > 0 {
		pl, err := s.db.PipelineByID(ctx, req.PipelineID)
		if err != nil {
			return conf, "", err
		}
		targets, ok := s.pipelineTargets(ctx, pl)
		if !ok {
			return conf, "", msgs.Errorf("plugin.pipelineNotCompose", pl.Name)
		}
		env, err := s.pipelineEnv(pl)
		if err != nil {
			return conf, "", err
		}
		vals := envValues(env)
		if secret == "" {
			secret = vals[pluginSecretEnv]
		}
		if conf.Port == 0 {
			conf.Port, _ = strconv.Atoi(vals[pluginPortEnv])
		}
		if conf.Host == "" {
			conf.Host = targets[0].Name
		}
		conf.Pipeline = pl.Name
	}
	if conf.Port == 0 {
		conf.Port = pluginDefaultPort
	}
	if conf.Host == "" {
		return conf, "", msgs.Errorf("plugin.needHost")
	}
	if conf.Port < 1 || conf.Port > 65535 {
		return conf, "", msgs.Errorf("plugin.badPort", conf.Port)
	}
	if conf.Host != localHostName {
		if targets, err := s.resolveHosts(ctx, []string{conf.Host}, ""); err != nil || len(targets) == 0 {
			return conf, "", msgs.Errorf("plugin.hostGone", conf.Host)
		}
	}
	if len(secret) < 16 {
		return conf, "", msgs.Errorf("plugin.needSecret", pluginSecretEnv)
	}
	return conf, secret, nil
}

// handlePluginProbe — POST /hub/plugins/probe: манифест до подключения
// (что плагин умеет и какие права просит).
func (s *Server) handlePluginProbe(w http.ResponseWriter, r *http.Request) {
	var req pluginConnectReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	conf, secret, err := s.resolveConnect(r.Context(), &req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	c := plugin.Client{Dial: s.pluginDial(conf.Host, conf.Port), Secret: secret, Lang: string(msgs.FromContext(r.Context())), Timeout: 15 * time.Second}
	m, err := c.Manifest(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, msgs.Tc(r.Context(), "plugin.unreachable", m.Name, conf.Host, conf.Port, err))
		return
	}
	conflicts, warnings := s.pluginConflicts(r.Context(), firstNonEmpty(req.Name, m.Name), m)
	writeJSON(w, http.StatusOK, map[string]any{"host": conf.Host, "port": conf.Port, "pipeline": conf.Pipeline, "manifest": m,
		"conflicts": conflicts, "warnings": warnings})
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return b
}

// pluginConflicts — что помешает подключению: имя занято, инструмент MCP
// или слово сценария уже есть.
// Блокирует подключение — имя или инструмент MCP заняты (out);
// слово сценария занято (warn) — не блокирует: слово остаётся за первым
// плагином, у второго команда пишется «plugin ИМЯ СЛОВО …» (второй
// экземпляр того же плагина).
func (s *Server) pluginConflicts(ctx context.Context, name string, m plugin.Manifest) (out, warn []string) {
	others := s.plugins(ctx)
	for _, o := range others {
		if o.Name == name {
			out = append(out, msgs.Tc(ctx, "plugin.nameTaken", name))
			continue
		}
		om, err := s.pluginManifest(ctx, o)
		if err != nil {
			continue
		}
		for _, c := range m.Commands {
			if slices.ContainsFunc(om.Commands, func(x plugin.Command) bool { return x.Word == c.Word }) {
				warn = append(warn, msgs.Tc(ctx, "plugin.wordTaken", c.Word, o.Name))
			}
		}
	}
	for _, c := range m.Commands {
		if scriptBuiltinWord(c.Word) {
			warn = append(warn, msgs.Tc(ctx, "plugin.wordBuiltin", c.Word))
		}
	}
	for _, op := range m.Ops {
		if op.MCP && slices.ContainsFunc(mcpTools, func(t mcpTool) bool { return t.Name == pluginToolName(name, op.Name) }) {
			out = append(out, msgs.Tc(ctx, "plugin.toolTaken", pluginToolName(name, op.Name)))
		}
	}
	return out, warn
}

// handlePluginConnect — POST /hub/plugins: подключить (или
// переподключить с тем же именем — новые хост, порт, секрет).
func (s *Server) handlePluginConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req pluginConnectReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	conf, secret, err := s.resolveConnect(ctx, &req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m, code, err := s.connectPlugin(ctx, conf, secret, req)
	if err != nil {
		var ce *plugin.CallError
		if code == http.StatusBadGateway && !errors.As(err, &ce) {
			writeError(w, code, msgs.Tc(ctx, "plugin.unreachable", req.Name, conf.Host, conf.Port, err))
			return
		}
		writeErr(w, r, code, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": firstNonEmpty(req.Name, m.Name), "manifest": m})
}

// connectPlugin — манифест, проверки, токен для hub_actions, _connect и
// запись подключения (с тем же именем — заменяется). Ответ — манифест и
// код ошибки HTTP для вызывающего.
func (s *Server) connectPlugin(ctx context.Context, conf pluginConf, secret string, req pluginConnectReq) (plugin.Manifest, int, error) {
	c := plugin.Client{Dial: s.pluginDial(conf.Host, conf.Port), Secret: secret, Lang: string(msgs.FromContext(ctx)), Timeout: 15 * time.Second}
	m, err := c.Manifest(ctx)
	if err != nil {
		return m, http.StatusBadGateway, err
	}
	conf.Name = firstNonEmpty(req.Name, m.Name)
	if !plugin.ValidName(conf.Name) {
		return m, http.StatusBadRequest, msgs.Errorf("plugin.badName", conf.Name)
	}
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	list := s.plugins(ctx)
	prev := slices.IndexFunc(list, func(p pluginConf) bool { return p.Name == conf.Name })
	if conflicts, _ := s.pluginConflicts(ctx, conf.Name, m); len(conflicts) > 0 {
		// Повторное подключение того же — не конфликт с самим собой.
		if prev < 0 || len(conflicts) > 1 {
			return m, http.StatusConflict, errors.New(strings.Join(conflicts, "; "))
		}
	}
	if len(m.HubActions) > 0 && !req.GrantHub {
		return m, http.StatusBadRequest, msgs.Errorf("plugin.needGrant", strings.Join(m.HubActions, ", "))
	}
	user := auth.Username(ctx)
	conf.SecretEnc, err = secretbox.Encrypt(s.hub.key, []byte(secret))
	if err != nil {
		return m, http.StatusInternalServerError, err
	}
	conf.Title, conf.Version, conf.Author, conf.AddedAt = m.Title, m.Version, user, store.FormatTime(time.Now())
	conf.Words = nil
	for _, c := range m.Commands {
		conf.Words = append(conf.Words, c.Word)
	}
	// Права у хаба — токен «по правам» только с запрошенными действиями.
	connect := map[string]any{"name": conf.Name, "hub_url": strings.TrimSpace(req.HubURL)}
	if len(m.HubActions) > 0 {
		token, name, err := s.pluginToken(ctx, conf.Name, m.HubActions, user)
		if err != nil {
			return m, http.StatusBadRequest, err
		}
		connect["token"], conf.Token = token, name
	} else if prev >= 0 && list[prev].Token != "" {
		s.dropPluginToken(ctx, list[prev].Token)
	}
	if _, err := c.Call(ctx, plugin.Request{Op: plugin.OpConnect, Args: connect, User: user}); err != nil {
		var ce *plugin.CallError
		// Плагин без _connect (ему не нужен) — не ошибка.
		if !errors.As(err, &ce) || !strings.Contains(ce.Msg, plugin.OpConnect) {
			if conf.Token != "" {
				s.dropPluginToken(ctx, conf.Token)
			}
			if errors.As(err, &ce) {
				return m, http.StatusBadRequest, errors.New(ce.Msg)
			}
			return m, http.StatusBadGateway, err
		}
	}
	if prev >= 0 {
		list[prev] = conf
	} else {
		list = append(list, conf)
	}
	err = s.savePlugins(ctx, list)
	s.db.Audit(ctx, user, "plugin.connect", conf.Name, auditOutcome(err), map[string]any{"host": conf.Host, "port": conf.Port, "version": m.Version, "hub_actions": m.HubActions})
	if err != nil {
		return m, http.StatusInternalServerError, err
	}
	forgetManifests(conf.Name)
	s.refreshScriptWords(ctx)
	return m, http.StatusOK, nil
}

// pluginToken — токен «по правам» для плагина (прежний — заменяется).
func (s *Server) pluginToken(ctx context.Context, name string, actions []string, author string) (string, string, error) {
	for _, a := range actions {
		if !slices.Contains(store.TokenActions, a) {
			return "", "", msgs.Errorf("hub.tokenBadPerms", a)
		}
	}
	tokName := "plugin-" + name
	if list, err := s.db.ListAPITokens(ctx); err == nil {
		for _, t := range list {
			if t.Name == tokName {
				_ = s.db.DeleteAPIToken(ctx, t.ID)
			}
		}
	}
	keyID, secret, err := newTokenSecret()
	if err != nil {
		return "", "", err
	}
	enc, err := secretbox.Encrypt(s.hub.key, []byte(secret))
	if err != nil {
		return "", "", err
	}
	t := store.APIToken{Name: tokName, KeyID: keyID, SecretEnc: enc, Role: store.TokenRoleCustom, Author: author,
		Perms: store.TokenPerms{Actions: actions}}
	if _, err := s.db.CreateAPIToken(ctx, t); err != nil {
		return "", "", err
	}
	s.db.Audit(ctx, author, "token.create", tokName, "ok", map[string]any{"role": t.Role, "plugin": name})
	return bearerToken(keyID, secret), tokName, nil
}

func (s *Server) dropPluginToken(ctx context.Context, name string) {
	if list, err := s.db.ListAPITokens(ctx); err == nil {
		for _, t := range list {
			if t.Name == name {
				_ = s.db.DeleteAPIToken(ctx, t.ID)
			}
		}
	}
}

// handlePluginDisconnect — DELETE /hub/plugins/{name}: отключить (сам
// плагин и его данные не трогаются; токен плагина отзывается).
func (s *Server) handlePluginDisconnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	list := s.plugins(ctx)
	i := slices.IndexFunc(list, func(p pluginConf) bool { return p.Name == name })
	if i < 0 {
		writeErr(w, r, http.StatusNotFound, store.ErrNotFound)
		return
	}
	if list[i].Token != "" {
		s.dropPluginToken(ctx, list[i].Token)
	}
	list = slices.Delete(list, i, i+1)
	err := s.savePlugins(ctx, list)
	s.db.Audit(ctx, auth.Username(ctx), "plugin.disconnect", name, auditOutcome(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	forgetManifests(name)
	s.refreshScriptWords(ctx)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// pluginFromReq — подключённый плагин из пути.
func (s *Server) pluginFromReq(w http.ResponseWriter, r *http.Request) (pluginConf, bool) {
	p, ok := s.pluginByName(r.Context(), chi.URLParam(r, "name"))
	if !ok {
		writeErr(w, r, http.StatusNotFound, store.ErrNotFound)
	}
	return p, ok
}

// pluginSecretArg — аргументы, значения которых в журнал действий не
// пишутся (пароль ящика из поля password, токены).
var pluginSecretArg = regexp.MustCompile(`(?i)(pass|secret|token|key)`)

// pluginAuditArgs — аргументы операции для журнала: секретные — «***».
func pluginAuditArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if pluginSecretArg.MatchString(k) {
			v = "***"
		}
		out[k] = v
	}
	return out
}

// handlePluginCall — POST /hub/plugins/{name}/call {op, args, dry_run}.
func (s *Server) handlePluginCall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := s.pluginFromReq(w, r)
	if !ok {
		return
	}
	var req plugin.Request
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m, err := s.pluginManifest(ctx, p)
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	op, ok := m.Op(req.Op)
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.unknownOp", req.Op, p.Name))
		return
	}
	if err := s.pluginOpAllowed(ctx, p, op); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	resp, err := s.pluginCall(ctx, p, plugin.Request{Op: req.Op, Args: req.Args, DryRun: req.DryRun})
	if !op.ReadOnly && !req.DryRun {
		s.db.Audit(ctx, auth.Username(ctx), "plugin.op", p.Name+" "+req.Op, auditOutcome(err), pluginAuditArgs(req.Args))
	}
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- документы: правка с диффом и историей версий в хабе ---------------

// pluginDocKey — история документа: plugin.doc.<плагин>/<документ>.
func pluginDocKey(name, doc string) string { return "plugin.doc." + name + "/" + doc }

type pluginDocVersion struct {
	TS     string `json:"ts"`
	Author string `json:"author,omitempty"`
	Note   string `json:"note,omitempty"`
	Text   string `json:"text"`
}

// pluginDocHistoryMax — сколько версий документа хранить.
const pluginDocHistoryMax = 30

func (s *Server) pluginDocHistory(ctx context.Context, name, doc string) []pluginDocVersion {
	var hist []pluginDocVersion
	if raw, ok, err := s.db.KVGet(ctx, pluginDocKey(name, doc)); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &hist)
	}
	return hist
}

func (s *Server) addPluginDocVersion(ctx context.Context, name, doc string, v pluginDocVersion) {
	hist := append(s.pluginDocHistory(ctx, name, doc), v)
	if len(hist) > pluginDocHistoryMax {
		hist = hist[len(hist)-pluginDocHistoryMax:]
	}
	if raw, err := json.Marshal(hist); err == nil {
		_ = s.db.KVSet(ctx, pluginDocKey(name, doc), string(raw))
	}
}

var pluginDocRe = strings.NewReplacer("/", "_", "\\", "_", "\n", "_")

// pluginDocText — текст документа из ответа операции get.
func pluginDocText(resp plugin.Response) string {
	var res struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(resp.Result, &res) == nil && res.Text != "" {
		return res.Text
	}
	return resp.Text
}

// handlePluginDocGet — GET /hub/plugins/{name}/doc?get=OP&doc=ИМЯ&args=JSON:
// текущий текст и история.
func (s *Server) handlePluginDocGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := s.pluginFromReq(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var args map[string]any
	if raw := q.Get("args"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	m, err := s.pluginManifest(ctx, p)
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	op, ok := m.Op(q.Get("get"))
	if !ok || !op.ReadOnly {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.unknownOp", q.Get("get"), p.Name))
		return
	}
	resp, err := s.pluginCall(ctx, p, plugin.Request{Op: op.Name, Args: args})
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	hist := s.pluginDocHistory(ctx, p.Name, pluginDocRe.Replace(q.Get("doc")))
	if hist == nil {
		hist = []pluginDocVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": pluginDocText(resp), "versions": hist})
}

// handlePluginDocPut — PUT /hub/plugins/{name}/doc {get, put, doc, args,
// text, base, note}: записать документ. base — текст, который правили:
// изменился с тех пор — 409 (кто-то правил рядом). Прежний текст — в
// историю (первая запись — исходный), новый — следом.
func (s *Server) handlePluginDocPut(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, ok := s.pluginFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		Get  string         `json:"get"`
		Put  string         `json:"put"`
		Doc  string         `json:"doc"`
		Args map[string]any `json:"args"`
		Text string         `json:"text"`
		Base *string        `json:"base"`
		Note string         `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m, err := s.pluginManifest(ctx, p)
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	getOp, ok1 := m.Op(req.Get)
	putOp, ok2 := m.Op(req.Put)
	if !ok1 || !ok2 || !getOp.ReadOnly {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.unknownOp", req.Get+"/"+req.Put, p.Name))
		return
	}
	if err := s.pluginOpAllowed(ctx, p, putOp); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	cur, err := s.pluginCall(ctx, p, plugin.Request{Op: getOp.Name, Args: req.Args})
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	before := pluginDocText(cur)
	if req.Base != nil && *req.Base != before {
		writeJSON(w, http.StatusConflict, map[string]any{"error": msgs.Tc(ctx, "plugin.docChanged"), "text": before})
		return
	}
	args := map[string]any{}
	for k, v := range req.Args {
		args[k] = v
	}
	args["text"] = req.Text
	user := auth.Username(ctx)
	resp, err := s.pluginCall(ctx, p, plugin.Request{Op: putOp.Name, Args: args})
	s.db.Audit(ctx, user, "plugin.doc", p.Name+" "+req.Doc, auditOutcome(err), map[string]any{"note": req.Note})
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	doc := pluginDocRe.Replace(req.Doc)
	now := store.FormatTime(time.Now())
	if len(s.pluginDocHistory(ctx, p.Name, doc)) == 0 {
		s.addPluginDocVersion(ctx, p.Name, doc, pluginDocVersion{TS: now, Author: "-", Note: msgs.Tc(ctx, "plugin.docOriginal"), Text: before})
	}
	s.addPluginDocVersion(ctx, p.Name, doc, pluginDocVersion{TS: now, Author: user, Note: req.Note, Text: req.Text})
	writeJSON(w, http.StatusOK, resp)
}

// --- MCP -----------------------------------------------------------------

// pluginToolName — инструмент MCP операции: <плагин>_<операция>.
func pluginToolName(plugin, op string) string {
	return strings.ReplaceAll(plugin, "-", "_") + "_" + op
}

// pluginTools — инструменты MCP подключённых плагинов (операции с mcp:
// true), доступные токену.
func (s *Server) pluginTools(ctx context.Context, tok store.APIToken) []map[string]any {
	var out []map[string]any
	for _, p := range s.plugins(ctx) {
		m, err := s.pluginManifest(ctx, p)
		if err != nil {
			continue
		}
		for _, op := range m.Ops {
			name := pluginToolName(p.Name, op.Name)
			if !op.MCP || slices.ContainsFunc(mcpTools, func(t mcpTool) bool { return t.Name == name }) {
				continue
			}
			if !op.ReadOnly && !tok.Can(pluginActPrefix+p.Name) {
				continue
			}
			schema := op.Input
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			desc := op.Description
			if desc == "" {
				desc = op.Title
			}
			out = append(out, map[string]any{
				"name": name, "description": desc + " (plugin " + m.Title + ")", "inputSchema": schema,
				"annotations": map[string]any{"readOnlyHint": op.ReadOnly, "destructiveHint": op.Destructive, "openWorldHint": false},
			})
		}
	}
	return out
}

// pluginToolCall — вызов инструмента плагина через API хаба (те же
// проверки прав, что у интерфейса). ok=false — это не инструмент плагина.
func (s *Server) pluginToolCall(r *http.Request, tok store.APIToken, name string, args map[string]any) (map[string]any, bool) {
	ctx := r.Context()
	for _, p := range s.plugins(ctx) {
		prefix := strings.ReplaceAll(p.Name, "-", "_") + "_"
		opName, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		m, err := s.pluginManifest(ctx, p)
		if err != nil {
			return mcpText(err.Error(), true), true
		}
		op, ok := m.Op(opName)
		if !ok || !op.MCP {
			continue
		}
		code, out := s.mcpInternal(r, tok, http.MethodPost, "/hub/plugins/"+p.Name+"/call", map[string]any{"op": op.Name, "args": args})
		if code >= 400 {
			return mcpText("HTTP "+strconv.Itoa(code)+": "+out, true), true
		}
		var resp plugin.Response
		if json.Unmarshal([]byte(out), &resp) == nil {
			text := resp.Text
			if len(resp.Result) > 0 && string(resp.Result) != "null" {
				if text != "" {
					text += "\n"
				}
				text += string(resp.Result)
			}
			if text == "" && resp.View != nil {
				raw, _ := json.Marshal(resp.View)
				text = string(raw)
			}
			return mcpText(text, false), true
		}
		return mcpText(out, false), true
	}
	return nil, false
}

// pluginActionKnown — действие токена plugin.<имя> подключённого
// плагина.
func (s *Server) pluginActionKnown(ctx context.Context, act string) bool {
	name, ok := strings.CutPrefix(act, pluginActPrefix)
	if !ok {
		return false
	}
	_, ok = s.pluginByName(ctx, name)
	return ok
}

// --- сценарии ------------------------------------------------------------

// scriptBuiltinWord — слово занято языком сценариев.
func scriptBuiltinWord(w string) bool { return script.BuiltinWord(w) }

// refreshScriptWords — слова команд подключённых плагинов для разбора
// сценариев (из сохранённых при подключении — без обращения к плагинам).
func (s *Server) refreshScriptWords(ctx context.Context) {
	words := map[string]string{}
	for _, p := range s.plugins(ctx) {
		for _, w := range p.Words {
			if _, taken := words[w]; !taken {
				words[w] = p.Name
			}
		}
	}
	script.SetPluginWords(words)
}

// checkScript — script.Check и «Проверить» у плагинов для их команд
// (если параметры уже известны: заглушку плагин не разберёт).
func (s *Server) checkScript(ctx context.Context, sc script.Script, refs script.Refs) []script.Issue {
	issues := script.Check(sc, refs)
	if len(issues) > 0 || sc.Unresolved {
		return issues
	}
	for _, st := range sc.Steps {
		if st.Kind != script.KindPlugin {
			continue
		}
		p, ok := s.pluginByName(ctx, st.Name)
		if !ok {
			continue
		}
		if _, err := s.pluginCall(ctx, p, plugin.Request{Op: plugin.OpCheck, Args: map[string]any{"argv": st.List}}); err != nil {
			issues = append(issues, script.Issue{Line: st.Line, Err: err})
		}
	}
	return issues
}

// pluginStep — команда плагина в сценарии: в сухом прогоне плагин только
// отвечает, что сделал бы. Ответ — строками в журнал задания.
func (s *Server) pluginStep(ctx context.Context, jc *jobs.Context, st script.Step, dry bool) error {
	p, ok := s.pluginByName(ctx, st.Name)
	if !ok {
		return msgs.Errorf("script.unknownPlugin", st.Name)
	}
	c, err := s.pluginClient(ctx, p)
	if err != nil {
		return err
	}
	c.Timeout = 10 * time.Minute
	resp, err := c.Call(ctx, plugin.Request{Op: plugin.OpCommand, Args: map[string]any{"argv": st.List}, User: jc.Job.Author, DryRun: dry})
	if !dry {
		s.db.Audit(ctx, jc.Job.Author, "plugin.command", p.Name+" "+strings.Join(st.List, " "), auditOutcome(err), nil)
	}
	if err != nil {
		var ce *plugin.CallError
		if errors.As(err, &ce) {
			return msgs.Errorf("plugin.commandFailed", p.Name, ce.Msg)
		}
		return msgs.Errorf("plugin.unreachable", p.Name, p.Host, p.Port, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(resp.Text), "\n") {
		if line != "" {
			jc.Log("plugin.commandOut", p.Name, line)
		}
	}
	return nil
}

// reencryptPlugins — список плагинов из экспорта с секретами под ключом
// этого хаба.
func reencryptPlugins(raw string, reenc func([]byte) ([]byte, error)) (string, error) {
	var list []pluginConf
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return "", err
	}
	for i := range list {
		enc, err := reenc(list[i].SecretEnc)
		if err != nil {
			return "", err
		}
		list[i].SecretEnc = enc
	}
	out, err := json.Marshal(list)
	return string(out), err
}
