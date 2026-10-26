package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/examples"
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/plugin"
	"github.com/piqab/nkt/internal/store"
)

// «Настройки» плагина, поставленного хабом на хост: форма по описанию
// плагина (nkt-plugin.json). Несекретные значения хаб читает с хоста,
// секретов не видит никто — только «задан» и «заменить». Сохранение —
// заданием: агент пишет файл настроек и перезапускает службу; плагин не
// ответил — прежний файл возвращается. История несекретных значений — в
// хабе (pluginSettingsKV + имя), секретов в ней нет.

const (
	pluginSettingsKV  = "plugin.settings."
	pluginSettingsMax = 20
)

// pluginSettingsVersion — версия несекретных настроек.
type pluginSettingsVersion struct {
	At      string            `json:"at"`
	User    string            `json:"user"`
	Values  map[string]string `json:"values"`
	Changed []string          `json:"changed,omitempty"`
}

// pluginSettingsField — поле окна «Настройки».
type pluginSettingsField struct {
	Env      string `json:"env"`
	Title    string `json:"title"`
	Hint     string `json:"hint,omitempty"`
	Default  string `json:"default,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Value — текущее значение (несекретные); Set — задан ли секрет.
	Value string `json:"value"`
	Set   bool   `json:"set,omitempty"`
}

// pluginSettingsChange — строка сверки.
type pluginSettingsChange struct {
	Env    string `json:"env"`
	Title  string `json:"title"`
	Old    string `json:"old,omitempty"`
	New    string `json:"new,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// pluginNativeTarget — плагин, поставленный хабом: пакет каталога и хост.
func (s *Server) pluginNativeTarget(ctx context.Context, p pluginConf) (examples.PluginPackage, targetHost, error) {
	pkg, ok := pluginPackage(p.Native)
	if p.Native == "" || !ok {
		return pkg, targetHost{}, msgs.Errorf("plugin.notNative", p.Name)
	}
	targets, err := s.resolveHosts(ctx, []string{p.Host}, "")
	if err != nil || len(targets) != 1 {
		return pkg, targetHost{}, msgs.Errorf("plugin.hostGone", p.Host)
	}
	return pkg, targets[0], nil
}

// pluginSettingsRead — текущие несекретные значения и какие секреты заданы.
func (s *Server) pluginSettingsRead(ctx context.Context, user string, p pluginConf, pkg examples.PluginPackage, hostID int64) (map[string]string, map[string]bool, error) {
	keys, secrets := []string{}, []string{}
	for _, f := range pkg.Settings {
		if f.Secret {
			secrets = append(secrets, f.Env)
		} else {
			keys = append(keys, f.Env)
		}
	}
	var out struct {
		Values map[string]string `json:"values"`
		Set    map[string]bool   `json:"set"`
	}
	if _, err := s.hostCall(ctx, user, hostID, "POST", "/api/plugins/native/settings",
		map[string]any{"name": p.Name, "op": "read", "keys": keys, "secrets": secrets}, &out); err != nil {
		return nil, nil, err
	}
	if out.Values == nil {
		out.Values = map[string]string{}
	}
	return out.Values, out.Set, nil
}

func (s *Server) pluginSettingsHistory(ctx context.Context, name string) []pluginSettingsVersion {
	var list []pluginSettingsVersion
	if raw, ok, err := s.db.KVGet(ctx, pluginSettingsKV+name); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	return list
}

func (s *Server) pluginSettingsRemember(ctx context.Context, name string, v pluginSettingsVersion) error {
	list := append([]pluginSettingsVersion{v}, s.pluginSettingsHistory(ctx, name)...)
	if len(list) > pluginSettingsMax {
		list = list[:pluginSettingsMax]
	}
	raw, _ := json.Marshal(list)
	return s.db.KVSet(ctx, pluginSettingsKV+name, string(raw))
}

// needsSettings — у плагина нет обязательной настройки из описания (новая
// версия плагина добавила поле): пометка «нужна настройка».
func needsSettings(p pluginConf) bool {
	pkg, ok := pluginPackage(p.Native)
	if p.Native == "" || !ok || p.SettingKeys == nil {
		return false
	}
	for _, f := range pkg.Settings {
		if f.Required && f.Default == "" && !slices.Contains(p.SettingKeys, f.Env) {
			return true
		}
	}
	return false
}

// noSettings — плагин поставлен хабом из пакета, у которого настроек нет.
// Чужой плагин (пакета нет) — неизвестно, «Настроить» остаётся.
func noSettings(p pluginConf) bool {
	pkg, ok := pluginPackage(p.Native)
	return p.Native != "" && ok && len(pkg.Settings) == 0
}

// handlePluginSettings — GET /hub/plugins/{name}/settings: поля с текущими
// значениями (секреты — только «задан») и история.
func (s *Server) handlePluginSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pluginFromReq(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	pkg, h, err := s.pluginNativeTarget(ctx, p)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	values, set, err := s.pluginSettingsRead(ctx, auth.Username(ctx), p, pkg, h.ID)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	lang := string(msgs.FromContext(ctx))
	fields := make([]pluginSettingsField, 0, len(pkg.Settings))
	for _, f := range pkg.Settings {
		fields = append(fields, pluginSettingsField{Env: f.Env, Title: pick(f.Title, lang), Hint: pick(f.Hint, lang), Default: f.Default,
			Secret: f.Secret, Required: f.Required, Value: values[f.Env], Set: set[f.Env]})
	}
	hist := s.pluginSettingsHistory(ctx, p.Name)
	if hist == nil {
		hist = []pluginSettingsVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": fields, "history": hist, "host": p.Host})
}

// handlePluginSettingsSave — POST /hub/plugins/{name}/settings {settings,
// dry_run}: сверка (что изменится) или задание. Пустой секрет — прежний;
// пустое обычное значение — значение по умолчанию (или убрать).
func (s *Server) handlePluginSettingsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pluginFromReq(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var req struct {
		Settings map[string]string `json:"settings"`
		DryRun   bool              `json:"dry_run"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	pkg, h, err := s.pluginNativeTarget(ctx, p)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	lang := string(msgs.FromContext(ctx))
	known := map[string]bool{}
	for _, f := range pkg.Settings {
		known[f.Env] = true
	}
	for k := range req.Settings {
		if !known[k] {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.settingUnknown", p.Name, k))
			return
		}
	}
	values, set, err := s.pluginSettingsRead(ctx, auth.Username(ctx), p, pkg, h.ID)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	changes := []pluginSettingsChange{}
	apply := map[string]string{}
	for _, f := range pkg.Settings {
		v := strings.TrimSpace(req.Settings[f.Env])
		if strings.ContainsAny(v, "\r\n\"'\\$`") {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeBadEnv", f.Env))
			return
		}
		title := pick(f.Title, lang)
		if f.Secret {
			if v == "" {
				if f.Required && !set[f.Env] {
					writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.settingRequired", title))
					return
				}
				continue
			}
			apply[f.Env] = v
			changes = append(changes, pluginSettingsChange{Env: f.Env, Title: title, Secret: true})
			continue
		}
		if v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.settingRequired", title))
			return
		}
		if v != values[f.Env] {
			apply[f.Env] = v
			changes = append(changes, pluginSettingsChange{Env: f.Env, Title: title, Old: values[f.Env], New: v})
		}
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
		return
	}
	if len(apply) == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.settingsNothing"))
		return
	}
	// Значения (и секреты) — в одноразовом хранилище, в параметрах задания
	// только имена.
	names := make([]string, 0, len(apply))
	for k := range apply {
		names = append(names, k)
	}
	sort.Strings(names)
	raw, _ := json.Marshal(apply)
	ticket := oneTime.Put(raw)
	wipe(raw)
	s.startPluginJob(w, r, PluginInstallParams{Package: p.Native, HostID: h.ID, HostName: h.Name, Name: p.Name, Action: "settings",
		Port: p.Port, Ticket: ticket, Names: names}, "plugin.settingsTitle")
}

// runSettings — задание «Настройки»: записать, дождаться плагина (нет —
// вернуть прежние), запомнить версию, у DNS-плагина — сверка записей.
func (r *PluginInstallRunner) runSettings(ctx context.Context, jc *jobs.Context, user string, p PluginInstallParams) error {
	s := r.s
	raw, ok := oneTime.Take(p.Ticket)
	if !ok {
		return msgs.Errorf("plugin.ticketGone")
	}
	var set map[string]string
	err := json.Unmarshal(raw, &set)
	wipe(raw)
	if err != nil {
		return err
	}
	conf, ok := s.pluginByName(ctx, p.Name)
	if !ok {
		return store.ErrNotFound
	}
	pkg, _ := pluginPackage(conf.Native)
	before, _, err := s.pluginSettingsRead(ctx, user, conf, pkg, p.HostID)
	if err != nil {
		return err
	}
	jc.StepKey(1, 3, "plugin.stepSettings", p.Name, p.HostName)
	call := func(op string, body map[string]any, out any) error {
		body["name"], body["op"] = p.Name, op
		_, err := s.hostCall(ctx, user, p.HostID, "POST", "/api/plugins/native/settings", body, out)
		return err
	}
	err = call("apply", map[string]any{"set": set}, nil)
	set = nil
	if err != nil {
		return err
	}
	jc.StepKey(2, 3, "plugin.stepWait", p.Name)
	var m pluginManifestWait
	if werr := s.waitPluginManifest(ctx, conf, &m); werr != nil {
		var back struct {
			Log string `json:"log"`
		}
		if err := call("rollback", map[string]any{}, &back); err != nil {
			return err
		}
		if back.Log != "" {
			jc.Log("plugin.settingsLogTail", p.Name)
			for _, line := range strings.Split(back.Log, "\n") {
				jc.Logf("      %s", line)
			}
		}
		forgetManifests(p.Name)
		return msgs.Errorf("plugin.settingsRolledBack", p.Name, werr)
	}
	_ = call("commit", map[string]any{}, nil)
	after, _, err := s.pluginSettingsRead(ctx, user, conf, pkg, p.HostID)
	if err != nil {
		return err
	}
	now := store.FormatTime(time.Now())
	// Первая правка — сначала версия «как было», чтобы её можно было вернуть.
	if len(s.pluginSettingsHistory(ctx, p.Name)) == 0 {
		_ = s.pluginSettingsRemember(ctx, p.Name, pluginSettingsVersion{At: now, User: conf.Author, Values: before})
	}
	if err := s.pluginSettingsRemember(ctx, p.Name, pluginSettingsVersion{At: now, User: jc.Job.Author, Values: after, Changed: p.Names}); err != nil {
		return err
	}
	pluginsMu.Lock()
	list := s.plugins(ctx)
	for i := range list {
		if list[i].Name == p.Name {
			keys := []string{}
			for k := range after {
				keys = append(keys, k)
			}
			for _, k := range p.Names {
				if !slices.Contains(keys, k) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			list[i].SettingKeys = keys
		}
	}
	err = s.savePlugins(ctx, list)
	pluginsMu.Unlock()
	if err != nil {
		return err
	}
	jc.StepKey(3, 3, "plugin.settingsApplied", p.Name, m.version)
	// DNS-плагин: другие зоны или токен — сверка записей сразу, не через
	// 10 минут.
	if mf, err := s.pluginManifest(ctx, conf); err == nil && slices.Contains(mf.Provides, plugin.ProvidesDNS) {
		jc.Log("plugin.settingsDNSSync", p.Name)
		if err := s.dnsSync(ctx, nil, false, jc.Log); err != nil {
			jc.Log("plugin.settingsDNSSyncFailed", err)
		}
	}
	return nil
}
