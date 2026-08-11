package hub

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// ------------------------------------------------------------------- auth

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Укажите логин и пароль")
		return
	}

	token, expires, user, err := s.auth.Login(r.Context(), req.Username, req.Password, r.UserAgent())
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, auth.ErrTooManyAttempts) {
			status = http.StatusTooManyRequests
		}
		writeError(w, status, err.Error())
		return
	}
	s.auth.SetSessionCookie(w, token, expires)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":    map[string]any{"username": user.Username, "role": user.Role},
		"expires": store.FormatTime(expires),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.auth.Logout(r.Context(), auth.TokenFromRequest(r))
	s.auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMe matches the shape of a plain nkt's own /api/auth/me (see
// api.handleMe and web/src/types.ts's Me), so the embedded frontend's shell
// works unmodified whether it is talking to a single host or to a hub —
// mode: "hub" is what tells App.tsx to show the host registry instead of a
// local dashboard.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "Требуется вход в систему")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":        user.Username,
		"role":            user.Role,
		"is_admin":        user.IsAdmin(),
		"mode":            "hub",
		"allow_mutations": s.cfg.AllowMutations,
		"simulated":       false,
	})
}

// ------------------------------------------------------------------- hosts

type addHostRequest struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	SSHPort  int    `json:"ssh_port"`
	SSHUser  string `json:"ssh_user"`
	AuthKind string `json:"auth_kind"` // "password" | "key"
	Secret   string `json:"secret"`    // password, or a PEM private key
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.ListHosts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, hosts)
}

func (s *Server) handleAddHost(w http.ResponseWriter, r *http.Request) {
	var req addHostRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.hub.AddHost(r.Context(), strings.TrimSpace(req.Name), strings.TrimSpace(req.Addr),
		req.SSHPort, strings.TrimSpace(req.SSHUser), req.AuthKind, req.Secret)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.hub.CloseHost(id)
	if err := s.db.DeleteHost(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStartInstall(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.db.HostByID(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	job, err := s.hub.StartInstall(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"job": job})
}

func (s *Server) handleInstallJobStatus(w http.ResponseWriter, r *http.Request) {
	events, done, errMsg, ok := s.hub.InstallJobStatus(chi.URLParam(r, "job"))
	if !ok {
		writeError(w, http.StatusNotFound, "Задача не найдена")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "done": done, "error": errMsg})
}

func hostIDParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, errors.New("неверный id хоста")
	}
	return id, nil
}

func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// ------------------------------------------------------------------- proxy

// proxyHost forwards a request under /api/hosts/{id}/* to the same path
// under /api/* on that host's own nkt — see Manager.Proxy.
func (s *Server) proxyHost(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rest := chi.URLParam(r, "*")

	r2 := r.Clone(r.Context())
	r2.URL.Path = "/api/" + rest
	r2.URL.RawPath = ""
	s.hub.Proxy(id).ServeHTTP(w, r2)
}
