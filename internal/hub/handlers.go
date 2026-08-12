package hub

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/store"
)

// minPasswordRunes mirrors api's own rule (see api.passwordLongEnough), so a
// hub admin's password is never held to a looser standard just because the
// account lives on the hub instead of a plain nkt.
const minPasswordRunes = 10

const minPasswordMessage = "Пароль должен быть не короче 10 символов"

func passwordLongEnough(password string) bool {
	return utf8.RuneCountInString(password) >= minPasswordRunes
}

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

type passwordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// handleChangePassword backs the same "Сменить пароль" form every nkt page
// shares (web/src/components/PasswordForm.tsx) — it always targets the hub
// itself (see api.ts's hostScope bypass for /auth/*), never a managed host.
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "Пароль изменён, войдите заново."})
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
		"hub_version":     s.hub.Version(),
	})
}

// ------------------------------------------------------------------- hosts

// authKindGenerated is a request-only auth_kind value (never stored — see
// store.HostAuthPassword/HostAuthKey): it asks the hub to generate its own
// keypair for the host instead of accepting a credential from the caller.
// Once generated, the host is indistinguishable from one added with a
// hand-supplied key — it is stored with store.HostAuthKey like any other.
const authKindGenerated = "generated"

type addHostRequest struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	SSHPort  int    `json:"ssh_port"`
	SSHUser  string `json:"ssh_user"`
	AuthKind string `json:"auth_kind"` // "generated" | "password" | "key"
	Secret   string `json:"secret"`    // password, or a PEM private key — unused when auth_kind is "generated"
}

// hostWithOverview is store.Host plus what pollOverviews last learned about
// it (see Manager.Overview) — merged in here rather than persisted on Host
// itself, since it's cache data with no reason to survive a hub restart.
//
// Both Findings and Reachable are omitted (not just false/empty) for a host
// pollOverviews has never reported on — the frontend's signal to show
// "неизвестно" rather than "недоступен"/"ноль проблем" for a host that
// isn't online yet, or hasn't had its first poll tick. Reachable is a
// *bool, not a bool, specifically so that a real "currently unreachable"
// (false) still serialises — a plain `bool` with `omitempty` would drop
// false exactly like the zero value it is, making it indistinguishable
// from "never polled" on the wire.
type hostWithOverview struct {
	store.Host
	Findings     map[string]int `json:"findings,omitempty"`
	Reachable    *bool          `json:"reachable,omitempty"`
	LastPolledAt string         `json:"last_polled_at,omitempty"`
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.ListHosts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]hostWithOverview, len(hosts))
	for i, h := range hosts {
		out[i] = hostWithOverview{Host: h}
		if findings, reachable, lastPolledAt, ok := s.hub.Overview(h.ID); ok {
			out[i].Findings = findings
			out[i].Reachable = &reachable
			if !lastPolledAt.IsZero() {
				out[i].LastPolledAt = store.FormatTime(lastPolledAt)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddHost(w http.ResponseWriter, r *http.Request) {
	var req addHostRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	addr := strings.TrimSpace(req.Addr)
	sshUser := strings.TrimSpace(req.SSHUser)

	if req.AuthKind == authKindGenerated {
		id, authorizedKey, err := s.hub.AddHostGenerated(r.Context(), name, addr, req.SSHPort, sshUser)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "authorized_key": authorizedKey})
		return
	}

	id, err := s.hub.AddHost(r.Context(), name, addr, req.SSHPort, sshUser, req.AuthKind, req.Secret)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// updateHostRequest mirrors addHostRequest; Secret is optional here — an
// empty string leaves the stored SSH credential untouched (see
// Manager.UpdateHost). auth_kind "generated" replaces it with a freshly
// hub-generated keypair regardless of Secret.
type updateHostRequest struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	SSHPort  int    `json:"ssh_port"`
	SSHUser  string `json:"ssh_user"`
	AuthKind string `json:"auth_kind"`
	Secret   string `json:"secret"`
}

func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req updateHostRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	addr := strings.TrimSpace(req.Addr)
	sshUser := strings.TrimSpace(req.SSHUser)

	if req.AuthKind == authKindGenerated {
		authorizedKey, err := s.hub.UpdateHostGenerated(r.Context(), id, name, addr, req.SSHPort, sshUser)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "authorized_key": authorizedKey})
		return
	}

	if err := s.hub.UpdateHost(r.Context(), id, name, addr, req.SSHPort, sshUser, req.AuthKind, req.Secret); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleHostPubKey(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	line, err := s.hub.PublicKeyLine(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorized_key": line})
}

// handleRemoveSudoAccess deletes the sudoers drop-in HUB.md tells an
// operator to create for a non-root SSH user — a deliberate, admin-only
// cleanup action, not something a viewer should ever be able to trigger on
// someone else's managed host.
func (s *Server) handleRemoveSudoAccess(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.hub.RemoveSudoAccess(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

// handleCancelInstall stops a host's in-flight install (or, if the hub
// restarted mid-install and lost track of it, just clears the stuck status)
// so its controls have something to act on again either way.
func (s *Server) handleCancelInstall(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.hub.CancelInstall(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInstallJobStatus(w http.ResponseWriter, r *http.Request) {
	events, done, errMsg, ok := s.hub.InstallJobStatus(chi.URLParam(r, "job"))
	if !ok {
		writeError(w, http.StatusNotFound, "Задача не найдена")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "done": done, "error": errMsg})
}

// handleLatestInstallJob lets the UI reopen a host's most recent install
// log — after closing the modal, or after a page reload — without having
// kept the job id around itself.
func (s *Server) handleLatestInstallJob(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	jobID, ok := s.hub.LatestJobID(id)
	if !ok {
		writeError(w, http.StatusNotFound, "для этого хоста ещё не было установок")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"job": jobID})
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
