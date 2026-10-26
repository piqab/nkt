package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/osinfo"
	"github.com/piqab/nkt/internal/plugin"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Хосты плагинов (provides hosts): устройства без агента nkt — MikroTik,
// Windows, облачные ВМ, — которые читает и которыми управляет плагин. В
// хабе это обычные строки hosts с kind = plugin: список, группы, пределы
// токенов, оповещения, «Мониторинг», экспорт. Учётки подключения (поля
// формы «Добавить хост») хранит хаб под мастер-ключом и передаёт плагину в
// каждом вызове; наружу секретные поля (type: password) не отдаются.
// ListHosts таких хостов не видит — всё, что ходит к агенту nkt, их не
// трогает; здесь — их собственные вызовы и опрос.

// pluginHostCallTimeout — действие или команда на устройстве (обновление,
// скрипт) бывает долгим.
const pluginHostCallTimeout = 3 * time.Minute

var pluginHostNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// pluginHostType — тип устройства подключённого плагина.
type pluginHostType struct {
	Plugin      string          `json:"plugin"`
	PluginTitle string          `json:"plugin_title"`
	Type        plugin.HostType `json:"type"`
}

// pluginHostTypes — типы устройств всех подключённых плагинов.
func (s *Server) pluginHostTypes(ctx context.Context) []pluginHostType {
	var out []pluginHostType
	for _, p := range s.plugins(ctx) {
		m, err := s.pluginManifest(ctx, p)
		if err != nil || !m.Has(plugin.ProvidesHosts) {
			continue
		}
		for _, t := range m.HostTypes {
			out = append(out, pluginHostType{Plugin: p.Name, PluginTitle: m.Title, Type: t})
		}
	}
	return out
}

// pluginHostTypeOf — тип хоста плагина (манифест плагина сейчас).
func (s *Server) pluginHostTypeOf(ctx context.Context, h store.Host) (pluginConf, plugin.HostType, error) {
	p, ok := s.pluginByName(ctx, h.Plugin)
	if !ok {
		return p, plugin.HostType{}, msgs.Errorf("plugin.hostNoPlugin", h.Name, h.Plugin)
	}
	m, err := s.pluginManifest(ctx, p)
	if err != nil {
		return p, plugin.HostType{}, err
	}
	t, ok := m.HostType(h.PluginType)
	if !ok {
		return p, plugin.HostType{}, msgs.Errorf("plugin.hostNoType", h.Name, h.PluginType, h.Plugin)
	}
	return p, t, nil
}

func (s *Server) pluginHostConn(h store.Host) (map[string]any, error) {
	conn := map[string]any{}
	if len(h.ConnEnc) == 0 {
		return conn, nil
	}
	raw, err := secretbox.Decrypt(s.hub.key, h.ConnEnc)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &conn)
	return conn, err
}

// pluginHostCall — служебная операция плагина для хоста: args {type, conn,
// …extra}.
func (s *Server) pluginHostCall(ctx context.Context, h store.Host, op string, extra map[string]any, dry bool, out any) (plugin.Response, error) {
	p, _, err := s.pluginHostTypeOf(ctx, h)
	if err != nil {
		return plugin.Response{}, err
	}
	conn, err := s.pluginHostConn(h)
	if err != nil {
		return plugin.Response{}, err
	}
	return s.pluginTypeCall(ctx, p, h.PluginType, conn, op, extra, dry, out)
}

func (s *Server) pluginTypeCall(ctx context.Context, p pluginConf, typ string, conn map[string]any, op string, extra map[string]any, dry bool, out any) (plugin.Response, error) {
	c, err := s.pluginClient(ctx, p)
	if err != nil {
		return plugin.Response{}, err
	}
	c.Timeout = pluginHostCallTimeout
	args := map[string]any{"type": typ, "conn": conn}
	for k, v := range extra {
		args[k] = v
	}
	resp, err := c.Call(ctx, plugin.Request{Op: op, Args: args, User: auth.Username(ctx), DryRun: dry})
	if err != nil {
		return resp, err
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

// pluginHostSecretFields — поля типа, которые не показываются (пароли).
func pluginHostSecretFields(t plugin.HostType) map[string]bool {
	out := map[string]bool{}
	for _, f := range t.Fields {
		if f.Type == "password" {
			out[f.Name] = true
		}
	}
	return out
}

// pluginHostConnFrom — поля формы по типу: известные, строками, без
// переводов строк; пустой секрет — прежний (prev).
func pluginHostConnFrom(t plugin.HostType, in map[string]any, prev map[string]any) (map[string]any, error) {
	secret := pluginHostSecretFields(t)
	out := map[string]any{}
	for _, f := range t.Fields {
		v := strings.TrimSpace(anyString(in[f.Name]))
		if v == "" && secret[f.Name] && prev != nil {
			v = anyString(prev[f.Name])
		}
		if v == "" && f.Default != nil {
			v = anyString(f.Default)
		}
		if strings.ContainsAny(v, "\r\n\x00") || len(v) > 4096 {
			return nil, msgs.Errorf("plugin.hostBadField", f.Title)
		}
		if v == "" && f.Required {
			return nil, msgs.Errorf("plugin.settingRequired", f.Title)
		}
		if v != "" {
			out[f.Name] = v
		}
	}
	return out, nil
}

func anyString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// handlePluginHostTypes — GET /hub/plugin-hosts/types: что можно добавить.
func (s *Server) handlePluginHostTypes(w http.ResponseWriter, r *http.Request) {
	list := s.pluginHostTypes(r.Context())
	if list == nil {
		list = []pluginHostType{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"types": list})
}

type pluginHostReq struct {
	Name   string         `json:"name"`
	Group  string         `json:"group"`
	Plugin string         `json:"plugin"`
	Type   string         `json:"type"`
	Conn   map[string]any `json:"conn"`
}

// pluginHostCheck — «Проверить»: плагин подключается к устройству. Пустой
// отпечаток в поле fingerprint заполняется тем, что увидел плагин
// (доверие при первом подключении, как ключ SSH хоста).
func (s *Server) pluginHostCheck(ctx context.Context, p pluginConf, t plugin.HostType, conn map[string]any) (plugin.HostInfo, error) {
	var info plugin.HostInfo
	if _, err := s.pluginTypeCall(ctx, p, t.ID, conn, plugin.OpHostCheck, nil, false, &info); err != nil {
		return info, err
	}
	if info.Fingerprint != "" && slices.ContainsFunc(t.Fields, func(f plugin.Field) bool { return f.Name == "fingerprint" }) && anyString(conn["fingerprint"]) == "" {
		conn["fingerprint"] = info.Fingerprint
	}
	return info, nil
}

func (s *Server) pluginHostReqType(ctx context.Context, req pluginHostReq) (pluginConf, plugin.HostType, error) {
	p, ok := s.pluginByName(ctx, req.Plugin)
	if !ok {
		return p, plugin.HostType{}, msgs.Errorf("plugin.hostNoPlugin", req.Name, req.Plugin)
	}
	m, err := s.pluginManifest(ctx, p)
	if err != nil {
		return p, plugin.HostType{}, err
	}
	t, ok := m.HostType(req.Type)
	if !ok {
		return p, plugin.HostType{}, msgs.Errorf("plugin.hostNoType", req.Name, req.Type, req.Plugin)
	}
	return p, t, nil
}

// handlePluginHostCheck — POST /hub/plugin-hosts/check {plugin, type, conn}.
func (s *Server) handlePluginHostCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req pluginHostReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	p, t, err := s.pluginHostReqType(ctx, req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	conn, err := pluginHostConnFrom(t, req.Conn, nil)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	info, err := s.pluginHostCheck(ctx, p, t, conn)
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"info": info, "fingerprint": anyString(conn["fingerprint"])})
}

// handlePluginHostAdd — POST /hub/plugin-hosts: проверить и завести хост.
func (s *Server) handlePluginHostAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req pluginHostReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	p, t, err := s.pluginHostReqType(ctx, req)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	conn, err := pluginHostConnFrom(t, req.Conn, nil)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	info, err := s.pluginHostCheck(ctx, p, t, conn)
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if name == "" {
		name = strings.ToLower(info.Name)
	}
	if !pluginHostNameRe.MatchString(name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.hostBadName", name))
		return
	}
	if all, err := s.db.ListAllHosts(ctx); err == nil && slices.ContainsFunc(all, func(h store.Host) bool { return h.Name == name }) {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("plugin.hostNameTaken", name))
		return
	}
	raw, _ := json.Marshal(conn)
	enc, err := secretbox.Encrypt(s.hub.key, raw)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	id, err := s.db.CreatePluginHost(ctx, name, anyString(conn["address"]), strings.TrimSpace(req.Group), p.Name, t.ID, enc)
	s.db.Audit(ctx, auth.Username(ctx), "plugin.hostAdd", name, auditOutcome(err), map[string]any{"plugin": p.Name, "type": t.ID, "address": conn["address"]})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	go s.pollPluginHostNow(context.WithoutCancel(ctx), id, true)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "info": info})
}

// pluginHostFromReq — хост плагина по {id} (и в пределах токена).
func (s *Server) pluginHostFromReq(w http.ResponseWriter, r *http.Request) (store.Host, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return store.Host{}, false
	}
	if allow := s.scopeFilter(r.Context()); allow != nil && !allow(id) {
		writeErr(w, r, http.StatusNotFound, store.ErrNotFound)
		return store.Host{}, false
	}
	h, err := s.db.HostByID(r.Context(), id)
	if err != nil || !h.IsPlugin() {
		writeErr(w, r, http.StatusNotFound, store.ErrNotFound)
		return store.Host{}, false
	}
	return h, true
}

// handlePluginHost — GET /hub/plugin-hosts/{id}: хост, тип, поля
// подключения без секретов (у секретов — «задан»), последний опрос.
func (s *Server) handlePluginHost(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := map[string]any{"host": h}
	_, t, err := s.pluginHostTypeOf(ctx, h)
	if err != nil {
		out["error"] = msgs.Localize(msgs.FromContext(ctx), err)
	} else {
		out["type"] = t
		conn, _ := s.pluginHostConn(h)
		secret := pluginHostSecretFields(t)
		shown, set := map[string]string{}, map[string]bool{}
		for _, f := range t.Fields {
			if secret[f.Name] {
				set[f.Name] = anyString(conn[f.Name]) != ""
			} else {
				shown[f.Name] = anyString(conn[f.Name])
			}
		}
		out["conn"], out["secret_set"] = shown, set
	}
	if st, ok := s.pluginHostState(h.ID); ok {
		out["state"] = st
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePluginHostConn — PUT /hub/plugin-hosts/{id}/conn {conn}: новые
// учётки (пустой секрет — прежний), «Проверить» перед записью.
func (s *Server) handlePluginHostConn(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var req pluginHostReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	p, t, err := s.pluginHostTypeOf(ctx, h)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	prev, _ := s.pluginHostConn(h)
	conn, err := pluginHostConnFrom(t, req.Conn, prev)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	info, err := s.pluginHostCheck(ctx, p, t, conn)
	if err != nil {
		pluginError(w, r, p, err)
		return
	}
	raw, _ := json.Marshal(conn)
	enc, err := secretbox.Encrypt(s.hub.key, raw)
	if err == nil {
		err = s.db.SetPluginHostConn(ctx, h.ID, anyString(conn["address"]), enc)
	}
	var changed []string
	for k, v := range conn {
		if anyString(prev[k]) != anyString(v) {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	s.db.Audit(ctx, auth.Username(ctx), "plugin.hostConn", h.Name, auditOutcome(err), map[string]any{"fields": changed})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"info": info})
}

// handlePluginHostDelete — DELETE /hub/plugin-hosts/{id}: убрать из хаба
// (на устройстве ничего не меняется).
func (s *Server) handlePluginHostDelete(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	err := s.db.DeleteHost(ctx, h.ID)
	s.db.Audit(ctx, auth.Username(ctx), "plugin.hostDelete", h.Name, auditOutcome(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	_ = s.db.KVSet(ctx, pluginHostConfigKV+strconv.FormatInt(h.ID, 10), "")
	pluginHostStates.Lock()
	delete(pluginHostStates.m, h.ID)
	pluginHostStates.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// pluginHostSection — у типа хоста есть раздел (иначе 404).
func (s *Server) pluginHostSection(w http.ResponseWriter, r *http.Request, h store.Host, section string) (plugin.HostType, bool) {
	_, t, err := s.pluginHostTypeOf(r.Context(), h)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return t, false
	}
	if !t.HasSection(section) {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("plugin.hostNoSection", h.Name, section))
		return t, false
	}
	return t, true
}

// handlePluginHostSnapshot — GET /hub/plugin-hosts/{id}/snapshot: состав.
func (s *Server) handlePluginHostSnapshot(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	if _, ok := s.pluginHostSection(w, r, h, plugin.SectionOverview); !ok {
		return
	}
	var snap plugin.HostSnapshot
	if _, err := s.pluginHostCall(r.Context(), h, plugin.OpHostSnapshot, nil, false, &snap); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	pluginHostStates.Lock()
	st := pluginHostStates.m[h.ID]
	st.Neighbors = snap.Neighbors
	pluginHostStates.m[h.ID] = st
	pluginHostStates.Unlock()
	writeJSON(w, http.StatusOK, snap)
}

// handlePluginHostFindings — GET /hub/plugin-hosts/{id}/findings.
func (s *Server) handlePluginHostFindings(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	if _, ok := s.pluginHostSection(w, r, h, plugin.SectionFindings); !ok {
		return
	}
	var out struct {
		Findings []plugin.HostFinding `json:"findings"`
	}
	if _, err := s.pluginHostCall(r.Context(), h, plugin.OpHostFindings, nil, false, &out); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if out.Findings == nil {
		out.Findings = []plugin.HostFinding{}
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePluginHostMetrics — GET /hub/plugin-hosts/{id}/metrics: сейчас.
func (s *Server) handlePluginHostMetrics(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	if _, ok := s.pluginHostSection(w, r, h, plugin.SectionMetrics); !ok {
		return
	}
	var m plugin.HostMetrics
	if _, err := s.pluginHostCall(r.Context(), h, plugin.OpHostMetrics, nil, false, &m); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// --- конфигурация: версии в хабе -------------------------------------------

// pluginHostConfigKV — версии конфигурации хоста плагина (новые первыми).
const (
	pluginHostConfigKV  = "plugin.hostcfg."
	pluginHostConfigMax = 30
)

type pluginHostConfigVersion struct {
	At   string `json:"at"`
	SHA  string `json:"sha"`
	Text string `json:"text"`
	// By — кто прочитал (опрос хаба или пользователь).
	By string `json:"by,omitempty"`
}

func (s *Server) pluginHostConfigs(ctx context.Context, id int64) []pluginHostConfigVersion {
	var list []pluginHostConfigVersion
	if raw, ok, err := s.db.KVGet(ctx, pluginHostConfigKV+strconv.FormatInt(id, 10)); err == nil && ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	return list
}

// pluginHostConfigFetch — конфигурация с устройства; изменилась —
// новая версия (так видно, что поменяли на устройстве, кто бы ни менял).
func (s *Server) pluginHostConfigFetch(ctx context.Context, h store.Host, by string) ([]pluginHostConfigVersion, error) {
	var out struct {
		Text string `json:"text"`
	}
	if _, err := s.pluginHostCall(ctx, h, plugin.OpHostConfig, nil, false, &out); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(out.Text))
	sha := hex.EncodeToString(sum[:8])
	list := s.pluginHostConfigs(ctx, h.ID)
	if len(list) > 0 && list[0].SHA == sha {
		return list, nil
	}
	list = append([]pluginHostConfigVersion{{At: store.FormatTime(time.Now()), SHA: sha, Text: out.Text, By: by}}, list...)
	if len(list) > pluginHostConfigMax {
		list = list[:pluginHostConfigMax]
	}
	raw, _ := json.Marshal(list)
	if err := s.db.KVSet(ctx, pluginHostConfigKV+strconv.FormatInt(h.ID, 10), string(raw)); err != nil {
		return nil, err
	}
	return list, nil
}

// handlePluginHostConfig — GET /hub/plugin-hosts/{id}/config[?fresh=1]:
// версии конфигурации (fresh — сначала прочитать с устройства).
func (s *Server) handlePluginHostConfig(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	if _, ok := s.pluginHostSection(w, r, h, plugin.SectionConfig); !ok {
		return
	}
	ctx := r.Context()
	list := s.pluginHostConfigs(ctx, h.ID)
	if r.URL.Query().Get("fresh") == "1" || len(list) == 0 {
		var err error
		if list, err = s.pluginHostConfigFetch(ctx, h, auth.Username(ctx)); err != nil {
			writeErr(w, r, http.StatusBadGateway, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list})
}

// --- действия и команды -----------------------------------------------------

// pluginHostWriteAllowed — менять устройство: администратор или токен с
// действием «плагин ИМЯ» (хост — уже в пределах токена).
func (s *Server) pluginHostWriteAllowed(ctx context.Context, h store.Host) error {
	if t, ok := tokenFromContext(ctx); ok {
		if t.Can(pluginActPrefix + h.Plugin) {
			return nil
		}
		return tokenDeny(http.StatusForbidden, "auth.tokenActionDenied", pluginActPrefix+h.Plugin)
	}
	if u, ok := auth.UserFromContext(ctx); ok && u.IsAdmin() {
		return nil
	}
	return msgs.Errorf("auth.adminOnly")
}

// handlePluginHostAction — POST /hub/plugin-hosts/{id}/action {action,
// args, dry_run}.
func (s *Server) handlePluginHostAction(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	t, ok := s.pluginHostSection(w, r, h, plugin.SectionActions)
	if !ok {
		return
	}
	if err := s.pluginHostWriteAllowed(ctx, h); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	var req struct {
		Action string         `json:"action"`
		Args   map[string]any `json:"args"`
		DryRun bool           `json:"dry_run"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !slices.ContainsFunc(t.Actions, func(a plugin.Action) bool { return a.Op == req.Action }) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.hostNoAction", h.Name, req.Action))
		return
	}
	resp, err := s.pluginHostCall(ctx, h, plugin.OpHostAction, map[string]any{"action": req.Action, "args": req.Args}, req.DryRun, nil)
	if !req.DryRun {
		s.db.Audit(ctx, auth.Username(ctx), "plugin.hostAction", h.Name+" "+req.Action, auditOutcome(err), pluginAuditArgs(req.Args))
	}
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": resp.Text})
}

// pluginHostExec — команда на языке типа (страница хоста, сценарий, MCP).
func (s *Server) pluginHostExec(ctx context.Context, h store.Host, command string, check, dry bool) (string, error) {
	_, t, err := s.pluginHostTypeOf(ctx, h)
	if err != nil {
		return "", err
	}
	if t.Exec == "" {
		return "", msgs.Errorf("script.runNoExec", h.Name, t.Title)
	}
	if strings.TrimSpace(command) == "" {
		return "", msgs.Errorf("script.runEmpty")
	}
	resp, err := s.pluginHostCall(ctx, h, plugin.OpHostExec, map[string]any{"command": command, "check": check}, dry, nil)
	return resp.Text, err
}

// handlePluginHostExec — POST /hub/plugin-hosts/{id}/exec {command,
// dry_run}.
func (s *Server) handlePluginHostExec(w http.ResponseWriter, r *http.Request) {
	h, ok := s.pluginHostFromReq(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, ok := s.pluginHostSection(w, r, h, plugin.SectionExec); !ok {
		return
	}
	if err := s.pluginHostWriteAllowed(ctx, h); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	var req struct {
		Command string `json:"command"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	text, err := s.pluginHostExec(ctx, h, req.Command, false, req.DryRun)
	if !req.DryRun {
		// Сама команда — в журнал действий: её видно, кто и что запускал
		// (секретов в командах RouterOS и PowerShell быть не должно).
		s.db.Audit(ctx, auth.Username(ctx), "plugin.hostExec", h.Name, auditOutcome(err), map[string]any{"command": req.Command})
	}
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text})
}

// --- опрос ------------------------------------------------------------------

// pluginHostStateJSON — последний опрос хоста плагина.
type pluginHostStateJSON struct {
	Reachable bool                  `json:"reachable"`
	Error     string                `json:"error,omitempty"`
	PolledAt  string                `json:"polled_at,omitempty"`
	Info      *plugin.HostInfo      `json:"info,omitempty"`
	Metrics   *plugin.HostMetrics   `json:"metrics,omitempty"`
	Findings  map[string]int        `json:"findings,omitempty"`
	Neighbors []plugin.HostNeighbor `json:"-"`
	ticks     int
	prevNet   map[string]plugin.HostNetCounters
	prevAt    time.Time
}

var pluginHostStates = struct {
	sync.Mutex
	m map[int64]pluginHostStateJSON
}{m: map[int64]pluginHostStateJSON{}}

func (s *Server) pluginHostState(id int64) (pluginHostStateJSON, bool) {
	pluginHostStates.Lock()
	defer pluginHostStates.Unlock()
	st, ok := pluginHostStates.m[id]
	return st, ok
}

// pluginHostTypeIcons — значок (model.OSInfo.ID) по типу устройства;
// прочие типы — общий значок «устройство».
var pluginHostTypeIcons = map[string]string{
	"windows": "windows", "routeros": "mikrotik", "openwrt": "openwrt", "macos": "macos",
	"freebsd": "freebsd", "keenetic": "keenetic", "snmp-printer": "printer", "snmp-switch": "switch",
	"yc-vm": "yandexcloud",
}

func pluginHostOS(typ string, info *plugin.HostInfo) *model.OSInfo {
	out := &model.OSInfo{ID: pluginHostTypeIcons[typ], Source: osinfo.SourcePlugin}
	if out.ID == "" {
		out.ID = "device"
	}
	if info != nil {
		// Linux без агента — значок дистрибутива по имени ОС с устройства.
		if typ == "linux-ssh" {
			if id := osinfo.NormalizeID(strings.Fields(info.OS + " linux")[0]); id != "" {
				out.ID, out.Like = id, "linux"
			}
		}
		out.Name = strings.Join(strings.Fields(strings.Join([]string{info.Model, info.OS, info.Version}, " ")), " ")
	}
	return out
}

// pluginHostPollEvery — метрики каждый опрос, находки — каждый десятый,
// конфигурация — раз в час.
const (
	pluginHostPollEvery     = time.Minute
	pluginHostFindingsEvery = 10
	pluginHostConfigEvery   = 60
)

// StartPluginHostPoll — фоновый опрос хостов плагинов.
func (s *Server) StartPluginHostPoll(ctx context.Context) {
	go func() {
		t := time.NewTicker(pluginHostPollEvery)
		defer t.Stop()
		for {
			s.pollPluginHosts(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (s *Server) pollPluginHosts(ctx context.Context) {
	hosts, err := s.db.ListPluginHosts(ctx)
	if err != nil {
		return
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s.pollPluginHostNow(ctx, id, false)
		}(h.ID)
	}
	wg.Wait()
}

// pluginSeverity — важность находки плагина в счётчиках хаба.
func pluginSeverity(s string) string {
	switch s {
	case "critical":
		return "critical"
	case "warning":
		return "medium"
	}
	return "info"
}

// pollPluginHostNow — опрос одного хоста плагина: метрики (доступность,
// «Мониторинг»), находки (оповещения), конфигурация (версии).
func (s *Server) pollPluginHostNow(ctx context.Context, id int64, full bool) {
	h, err := s.db.HostByID(ctx, id)
	if err != nil || !h.IsPlugin() {
		return
	}
	ctx = auth.WithUser(ctx, store.User{Username: "hub", Role: store.RoleAdmin})
	_, t, terr := s.pluginHostTypeOf(ctx, h)
	pluginHostStates.Lock()
	st := pluginHostStates.m[id]
	pluginHostStates.Unlock()
	st.ticks++
	now := time.Now()
	fail := func(err error) {
		st.Reachable, st.Error = false, msgs.Localize(msgs.DefaultLang, err)
		s.hub.recordUnreachable(ctx, id, err)
		pluginHostStates.Lock()
		pluginHostStates.m[id] = st
		pluginHostStates.Unlock()
	}
	if terr != nil {
		fail(terr)
		return
	}
	var m plugin.HostMetrics
	if t.HasSection(plugin.SectionMetrics) {
		if _, err := s.pluginHostCall(ctx, h, plugin.OpHostMetrics, nil, false, &m); err != nil {
			fail(err)
			return
		}
	} else {
		var info plugin.HostInfo
		if _, err := s.pluginHostCall(ctx, h, plugin.OpHostCheck, nil, false, &info); err != nil {
			fail(err)
			return
		}
		st.Info = &info
	}
	st.Reachable, st.Error, st.PolledAt = true, "", store.FormatTime(now)
	_ = s.db.TouchHostSeen(ctx, id)
	st.Metrics = &m
	s.pluginHostMonRows(ctx, id, &st, m, now)
	findings := st.Findings
	severeNow := map[string]string{}
	if t.HasSection(plugin.SectionFindings) && (full || st.ticks == 1 || st.ticks%pluginHostFindingsEvery == 0) {
		var out struct {
			Findings []plugin.HostFinding `json:"findings"`
		}
		if _, err := s.pluginHostCall(ctx, h, plugin.OpHostFindings, nil, false, &out); err == nil {
			findings = map[string]int{}
			for _, f := range out.Findings {
				sev := pluginSeverity(f.Severity)
				findings[sev]++
				if sev == "critical" {
					severeNow[f.ID] = f.Title
				}
			}
		}
	}
	if st.Info == nil || full || st.ticks%pluginHostFindingsEvery == 0 {
		var info plugin.HostInfo
		if _, err := s.pluginHostCall(ctx, h, plugin.OpHostCheck, nil, false, &info); err == nil {
			st.Info = &info
		}
	}
	st.Findings = findings
	pluginHostStates.Lock()
	pluginHostStates.m[id] = st
	pluginHostStates.Unlock()
	// Значок в списке хостов — по типу устройства (RouterOS, принтер…), в
	// подсказке — модель и версия с самого устройства.
	s.hub.notePluginHostPolled(ctx, id, findings, severeNow, m.UptimeS, pluginHostOS(h.PluginType, st.Info))
	if t.HasSection(plugin.SectionConfig) && (full || st.ticks == 1 || st.ticks%pluginHostConfigEvery == 0) {
		_, _ = s.pluginHostConfigFetch(ctx, h, "")
	}
}

// pluginHostMon — накопленное за час (среднее и максимум по опросам).
var pluginHostMon = struct {
	sync.Mutex
	m map[int64]map[string]*monAcc
}{m: map[int64]map[string]*monAcc{}}

type monAcc struct {
	hour     string
	sum, max float64
	n        int
}

// pluginHostMonRows — метрики опроса в часовые ряды «Мониторинга» хаба:
// те же source/metric, что у агента nkt (host cpu_pct, mem_used_bytes,
// disk used_bytes), плюс скорость интерфейсов (net rx_bps/tx_bps) и
// температура.
func (s *Server) pluginHostMonRows(ctx context.Context, id int64, st *pluginHostStateJSON, m plugin.HostMetrics, now time.Time) {
	vals := map[string]float64{}
	if m.CPUPct != nil {
		vals["host|host|cpu_pct"] = *m.CPUPct
	}
	if m.MemUsed != nil && m.MemTotal != nil {
		vals["host|host|mem_used_bytes"] = float64(*m.MemUsed)
		vals["host|host|mem_total_bytes"] = float64(*m.MemTotal)
	}
	if m.DiskUsed != nil && m.DiskSize != nil {
		vals["disk|/|used_bytes"] = float64(*m.DiskUsed)
		vals["disk|/|size_bytes"] = float64(*m.DiskSize)
	}
	if m.TempC != nil {
		vals["host|host|temp_c"] = *m.TempC
	}
	if st.prevNet != nil && !st.prevAt.IsZero() {
		dt := now.Sub(st.prevAt).Seconds()
		for name, c := range m.Net {
			p, ok := st.prevNet[name]
			if !ok || dt <= 0 || c.RX < p.RX || c.TX < p.TX {
				continue // счётчик сбросился (перезагрузка) — пропустить точку
			}
			vals["net|"+name+"|rx_bps"] = float64(c.RX-p.RX) * 8 / dt
			vals["net|"+name+"|tx_bps"] = float64(c.TX-p.TX) * 8 / dt
		}
	}
	st.prevNet, st.prevAt = m.Net, now
	if len(vals) == 0 {
		return
	}
	hour := now.UTC().Format("2006-01-02T15")
	pluginHostMon.Lock()
	acc := pluginHostMon.m[id]
	if acc == nil {
		acc = map[string]*monAcc{}
		pluginHostMon.m[id] = acc
	}
	var rows []store.MonRow
	for k, v := range vals {
		a := acc[k]
		if a == nil || a.hour != hour {
			a = &monAcc{hour: hour, max: v}
			acc[k] = a
		}
		a.sum += v
		a.n++
		if v > a.max {
			a.max = v
		}
		parts := strings.SplitN(k, "|", 3)
		avg := a.sum / float64(a.n)
		rows = append(rows, store.MonRow{Source: parts[0], Subject: parts[1], Metric: parts[2], At: hour, Avg: avg, Max: a.max, Sum: avg})
	}
	pluginHostMon.Unlock()
	if err := s.db.MonUpsertHourly(ctx, id, rows, false); err == nil {
		_ = s.db.MonRollupDaily(ctx, id, []string{hour[:10]})
	}
}

// notePluginHostPolled — удачный опрос хоста плагина: те же события, что у
// хостов с агентом (вернулся, серьёзные находки, перезагрузка) и строка
// в кэше опроса — список хостов показывает её как у всех.
func (m *Manager) notePluginHostPolled(ctx context.Context, id int64, findings map[string]int, severeNow map[string]string, uptimeS int64, os *model.OSInfo) {
	m.noteReachability(ctx, id, true, "")
	if findings != nil {
		m.noteFindings(ctx, id, findings, severeNow)
	}
	m.noteUptime(ctx, id, uptimeS)
	now := time.Now()
	m.overviewMu.Lock()
	defer m.overviewMu.Unlock()
	delete(m.pollFails, id)
	if os != nil {
		m.hostOS[id] = os
	}
	cur := m.overview[id]
	cur.reachable = true
	if findings != nil {
		cur.findings = findings
		cur.severe = severeNow
	}
	cur.uptimeS = uptimeS
	cur.lastPolledAt, cur.lastCheckedAt, cur.errMsg = now, now, ""
	m.overview[id] = cur
}
