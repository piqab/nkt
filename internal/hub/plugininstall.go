package hub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/examples"
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Установка плагина на хост без Docker («О системе» → «Плагины» →
// «Установить на хост»): плагин из встроенного каталога (examples/*/deploy/
// nkt-plugin.json) ставится службой systemd через агент nkt хоста
// (/api/plugins/native) и подключается к хабу. Секреты (настройки с
// secret и секрет подписи, который создаёт хаб) — не в параметрах
// задания: в одноразовом хранилище памяти хаба, задание забирает их по
// жетону.

// KindPluginInstall — задание «установить, обновить или удалить плагин на
// хосте».
const KindPluginInstall = "plugin.install"

// PluginInstallParams — параметры задания (без секретов).
type PluginInstallParams struct {
	Package  string `json:"package"`
	HostID   int64  `json:"host_id"`
	HostName string `json:"host_name"`
	Name     string `json:"name"`
	// Action — install | update | remove | update_all | settings.
	Action string `json:"action"`
	// Port — порт плагина на 127.0.0.1 хоста (у второго экземпляра на том
	// же хосте — следующий свободный).
	Port int `json:"port,omitempty"`
	// Ticket — жетон настроек в oneTime (install).
	Ticket string `json:"ticket,omitempty"`
	// Names — update_all: какие плагины обновить; settings: какие
	// настройки меняются (только имена — значения в oneTime).
	Names []string `json:"names,omitempty"`
}

// pluginStartWait — сколько ждать, пока плагин после запуска службы ответит.
var pluginStartWait = 45 * time.Second

// hubInContainer — хаб работает в контейнере: на машину хаба плагин
// службой не поставить (в контейнере нет systemd).
var hubInContainer = func() bool {
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

// packageSHA — отпечаток файла плагина в каталоге: другой у поставленного —
// «есть версия новее».
func packageSHA(p examples.PluginPackage) string {
	sum := sha256.Sum256(p.Content)
	return hex.EncodeToString(sum[:8])
}

// nativeOutdated — плагин поставлен хабом, а в каталоге хаба файл новее.
func nativeOutdated(p pluginConf) bool {
	if p.Native == "" {
		return false
	}
	pkg, ok := pluginPackage(p.Native)
	return ok && p.NativeSHA != packageSHA(pkg)
}

func pluginPackage(key string) (examples.PluginPackage, bool) {
	for _, p := range examples.PluginPackages() {
		if p.Key == key {
			return p, true
		}
	}
	return examples.PluginPackage{}, false
}

func pick(m map[string]string, lang string) string {
	if v := m[lang]; v != "" {
		return v
	}
	return m["ru"]
}

// handlePluginCatalog — GET /hub/plugins/catalog: что можно поставить на
// хост без Docker (на языке запроса).
func (s *Server) handlePluginCatalog(w http.ResponseWriter, r *http.Request) {
	lang := string(msgs.FromContext(r.Context()))
	type field struct {
		Env      string `json:"env"`
		Title    string `json:"title"`
		Hint     string `json:"hint,omitempty"`
		Default  string `json:"default,omitempty"`
		Secret   bool   `json:"secret,omitempty"`
		Required bool   `json:"required,omitempty"`
	}
	type pkg struct {
		Key         string  `json:"key"`
		Name        string  `json:"name"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Run         string  `json:"run"`
		Port        int     `json:"port"`
		Docker      bool    `json:"docker,omitempty"`
		Settings    []field `json:"settings"`
	}
	out := []pkg{}
	for _, p := range examples.PluginPackages() {
		item := pkg{Key: p.Key, Name: p.Name, Title: pick(p.Title, lang), Description: pick(p.Description, lang), Run: p.Run, Port: p.Port, Docker: p.Docker, Settings: []field{}}
		for _, f := range p.Settings {
			item.Settings = append(item.Settings, field{Env: f.Env, Title: pick(f.Title, lang), Hint: pick(f.Hint, lang), Default: f.Default, Secret: f.Secret, Required: f.Required})
		}
		out = append(out, item)
	}
	// На машину хаба — только если хаб не в контейнере.
	writeJSON(w, http.StatusOK, map[string]any{"packages": out, "local_ok": !hubInContainer()})
}

// handlePluginInstall — POST /hub/plugins/install {package, host, name,
// settings}: задание установки.
func (s *Server) handlePluginInstall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Package  string            `json:"package"`
		Host     string            `json:"host"`
		Name     string            `json:"name"`
		Settings map[string]string `json:"settings"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	pkg, ok := pluginPackage(req.Package)
	if !ok {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.unknownPackage", req.Package))
		return
	}
	name := firstNonEmpty(req.Name, pkg.Name)
	if !validPluginName(name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.badName", name))
		return
	}
	if p, taken := s.pluginByName(ctx, name); taken && (p.Native == "" || p.Host != req.Host) {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("plugin.nameTaken", name))
		return
	}
	targets, err := s.resolveHosts(ctx, []string{req.Host}, "")
	if err != nil || len(targets) != 1 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.hostGone", req.Host))
		return
	}
	if targets[0].ID == localHostID && hubInContainer() {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.localInDocker"))
		return
	}
	// Порт: свой у каждого плагина на хосте (второй экземпляр — следующий).
	host := targets[0].Name
	if targets[0].ID == localHostID {
		host = localHostName
	}
	used := map[int]bool{}
	for _, o := range s.plugins(ctx) {
		if o.Host == host && o.Name != name {
			used[o.Port] = true
		}
	}
	port := pkg.Port
	for used[port] {
		port++
	}
	env := map[string]string{}
	for _, f := range pkg.Settings {
		v := strings.TrimSpace(req.Settings[f.Env])
		if v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.settingRequired", pick(f.Title, string(msgs.FromContext(ctx)))))
			return
		}
		if strings.ContainsAny(v, "\r\n\"'\\$`") {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeBadEnv", f.Env))
			return
		}
		if v != "" {
			env[f.Env] = v
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	env["NKT_PLUGIN_SECRET"] = hex.EncodeToString(secret)
	raw, _ := json.Marshal(env)
	ticket := oneTime.Put(raw)
	wipe(raw)
	s.startPluginJob(w, r, PluginInstallParams{Package: pkg.Key, HostID: targets[0].ID, HostName: targets[0].Name, Name: name, Action: "install", Port: port, Ticket: ticket}, "plugin.installTitle")
}

func validPluginName(n string) bool {
	return len(n) >= 2 && len(n) <= 32 && n[0] >= 'a' && n[0] <= 'z' && strings.Trim(n, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

// handlePluginNativeAction — POST /hub/plugins/{name}/native/{action}:
// update — файл плагина версии хаба, перезапуск; remove — служба и файлы с
// хоста, подключение снимается.
func (s *Server) handlePluginNativeAction(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pluginFromReq(w, r)
	if !ok {
		return
	}
	action := chi.URLParam(r, "action")
	if p.Native == "" || (action != "update" && action != "remove") {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.notNative", p.Name))
		return
	}
	targets, err := s.resolveHosts(r.Context(), []string{p.Host}, "")
	if err != nil || len(targets) != 1 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.hostGone", p.Host))
		return
	}
	title := "plugin.updateTitle"
	if action == "remove" {
		title = "plugin.removeTitle"
	}
	s.startPluginJob(w, r, PluginInstallParams{Package: p.Native, HostID: targets[0].ID, HostName: targets[0].Name, Name: p.Name, Action: action, Port: p.Port}, title)
}

// handlePluginUpdateAll — POST /hub/plugins/native/update-all: одним
// заданием обновить все плагины, у которых в каталоге хаба файл новее.
func (s *Server) handlePluginUpdateAll(w http.ResponseWriter, r *http.Request) {
	var names []string
	for _, p := range s.plugins(r.Context()) {
		if nativeOutdated(p) {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nothingToUpdate"))
		return
	}
	s.startPluginJob(w, r, PluginInstallParams{Name: strings.Join(names, ", "), Action: "update_all", Names: names}, "plugin.updateAllTitle")
}

func (s *Server) startPluginJob(w http.ResponseWriter, r *http.Request, p PluginInstallParams, title string) {
	ctx := r.Context()
	user := auth.Username(ctx)
	args := []any{p.Name, p.HostName}
	if p.Action == "update_all" {
		args = []any{p.Name}
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindPluginInstall, TitleKey: title, TitleArgs: args,
		Queue: "plugin:" + p.Name, Author: user, Steps: 3, Params: p})
	s.db.Audit(ctx, user, "plugin."+p.Action, p.Name, auditOutcome(err), map[string]any{"host": p.HostName, "package": p.Package})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// PluginInstallRunner — исполнитель задания.
type PluginInstallRunner struct{ s *Server }

// NewPluginInstallRunner — исполнитель.
func NewPluginInstallRunner(s *Server) *PluginInstallRunner { return &PluginInstallRunner{s: s} }

// Run: файлы и служба на хосте (python3 нет — пакет), ожидание плагина,
// подключение; обновление — только файл и перезапуск; удаление — служба,
// файлы, подключение.
func (r *PluginInstallRunner) Run(ctx context.Context, jc *jobs.Context) error {
	s := r.s
	var p PluginInstallParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	user := s.actingUser(ctx, jc.Job.Author, s.firstAdmin(ctx))
	ctx = auth.WithUser(ctx, store.User{Username: jc.Job.Author, Role: store.RoleAdmin})
	if p.Action == "update_all" {
		for i, name := range p.Names {
			conf, ok := s.pluginByName(ctx, name)
			if !ok || conf.Native == "" {
				continue
			}
			targets, err := s.resolveHosts(ctx, []string{conf.Host}, "")
			if err != nil || len(targets) != 1 {
				return msgs.Errorf("plugin.hostGone", conf.Host)
			}
			jc.StepKey(i+1, len(p.Names), "plugin.stepFiles", name, conf.Host)
			if err := r.installFiles(ctx, jc, user, PluginInstallParams{Package: conf.Native, HostID: targets[0].ID, HostName: targets[0].Name,
				Name: name, Action: "update", Port: conf.Port}, nil); err != nil {
				return err
			}
			var m pluginManifestWait
			if err := s.waitPluginManifest(ctx, conf, &m); err != nil {
				return err
			}
			jc.Log("plugin.updated", name, m.version)
		}
		return nil
	}
	if p.Action == "settings" {
		return r.runSettings(ctx, jc, user, p)
	}
	if p.Action == "remove" {
		jc.StepKey(1, 2, "plugin.stepRemove", p.Name, p.HostName)
		if _, err := s.hostCall(ctx, user, p.HostID, "POST", "/api/plugins/native/remove", map[string]string{"name": p.Name}, nil); err != nil {
			return err
		}
		jc.StepKey(2, 2, "plugin.stepDisconnect", p.Name)
		return s.disconnectPlugin(ctx, p.Name)
	}
	pkg, ok := pluginPackage(p.Package)
	if !ok {
		return msgs.Errorf("plugin.unknownPackage", p.Package)
	}
	if p.Port == 0 {
		p.Port = pkg.Port
	}
	var env map[string]string
	if p.Action == "install" {
		raw, ok := oneTime.Take(p.Ticket)
		if !ok {
			// Хаб перезапускался или прошло больше 15 минут: секретов нет.
			return msgs.Errorf("plugin.ticketGone")
		}
		err := json.Unmarshal(raw, &env)
		wipe(raw)
		if err != nil {
			return err
		}
	}
	jc.StepKey(1, 3, "plugin.stepFiles", p.Name, p.HostName)
	if err := r.installFiles(ctx, jc, user, p, env); err != nil {
		return err
	}
	if p.Action == "update" {
		jc.StepKey(2, 3, "plugin.stepWait", p.Name)
		forgetManifests(p.Name)
		conf, _ := s.pluginByName(ctx, p.Name)
		var m pluginManifestWait
		if err := s.waitPluginManifest(ctx, conf, &m); err != nil {
			return err
		}
		jc.StepKey(3, 3, "plugin.updated", p.Name, m.version)
		return nil
	}
	jc.StepKey(2, 3, "plugin.stepWait", p.Name)
	conf := pluginConf{Host: p.HostName, Port: p.Port, Native: pkg.Key, NativeSHA: packageSHA(pkg), SettingKeys: []string{}}
	for k := range env {
		if !strings.HasPrefix(k, "NKT_") {
			conf.SettingKeys = append(conf.SettingKeys, k)
		}
	}
	slices.Sort(conf.SettingKeys)
	if p.HostID == localHostID {
		conf.Host = localHostName
	}
	secret := env["NKT_PLUGIN_SECRET"]
	defer func() { env = nil }()
	var last error
	deadline := time.Now().Add(pluginStartWait)
	for {
		m, code, err := s.connectPlugin(ctx, conf, secret, pluginConnectReq{Name: p.Name})
		if err == nil {
			jc.StepKey(3, 3, "plugin.connected", p.Name, m.Version)
			return nil
		}
		last = err
		// Плагин ещё поднимается — подождать; другая ошибка — сразу.
		if code != http.StatusBadGateway || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return msgs.Errorf("plugin.connectFailed", p.Name, last)
}

// installFiles — файл плагина (env nil — настройки прежние) и служба на
// хосте; нет python3 — пакет профилем и ещё раз. Обновлённому — отпечаток
// файла (пометка «есть версия новее» снимается).
func (r *PluginInstallRunner) installFiles(ctx context.Context, jc *jobs.Context, user string, p PluginInstallParams, env map[string]string) error {
	s := r.s
	pkg, ok := pluginPackage(p.Package)
	if !ok {
		return msgs.Errorf("plugin.unknownPackage", p.Package)
	}
	body := map[string]any{"name": p.Name, "run": pkg.Run, "file": pkg.File, "content": string(pkg.Content), "port": p.Port, "docker": pkg.Docker}
	if env != nil {
		body["env"] = env
	}
	_, err := s.hostCall(ctx, user, p.HostID, "POST", "/api/plugins/native", body, nil)
	if isNoPython(err) {
		jc.Log("plugin.installPython", p.HostName)
		sr := &ScriptRunner{m: s.hub, s: s}
		h, herr := s.hostRecord(ctx, p.HostID)
		if herr != nil {
			return herr
		}
		if err := sr.applyMini(ctx, jc, h, "plugin-"+p.Name, map[string]any{"packages": []string{"python3"}}); err != nil {
			return err
		}
		_, err = s.hostCall(ctx, user, p.HostID, "POST", "/api/plugins/native", body, nil)
	}
	if err != nil {
		return err
	}
	jc.Log("plugin.serviceStarted", "nkt-plugin-"+p.Name, p.HostName)
	if p.Action == "update" {
		pluginsMu.Lock()
		list := s.plugins(ctx)
		for i := range list {
			if list[i].Name == p.Name {
				list[i].NativeSHA = packageSHA(pkg)
			}
		}
		err := s.savePlugins(ctx, list)
		pluginsMu.Unlock()
		forgetManifests(p.Name)
		return err
	}
	return nil
}

func isNoPython(err error) bool {
	if err == nil {
		return false
	}
	t := err.Error()
	return strings.Contains(t, msgs.T(msgs.RU, "plugin.nativeNoPython")) || strings.Contains(t, msgs.T(msgs.EN, "plugin.nativeNoPython"))
}

// pluginManifestWait — версия плагина после обновления.
type pluginManifestWait struct{ version string }

func (s *Server) waitPluginManifest(ctx context.Context, conf pluginConf, out *pluginManifestWait) error {
	deadline := time.Now().Add(pluginStartWait)
	for {
		forgetManifests(conf.Name)
		m, err := s.pluginManifest(ctx, conf)
		if err == nil {
			out.version = m.Version
			return nil
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("plugin.connectFailed", conf.Name, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// hostRecord — хост по id (машина хаба — запись localhost).
func (s *Server) hostRecord(ctx context.Context, id int64) (store.Host, error) {
	if id == localHostID {
		return store.Host{ID: localHostID, Name: localHostName}, nil
	}
	return s.db.HostByID(ctx, id)
}

// disconnectPlugin — снять подключение (и токен плагина).
func (s *Server) disconnectPlugin(ctx context.Context, name string) error {
	pluginsMu.Lock()
	defer pluginsMu.Unlock()
	list := s.plugins(ctx)
	i := slices.IndexFunc(list, func(p pluginConf) bool { return p.Name == name })
	if i < 0 {
		return nil
	}
	if list[i].Token != "" {
		s.dropPluginToken(ctx, list[i].Token)
	}
	list = slices.Delete(list, i, i+1)
	err := s.savePlugins(ctx, list)
	s.db.Audit(ctx, auth.Username(ctx), "plugin.disconnect", name, auditOutcome(err), nil)
	if err != nil {
		return err
	}
	forgetManifests(name)
	s.refreshScriptWords(ctx)
	return nil
}
