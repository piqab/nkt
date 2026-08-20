package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// TerminalEnabled becomes NKT_TERMINAL_ENABLED on this host's next
	// install/update (see store.Host.TerminalEnabled) — off by default,
	// same as the env var itself.
	TerminalEnabled bool `json:"terminal_enabled"`
	// TunnelEnabled turns on the reverse-tunnel fallback channel for this
	// host's next install/update (see store.Host.TunnelEnabled) — off by
	// default. Set separately from AddHost/AddHostGenerated below (see
	// Manager.SetTunnelEnabled's own doc comment for why).
	TunnelEnabled bool `json:"tunnel_enabled"`
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
	Findings  map[string]int `json:"findings,omitempty"`
	Reachable *bool          `json:"reachable,omitempty"`
	// RunningVersion is what the host's binary reports actually serving
	// requests, as opposed to Host.NktVersion, which is what the hub
	// recorded having installed there. They differ exactly when an
	// update did not take effect — a failure that is otherwise invisible,
	// since every page keeps working, just without whatever the new
	// version added.
	RunningVersion string `json:"running_version,omitempty"`
	LastPolledAt   string `json:"last_polled_at,omitempty"`
	// Channel is "ssh" or "tunnel" — which path the hub most recently
	// reached this host through (see Manager.recordChannel). Omitted
	// before the first dial attempt.
	Channel string `json:"channel,omitempty"`
	// TunnelConnected reports whether this host currently has a live
	// reverse-tunnel session registered, regardless of whether SSH is
	// also working right now — a host can have a healthy standby channel
	// (Channel still "ssh") long before it's ever actually needed.
	TunnelConnected bool `json:"tunnel_connected,omitempty"`
}

// localHostID is the sentinel Host.ID for the synthetic "localhost" row —
// real hosts autoincrement from 1, so this never collides with one.
const localHostID = -1

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.ListHosts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]hostWithOverview, 0, len(hosts)+1)
	if local := s.localHostEntry(); local != nil {
		out = append(out, *local)
	}
	for _, h := range hosts {
		row := hostWithOverview{Host: h}
		if ov, ok := s.hub.Overview(h.ID); ok {
			row.Findings = ov.Findings
			reachable := ov.Reachable
			row.Reachable = &reachable
			row.RunningVersion = ov.Version
			row.Channel = ov.Channel
			if !ov.LastPolledAt.IsZero() {
				row.LastPolledAt = store.FormatTime(ov.LastPolledAt)
			}
		}
		row.TunnelConnected = s.hub.TunnelConnected(h.ID)
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// localHostEntry builds the pinned "localhost" row — the machine the hub
// itself runs on, reachable with no SSH/install (see proxyLocal). Returns
// nil when the hub wasn't built with an embedded scanner at all (Local ==
// nil): a hub that genuinely has nothing local to show shouldn't pin an
// empty, permanently-broken row at the top of every operator's host list.
func (s *Server) localHostEntry() *hostWithOverview {
	if s.local == nil {
		return nil
	}
	row := &hostWithOverview{Host: store.Host{
		ID:     localHostID,
		Name:   "localhost",
		Addr:   "127.0.0.1",
		Status: store.HostStatusOnline,
	}}
	reachable := true
	row.Reachable = &reachable
	row.RunningVersion = s.hub.Version()
	if s.localScanner != nil {
		if snap := s.localScanner.Latest(); snap != nil {
			row.Findings = snap.FindingCounts()
			row.LastPolledAt = snap.TS
		}
	}
	return row
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
		id, authorizedKey, err := s.hub.AddHostGenerated(r.Context(), name, addr, req.SSHPort, sshUser, req.TerminalEnabled)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.setTunnelEnabled(r.Context(), id, req.TunnelEnabled)
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "authorized_key": authorizedKey})
		return
	}

	id, err := s.hub.AddHost(r.Context(), name, addr, req.SSHPort, sshUser, req.AuthKind, req.Secret, req.TerminalEnabled)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setTunnelEnabled(r.Context(), id, req.TunnelEnabled)
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// setTunnelEnabled applies Manager.SetTunnelEnabled after the host it
// targets has already been created/updated successfully — a failure here
// (essentially only a DB error; the host id is always valid at this point)
// is logged rather than turned into an error response, since the host
// itself was already committed and reporting the whole request as failed
// would be misleading.
func (s *Server) setTunnelEnabled(ctx context.Context, hostID int64, enabled bool) {
	if err := s.hub.SetTunnelEnabled(ctx, hostID, enabled); err != nil {
		s.log.Warn("не удалось сохранить настройку резервного канала", "host_id", hostID, "err", err)
	}
}

// updateHostRequest mirrors addHostRequest; Secret is optional here — an
// empty string leaves the stored SSH credential untouched (see
// Manager.UpdateHost). auth_kind "generated" replaces it with a freshly
// hub-generated keypair regardless of Secret.
type updateHostRequest struct {
	Name            string `json:"name"`
	Addr            string `json:"addr"`
	SSHPort         int    `json:"ssh_port"`
	SSHUser         string `json:"ssh_user"`
	AuthKind        string `json:"auth_kind"`
	Secret          string `json:"secret"`
	TerminalEnabled bool   `json:"terminal_enabled"`
	TunnelEnabled   bool   `json:"tunnel_enabled"`
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
		authorizedKey, err := s.hub.UpdateHostGenerated(r.Context(), id, name, addr, req.SSHPort, sshUser, req.TerminalEnabled)
		if err != nil {
			fail(w, err)
			return
		}
		s.setTunnelEnabled(r.Context(), id, req.TunnelEnabled)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "authorized_key": authorizedKey})
		return
	}

	if err := s.hub.UpdateHost(r.Context(), id, name, addr, req.SSHPort, sshUser, req.AuthKind, req.Secret, req.TerminalEnabled); err != nil {
		fail(w, err)
		return
	}
	s.setTunnelEnabled(r.Context(), id, req.TunnelEnabled)
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

// handleStopHost/handleStartHost stop or start the netknownsthat systemd
// unit already installed on a host — an operational toggle, not a
// reinstall (see Manager.SetServiceRunning). A stopped host simply stops
// answering pollOverviews' periodic /api/overview poll and shows as
// unreachable, the same way any other outage would — no separate "stopped"
// status is tracked for it.
func (s *Server) handleStopHost(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.hub.SetServiceRunning(r.Context(), id, false); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStartHost(w http.ResponseWriter, r *http.Request) {
	id, err := hostIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.hub.SetServiceRunning(r.Context(), id, true); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleExportHosts hands back the whole host registry — including the
// encrypted SSH/admin secrets, as-is — as a downloadable JSON file, for
// backup or migrating to a new hub. ?include_key=1 additionally embeds
// this hub's own master key, so ImportHosts on the receiving hub can
// re-encrypt every secret with its own key on the spot instead of
// requiring the two hubs' NKT_HUB_MASTER_KEY to already match (see
// Manager.ExportHosts/ImportHosts) — off by default, since while present
// the file alone is enough to decrypt every secret in it.
func (s *Server) handleExportHosts(w http.ResponseWriter, r *http.Request) {
	includeKey := r.URL.Query().Get("include_key") == "1"
	export, err := s.hub.ExportHosts(r.Context(), includeKey)
	if err != nil {
		fail(w, err)
		return
	}
	filename := fmt.Sprintf("nkt-hub-export-%s.json", strings.ReplaceAll(export.ExportedAt[:10], "-", ""))
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	writeJSON(w, http.StatusOK, export)
}

// handleImportHosts adds every host in an uploaded export as a brand-new
// row — additive, never replacing or merging into what's already
// registered (see store.ImportHosts) — and reports per-host failures
// instead of aborting the whole file over one bad entry. An embedded
// master key (see handleExportHosts) is consumed and re-encrypted away by
// Manager.ImportHosts before anything reaches the database.
func (s *Server) handleImportHosts(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20)) // 16 MiB — generous for a host list, not unbounded
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	export, err := store.DecodeHubExport(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	imported, errs := s.hub.ImportHosts(r.Context(), export)
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "errors": errs})
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
	force := r.URL.Query().Get("force") == "true"
	job, err := s.hub.StartInstall(r.Context(), id, force)
	if err != nil {
		var foreign *ForeignInstallError
		if errors.As(err, &foreign) {
			// 409: not a server error, a decision the operator needs to
			// make — the frontend recognizes this shape and re-prompts
			// with foreign.Detail, retrying with ?force=true if confirmed.
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":           err.Error(),
				"foreign_install": true,
				"detail":          foreign.Detail,
			})
			return
		}
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

// proxyLocal forwards a request under /api/hosts/local/* to the embedded
// local api.Server (the machine the hub itself runs on) — the in-process
// equivalent of proxyHost, minus the SSH tunnel: no dialing, no per-host
// cookie swap, since there is nothing remote to reach.
func (s *Server) proxyLocal(w http.ResponseWriter, r *http.Request) {
	if s.local == nil {
		writeError(w, http.StatusServiceUnavailable, "локальный сканер хаба не запущен")
		return
	}
	rest := chi.URLParam(r, "*")
	// r.Context() already carries THIS router's own *chi.Context (set by
	// chi.Mux.ServeHTTP when it first dispatched this very request) — left
	// in place, s.local's chi.Mux.ServeHTTP would see a non-nil route
	// context, assume it's a nested sub-router of the SAME tree, and skip
	// allocating its own: it would then match against rctx.RoutePath, the
	// route THIS router already consumed (e.g. "/api/hosts/local/overview"),
	// not the rewritten r2.URL.Path below, and 404 — the embedded server
	// registers "/api/overview", never that. Shadowing the key with a nil
	// *chi.Context makes the type assertion in chi's ServeHTTP come back
	// nil, so it allocates a fresh route context for the embedded server's
	// own tree instead of reusing this one's.
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, (*chi.Context)(nil))
	r2 := r.Clone(ctx)
	r2.URL.Path = "/api/" + rest
	r2.URL.RawPath = ""
	s.local.ServeHTTP(w, r2)
}

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
