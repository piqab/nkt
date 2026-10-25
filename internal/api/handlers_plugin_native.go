package api

import (
	"fmt"
	"net/http"
	"regexp"
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
	// Env — настройки плагина; nil — оставить прежние (обновление файла).
	Env map[string]string `json:"env,omitempty"`
}

func pluginNativeUnitText(name, run, file string) string {
	return fmt.Sprintf(`# nkt: плагин хаба %[1]s («О системе» → «Плагины»). Правит и удаляет хаб.
[Unit]
Description=nkt hub plugin %[1]s
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%[2]s %[3]s/%[1]s/%[4]s
EnvironmentFile=%[5]s/%[1]s.env
DynamicUser=yes
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, name, pluginNativeRuns[run], pluginNativeDir, file, pluginNativeEnv)
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
	if err := c.WriteFile(unit, []byte(pluginNativeUnitText(req.Name, req.Run, req.File)), 0o644); err != nil {
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
	for _, p := range []string{fmt.Sprintf(pluginNativeUnit, req.Name), pluginNativeEnv + "/" + req.Name + ".env"} {
		_ = c.DeleteFile(p)
	}
	if res, err := c.Run(ctx, "rm", "-rf", "--", pluginNativeDir+"/"+req.Name); err == nil && !res.OK() {
		writeError(w, http.StatusInternalServerError, strings.TrimSpace(res.Output()))
		return
	}
	_, _ = c.Run(ctx, "systemctl", "daemon-reload")
	s.db.Audit(ctx, auth.Username(ctx), "plugin.native.remove", req.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
