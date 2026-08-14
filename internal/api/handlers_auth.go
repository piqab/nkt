package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/store"
)

// minPasswordRunes is the shortest password accepted anywhere in the API.
const minPasswordRunes = 10

const minPasswordMessage = "Пароль должен быть не короче 10 символов"

// passwordLongEnough counts characters rather than bytes: five Cyrillic letters
// occupy ten bytes in UTF-8 and would otherwise pass a byte-based check.
func passwordLongEnough(password string) bool {
	return utf8.RuneCountInString(password) >= minPasswordRunes
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	snap := s.scanner.Latest()
	payload := map[string]any{
		"status": "ok",
		"mode":   s.cfg.Mode,
		// The version of the binary actually serving this request — which
		// is not necessarily the one the hub recorded having installed
		// here. Distinguishing the two is the only way to notice that an
		// update never actually took effect on a host.
		"version":         s.version,
		"allow_mutations": s.cfg.AllowMutations,
		"scanned":         snap != nil,
	}
	if snap != nil {
		payload["last_scan"] = snap.TS
		payload["hostname"] = snap.Host.Hostname
	}
	writeJSON(w, http.StatusOK, payload)
}

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
		s.db.Audit(r.Context(), req.Username, "auth.login", "", "error", err.Error())
		writeError(w, status, err.Error())
		return
	}

	s.auth.SetSessionCookie(w, token, expires)
	s.db.Audit(r.Context(), user.Username, "auth.login", "", "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":    map[string]any{"username": user.Username, "role": user.Role},
		"expires": store.FormatTime(expires),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.auth.Logout(r.Context(), auth.TokenFromRequest(r))
	s.auth.ClearSessionCookie(w)
	s.db.Audit(r.Context(), auth.Username(r.Context()), "auth.logout", "", "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"username":        user.Username,
		"role":            user.Role,
		"is_admin":        user.IsAdmin(),
		"mode":            s.cfg.Mode,
		"allow_mutations": s.cfg.AllowMutations,
		"simulated":       s.cfg.IsFixtures(),
	})
}

type passwordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req passwordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !passwordLongEnough(req.NewPassword) {
		writeError(w, http.StatusBadRequest, minPasswordMessage)
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	if err := s.auth.ChangePassword(r.Context(), user.Username, req.OldPassword, req.NewPassword); err != nil {
		s.db.Audit(r.Context(), user.Username, "auth.password", "", "error", err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auth.ClearSessionCookie(w)
	s.db.Audit(r.Context(), user.Username, "auth.password", "", "ok", "все сессии завершены")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "Пароль изменён, войдите заново."})
}

// ----------------------------------------------------------------- user admin

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

type userCreateRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var req userCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || !passwordLongEnough(req.Password) {
		writeError(w, http.StatusBadRequest, "Нужен логин и "+strings.ToLower(minPasswordMessage))
		return
	}
	if req.Role == "" {
		req.Role = store.RoleViewer
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, err)
		return
	}
	if _, err := s.db.CreateUser(r.Context(), req.Username, hash, req.Role); err != nil {
		s.db.Audit(r.Context(), auth.Username(r.Context()), "user.create", req.Username, "error", err.Error())
		writeError(w, http.StatusBadRequest, "Не удалось создать пользователя: "+err.Error())
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "user.create", req.Username, "ok",
		map[string]string{"role": req.Role})
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

type userPatchRequest struct {
	Role     *string `json:"role,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
	Password *string `json:"password,omitempty"`
}

func (s *Server) handleUserPatch(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req userPatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actor, _ := auth.UserFromContext(r.Context())

	if req.Role != nil {
		if name == actor.Username && *req.Role != store.RoleAdmin {
			writeError(w, http.StatusBadRequest, "Нельзя снять с себя роль admin")
			return
		}
		if err := s.db.SetUserRole(r.Context(), name, *req.Role); err != nil {
			fail(w, err)
			return
		}
	}
	if req.Disabled != nil {
		if name == actor.Username && *req.Disabled {
			writeError(w, http.StatusBadRequest, "Нельзя отключить собственную учётную запись")
			return
		}
		if err := s.db.SetUserDisabled(r.Context(), name, *req.Disabled); err != nil {
			fail(w, err)
			return
		}
	}
	if req.Password != nil {
		if !passwordLongEnough(*req.Password) {
			writeError(w, http.StatusBadRequest, minPasswordMessage)
			return
		}
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			fail(w, err)
			return
		}
		if err := s.db.SetPasswordHash(r.Context(), name, hash); err != nil {
			fail(w, err)
			return
		}
	}
	s.db.Audit(r.Context(), actor.Username, "user.update", name, "ok", req)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	actor, _ := auth.UserFromContext(r.Context())
	if name == actor.Username {
		writeError(w, http.StatusBadRequest, "Нельзя удалить собственную учётную запись")
		return
	}
	if err := s.db.DeleteUser(r.Context(), name); err != nil {
		fail(w, err)
		return
	}
	s.db.Audit(r.Context(), actor.Username, "user.delete", name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
