package api

import (
	"net/http"

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
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())

	if req.Hostname != "" {
		err := s.sysconfig.SetHostname(r.Context(), req.Hostname)
		s.db.Audit(r.Context(), user, "system.hostname", req.Hostname, auditResult(err), errText(err))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Timezone != "" {
		err := s.sysconfig.SetTimezone(r.Context(), req.Timezone)
		s.db.Audit(r.Context(), user, "system.timezone", req.Timezone, auditResult(err), errText(err))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.NTP != nil {
		err := s.sysconfig.SetNTP(r.Context(), *req.NTP)
		s.db.Audit(r.Context(), user, "system.ntp", "", auditResult(err), errText(err))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	err := s.netmanager.Connection(r.Context(), req.UUID, req.Up)
	s.db.Audit(r.Context(), user, "network.connection", req.UUID, auditResult(err), errText(err))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	err := s.netmanager.ConnectWiFi(r.Context(), req.SSID, req.Password)
	// В журнал идёт только имя сети: пароль не должен попасть ни в аудит,
	// ни в сообщение об ошибке.
	s.db.Audit(r.Context(), user, "network.wifi", req.SSID, auditResult(err), errText(err))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	err := s.sandboxpkg.Remove(r.Context(), req.Kind, req.Name)
	s.db.Audit(r.Context(), user, "package."+req.Kind+".remove", req.Name, auditResult(err), errText(err))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.sandboxpkg.List(r.Context()))
}

// handleSandboxPackageUpdate обновляет всё установленное в одной из двух
// систем.
func (s *Server) handleSandboxPackageUpdate(w http.ResponseWriter, r *http.Request) {
	var req sandboxPackageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	out, err := s.sandboxpkg.Update(r.Context(), req.Kind)
	s.db.Audit(r.Context(), user, "package."+req.Kind+".update", "", auditResult(err), errText(err))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": out, "packages": s.sandboxpkg.List(r.Context())})
}
