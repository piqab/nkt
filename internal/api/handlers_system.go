package api

import (
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"os/exec"
	"strings"

	"github.com/piqab/nkt/internal/auth"
)

// handleHardware — сводка по железу: машина, процессор, память, батареи,
// температуры, устройства. Только чтение.
func (s *Server) handleHardware(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.hardware.Collect(r.Context()))
}

// handleSystemSettings отдаёт имя машины, часовой пояс, локаль и
// состояние автоматических обновлений.
func (s *Server) handleSystemSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sysconfig.Read(r.Context()))
}

// handleTimezones — список поясов для выбора.
func (s *Server) handleTimezones(w http.ResponseWriter, r *http.Request) {
	zones, err := s.sysconfig.Timezones(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"timezones": zones})
}

type systemSettingsRequest struct {
	Hostname string `json:"hostname"`
	Timezone string `json:"timezone"`
	NTP      *bool  `json:"ntp"`
}

// handleSystemSettingsUpdate меняет то, что пришло: каждое поле
// необязательное, и форма отправляет только изменённое.
func (s *Server) handleSystemSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req systemSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())

	if req.Hostname != "" {
		err := s.sysconfig.SetHostname(r.Context(), req.Hostname)
		s.db.Audit(r.Context(), user, "system.hostname", req.Hostname, auditResult(err), errText(err))
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	if req.Timezone != "" {
		err := s.sysconfig.SetTimezone(r.Context(), req.Timezone)
		s.db.Audit(r.Context(), user, "system.timezone", req.Timezone, auditResult(err), errText(err))
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	if req.NTP != nil {
		err := s.sysconfig.SetNTP(r.Context(), *req.NTP)
		s.db.Audit(r.Context(), user, "system.ntp", "", auditResult(err), errText(err))
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.sysconfig.Read(r.Context()))
}

// handleNetworkManager отдаёт соединения, устройства и найденные сети.
func (s *Server) handleNetworkManager(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.netmanager.State(r.Context()))
}

type nmConnectionRequest struct {
	UUID string `json:"uuid"`
	Up   bool   `json:"up"`
}

// handleNetworkConnection поднимает или гасит соединение.
func (s *Server) handleNetworkConnection(w http.ResponseWriter, r *http.Request) {
	var req nmConnectionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	err := s.netmanager.Connection(r.Context(), req.UUID, req.Up)
	s.db.Audit(r.Context(), user, "network.connection", req.UUID, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.netmanager.State(r.Context()))
}

type wifiConnectRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
}

// handleWiFiConnect подключается к беспроводной сети.
func (s *Server) handleWiFiConnect(w http.ResponseWriter, r *http.Request) {
	var req wifiConnectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	err := s.netmanager.ConnectWiFi(r.Context(), req.SSID, req.Password)
	// В журнал идёт только имя сети: пароль не должен попасть ни в аудит,
	// ни в сообщение об ошибке.
	s.db.Audit(r.Context(), user, "network.wifi", req.SSID, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.netmanager.State(r.Context()))
}

// handleSandboxPackages — установленное из snap и flatpak.
func (s *Server) handleSandboxPackages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sandboxpkg.List(r.Context()))
}

type sandboxPackageRequest struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// handleSandboxPackageRemove удаляет один пакет snap или flatpak.
func (s *Server) handleSandboxPackageRemove(w http.ResponseWriter, r *http.Request) {
	var req sandboxPackageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	err := s.sandboxpkg.Remove(r.Context(), req.Kind, req.Name)
	s.db.Audit(r.Context(), user, "package."+req.Kind+".remove", req.Name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sandboxpkg.List(r.Context()))
}

// handleSandboxPackageUpdate обновляет всё установленное в одной из двух
// систем.
func (s *Server) handleSandboxPackageUpdate(w http.ResponseWriter, r *http.Request) {
	var req sandboxPackageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	out, err := s.sandboxpkg.Update(r.Context(), req.Kind)
	s.db.Audit(r.Context(), user, "package."+req.Kind+".update", "", auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": out, "packages": s.sandboxpkg.List(r.Context())})
}

// handleLocales — все известные системе локали с отметками.
func (s *Server) handleLocales(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sysconfig.Locales(r.Context()))
}

type localesRequest struct {
	Generate []string `json:"generate"`
	Default  string   `json:"default"`
}

// handleLocalesUpdate генерирует выбранные локали и/или назначает
// основную.
func (s *Server) handleLocalesUpdate(w http.ResponseWriter, r *http.Request) {
	var req localesRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	if len(req.Generate) > 0 {
		err := s.sysconfig.GenerateLocales(r.Context(), req.Generate)
		s.db.Audit(r.Context(), user, "system.locale.generate", strings.Join(req.Generate, " "), auditResult(err), errText(err))
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	if req.Default != "" {
		err := s.sysconfig.SetLocale(r.Context(), req.Default)
		s.db.Audit(r.Context(), user, "system.locale", req.Default, auditResult(err), errText(err))
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.sysconfig.Locales(r.Context()))
}

// handleTimeSync — служба времени, серверы и состояние сверки.
func (s *Server) handleTimeSync(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sysconfig.TimeSync(r.Context()))
}

type timeSyncRequest struct {
	Servers *[]string `json:"servers"`
	SyncNow bool      `json:"sync_now"`
}

// handleTimeSyncUpdate меняет серверы и/или запускает сверку.
func (s *Server) handleTimeSyncUpdate(w http.ResponseWriter, r *http.Request) {
	var req timeSyncRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	if req.Servers != nil {
		err := s.sysconfig.SetTimeSyncServers(r.Context(), *req.Servers)
		s.db.Audit(r.Context(), user, "system.timesync.servers", strings.Join(*req.Servers, " "), auditResult(err), errText(err))
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	if req.SyncNow {
		err := s.sysconfig.SyncNow(r.Context())
		s.db.Audit(r.Context(), user, "system.timesync.now", "", auditResult(err), errText(err))
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.sysconfig.TimeSync(r.Context()))
}

// handleSandboxPackagesWS — GET /system/sandbox-packages/ws?op=remove&snap=a,b&flatpak=c
// (или op=update&snap=1&flatpak=1): удаление или обновление в окне
// выполнения с живым выводом, как у apt.
func (s *Server) handleSandboxPackagesWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	q := r.URL.Query()
	op := q.Get("op")
	split := func(v string) []string {
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	var script string
	var err error
	if op == "update" {
		script, err = control.SandboxScript(op, nil, nil, q.Get("snap") == "1", q.Get("flatpak") == "1")
	} else {
		script, err = control.SandboxScript(op, split(q.Get("snap")), split(q.Get("flatpak")), false, false)
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	buildCmd := func() *exec.Cmd {
		return unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, "bash", "-c", script)
	}
	s.runUpdateSession(w, r, "sandbox-pkg", buildCmd, "sandbox_pkg."+op, q.Get("snap")+" "+q.Get("flatpak"), s.cfg.TerminalIdleTimeout)
}

// handleSandboxPackagesStatus — итог последней сессии для окна.
func (s *Server) handleSandboxPackagesStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("sandbox-pkg")
	writeSessionStatus(w, active, finished, exitCode)
}
