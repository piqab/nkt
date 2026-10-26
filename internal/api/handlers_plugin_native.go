package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/msgs"
)

// Плагин хаба как служба systemd, без Docker («О системе» → «Плагины» →
// «Установить на хост»): файл плагина, настройки и юнит кладёт хаб этим
// вызовом. Секреты плагина (токен, секрет подписи) приходят только телом
// запроса и пишутся в файл 0600 — без истории конфигураций и не в
// параметры задания. Служба — от DynamicUser, плагин слушает 127.0.0.1.

const (
	pluginNativeDir  = "/opt/nkt-plugins"
	pluginNativeEnv  = "/etc/nkt-plugins"
	pluginNativeUnit = "/etc/systemd/system/nkt-plugin-%s.service"
)

var (
	pluginNativeNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	pluginNativeFileRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}\.py$`)
	pluginNativeEnvRe  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
)

// pluginNativeRuns — чем запускать (пока Python без зависимостей).
var pluginNativeRuns = map[string]string{"python3": "/usr/bin/env python3 -I"}

type pluginNativeReq struct {
	Name    string `json:"name"`
	Run     string `json:"run"`
	File    string `json:"file"`
	Content string `json:"content"`
	Port    int    `json:"port"`
	// Docker — плагину нужен доступ к Docker (служба в группе docker).
	Docker bool `json:"docker,omitempty"`
	// Env — настройки плагина; nil — оставить прежние (обновление файла).
	Env map[string]string `json:"env,omitempty"`
}

// pluginNativeUnitText — юнит службы; user пусто — DynamicUser, иначе
// постоянный системный пользователь (см. pluginNativeUser).
func pluginNativeUnitText(name, run, file string, docker bool, user string) string {
	groups := ""
	if docker {
		// Сокет Docker — группе docker: плагин управляет контейнерами
		// (docker-mailserver). Это права root — хаб предупреждает до установки.
		groups = "SupplementaryGroups=docker\n"
	}
	account := "DynamicUser=yes\n"
	if user != "" {
		account = "User=" + user + "\nGroup=" + user + "\n"
	}
	return fmt.Sprintf(`# nkt: плагин хаба %[1]s («О системе» → «Плагины»). Правит и удаляет хаб.
[Unit]
Description=nkt hub plugin %[1]s
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%[2]s %[3]s/%[1]s/%[4]s
EnvironmentFile=%[5]s/%[1]s.env
%[7]s# Свой каталог данных (профили connector и т. п.): systemd создаёт его и
# передаёт путь в STATE_DIRECTORY; остальная система — только для чтения.
StateDirectory=nkt-plugin-%[1]s
%[6]sNoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, name, pluginNativeRuns[run], pluginNativeDir, file, pluginNativeEnv, groups, account)
}

// pluginNativeUser — системный пользователь службы плагина там, где
// DynamicUser не годится: uid временного пользователя systemd без модуля
// NSS «systemd» (libnss-systemd) ни на какое имя не отображается, и ssh
// внутри плагина (windows, connector) падает с «No user exists for uid …».
// Пусто — DynamicUser годится. Имя не длиннее 32 знаков (предел useradd).
func pluginNativeUser(c collect.Collector, name string) string {
	if raw, err := c.ReadFile("/etc/nsswitch.conf"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if f := strings.Fields(line); len(f) > 1 && f[0] == "passwd:" && slices.Contains(f[1:], "systemd") {
				return ""
			}
		}
	}
	return pluginNativeUserName(name)
}

func pluginNativeUserName(name string) string {
	user := "nktp-" + name
	if len(user) > 32 {
		sum := sha256.Sum256([]byte(name))
		user = user[:23] + "-" + hex.EncodeToString(sum[:4])
	}
	return user
}

// handlePluginNativeInstall — POST /plugins/native: поставить или обновить
// (Env nil — настройки как были) и перезапустить службу.
func (s *Server) handlePluginNativeInstall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req pluginNativeReq
	if err := decodeJSONLimit(r, &req, 4<<20); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !pluginNativeNameRe.MatchString(req.Name) || !pluginNativeFileRe.MatchString(req.File) || pluginNativeRuns[req.Run] == "" ||
		req.Content == "" || req.Port < 1 || req.Port > 65535 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeBad"))
		return
	}
	for k, v := range req.Env {
		// EnvironmentFile: одна строка KEY=value, без кавычек и подстановок.
		if !pluginNativeEnvRe.MatchString(k) || strings.ContainsAny(v, "\r\n\"'\\$`") {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeBadEnv", k))
			return
		}
	}
	c := s.scanner.Collector()
	if !collect.Which(ctx, c, "python3") {
		writeError(w, http.StatusConflict, msgs.Tc(ctx, "plugin.nativeNoPython"))
		return
	}
	if req.Docker {
		if res, err := c.Run(ctx, "getent", "group", "docker"); err != nil || !res.OK() {
			writeError(w, http.StatusConflict, msgs.Tc(ctx, "plugin.nativeNoDocker"))
			return
		}
	}
	unit := fmt.Sprintf(pluginNativeUnit, req.Name)
	dir := pluginNativeDir + "/" + req.Name
	if err := c.Mkdir(dir); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if err := c.WriteFile(dir+"/"+req.File, []byte(req.Content), 0o644); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if req.Env != nil {
		env := map[string]string{}
		for k, v := range req.Env {
			env[k] = v
		}
		// Только 127.0.0.1: хаб ходит по SSH, наружу плагин не нужен.
		env["NKT_PLUGIN_LISTEN"] = "127.0.0.1"
		env["NKT_PLUGIN_PORT"] = fmt.Sprint(req.Port)
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("# nkt: настройки плагина хаба " + req.Name + " — секреты, только root.\n")
		for _, k := range keys {
			b.WriteString(k + "=" + env[k] + "\n")
		}
		if err := c.Mkdir(pluginNativeEnv); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		if err := c.WriteFile(pluginNativeEnv+"/"+req.Name+".env", []byte(b.String()), 0o600); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
	} else if _, err := c.ReadFile(pluginNativeEnv + "/" + req.Name + ".env"); err != nil {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeNoEnv", req.Name))
		return
	}
	user := pluginNativeUser(c, req.Name)
	if user != "" {
		if res, err := c.Run(ctx, "getent", "passwd", user); err != nil || !res.OK() {
			res, err := c.Run(ctx, "useradd", "--system", "--user-group", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", user)
			if err == nil && !res.OK() {
				err = fmt.Errorf("%s", strings.TrimSpace(res.Output()))
			}
			if err != nil {
				writeErr(w, r, http.StatusInternalServerError, msgs.Errorf("plugin.nativeUser", user, err))
				return
			}
		}
	}
	if err := c.WriteFile(unit, []byte(pluginNativeUnitText(req.Name, req.Run, req.File, req.Docker, user)), 0o644); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	service := "nkt-plugin-" + req.Name
	for _, args := range [][]string{{"daemon-reload"}, {"enable", service}, {"restart", service}} {
		res, err := c.Run(ctx, "systemctl", args...)
		if err == nil && !res.OK() {
			err = fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), strings.TrimSpace(res.Output()))
		}
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
	}
	s.db.Audit(ctx, auth.Username(ctx), "plugin.native.install", req.Name, "ok", map[string]any{"file": req.File, "env_set": req.Env != nil})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": service})
}

// handlePluginNativeRemove — POST /plugins/native/remove {name}: служба
// остановлена и выключена, файлы плагина, настроек и юнита удалены.
func (s *Server) handlePluginNativeRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil || !pluginNativeNameRe.MatchString(req.Name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeBad"))
		return
	}
	c := s.scanner.Collector()
	service := "nkt-plugin-" + req.Name
	_, _ = c.Run(ctx, "systemctl", "disable", "--now", service)
	for _, p := range []string{fmt.Sprintf(pluginNativeUnit, req.Name), pluginNativeEnv + "/" + req.Name + ".env", pluginNativeEnv + "/" + req.Name + ".env.prev"} {
		_ = c.DeleteFile(p)
	}
	_, _ = c.Run(ctx, "systemctl", "daemon-reload")
	// Пользователь службы, если его заводил хаб (без DynamicUser).
	user := pluginNativeUserName(req.Name)
	if res, err := c.Run(ctx, "getent", "passwd", user); err == nil && res.OK() {
		_, _ = c.Run(ctx, "userdel", user)
	}
	// Каталог /opt/nkt-plugins под ProtectSystem=strict агента закрыт —
	// RemoveAll удаляет его вне песочницы.
	dir := pluginNativeDir + "/" + req.Name
	if err := c.RemoveAll(dir); err != nil {
		writeErr(w, r, http.StatusInternalServerError, msgs.Errorf("plugin.nativeRemoveDir", dir, err))
		return
	}
	s.db.Audit(ctx, auth.Username(ctx), "plugin.native.remove", req.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// pluginNativeEnvRead — настройки плагина с хоста (KEY=value построчно).
func pluginNativeEnvRead(ctx context.Context, c collect.Collector, path string) (map[string]string, error) {
	raw, err := c.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && pluginNativeEnvRe.MatchString(k) {
			env[k] = v
		}
	}
	return env, nil
}

func pluginNativeEnvText(name string, env map[string]string) []byte {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# nkt: настройки плагина хаба " + name + " — секреты, только root.\n")
	for _, k := range keys {
		b.WriteString(k + "=" + env[k] + "\n")
	}
	return []byte(b.String())
}

// pluginNativeHubKey — настройки, которые ведёт хаб (секрет подписи, адрес
// и порт): окно «Настройки» их не видит и не меняет.
func pluginNativeHubKey(k string) bool { return strings.HasPrefix(k, "NKT_") }

// handlePluginNativeSettings — POST /plugins/native/settings {name, op}:
//
//   - read {keys, secrets} — значения keys (несекретные, их называет хаб) и
//     есть ли значения у secrets — самих секретов в ответе нет;
//   - apply {set} — заменить значения (пусто — убрать), прежний файл — в
//     .env.prev, перезапуск службы;
//   - rollback — вернуть .env.prev и перезапустить (плагин не поднялся),
//     в ответе — последние строки журнала службы;
//   - commit — плагин ответил: .env.prev удалить.
func (s *Server) handlePluginNativeSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Name    string            `json:"name"`
		Op      string            `json:"op"`
		Keys    []string          `json:"keys"`
		Secrets []string          `json:"secrets"`
		Set     map[string]string `json:"set"`
	}
	if err := decodeJSONLimit(r, &req, 1<<20); err != nil || !pluginNativeNameRe.MatchString(req.Name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeBad"))
		return
	}
	c := s.scanner.Collector()
	path := pluginNativeEnv + "/" + req.Name + ".env"
	prev := path + ".prev"
	service := "nkt-plugin-" + req.Name
	restart := func() error {
		res, err := c.Run(ctx, "systemctl", "restart", service)
		if err == nil && !res.OK() {
			err = fmt.Errorf("systemctl restart %s: %s", service, strings.TrimSpace(res.Output()))
		}
		return err
	}
	env, err := pluginNativeEnvRead(ctx, c, path)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeNoEnv", req.Name))
		return
	}
	switch req.Op {
	case "read":
		values, set := map[string]string{}, map[string]bool{}
		for _, k := range req.Keys {
			if v, ok := env[k]; ok && pluginNativeEnvRe.MatchString(k) && !pluginNativeHubKey(k) {
				values[k] = v
			}
		}
		for _, k := range req.Secrets {
			set[k] = env[k] != ""
		}
		writeJSON(w, http.StatusOK, map[string]any{"values": values, "set": set})
	case "apply":
		for k, v := range req.Set {
			if !pluginNativeEnvRe.MatchString(k) || pluginNativeHubKey(k) || strings.ContainsAny(v, "\r\n\"'\\$`") {
				writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeBadEnv", k))
				return
			}
		}
		if err := c.WriteFile(prev, pluginNativeEnvText(req.Name, env), 0o600); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		changed := make([]string, 0, len(req.Set))
		for k, v := range req.Set {
			if v == "" {
				delete(env, k)
			} else {
				env[k] = v
			}
			changed = append(changed, k)
		}
		sort.Strings(changed)
		if err := c.WriteFile(path, pluginNativeEnvText(req.Name, env), 0o600); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		if err := restart(); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		s.db.Audit(ctx, auth.Username(ctx), "plugin.native.settings", req.Name, "ok", map[string]any{"keys": changed})
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case "rollback":
		old, err := pluginNativeEnvRead(ctx, c, prev)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeNoPrev", req.Name))
			return
		}
		if err := c.WriteFile(path, pluginNativeEnvText(req.Name, old), 0o600); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		_ = c.DeleteFile(prev)
		tail := ""
		if res, err := c.Run(ctx, "journalctl", "-u", service, "-n", "15", "--no-pager", "-o", "cat"); err == nil && res.OK() {
			tail = strings.TrimSpace(res.Stdout)
		}
		if err := restart(); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		s.db.Audit(ctx, auth.Username(ctx), "plugin.native.settingsRollback", req.Name, "ok", nil)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "log": tail})
	case "commit":
		_ = c.DeleteFile(prev)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("plugin.nativeBad"))
	}
}
