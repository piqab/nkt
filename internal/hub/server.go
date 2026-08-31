// Package hub implements the multi-host control center: it installs nkt on
// remote VPS hosts over SSH and proxies each host's own web API through this
// process, so one dashboard manages many hosts the same way a plain nkt
// manages one — see /home/alex/.claude/plans/atomic-churning-russell.md for
// the full design.
package hub

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/msgs"
	"github.com/althq/netknownsthat/internal/store"
)

// Server is the hub's HTTP entry point: its own auth/session handling (via
// internal/auth, unchanged) plus the host registry and per-host proxy
// routes backed by Manager.
type Server struct {
	cfg          *config.Config
	db           *store.DB
	auth         *auth.Service
	hub          *Manager
	local        http.Handler
	localScanner *inventory.Scanner
	ui           fs.FS
	log          *slog.Logger
}

// Deps bundles the constructed subsystems, mirroring api.Deps.
type Deps struct {
	Cfg  *config.Config
	DB   *store.DB
	Auth *auth.Service
	Hub  *Manager
	// Local is the embedded api.Server.Handler() for the machine the hub
	// itself runs on — mounted at /api/hosts/local/* (proxyLocal) so
	// "localhost" appears in the host list with no SSH install of its
	// own. nil is valid (e.g. tests that don't need it): proxyLocal
	// reports 503 rather than panicking.
	Local http.Handler
	// LocalScanner is that same embedded instance's own inventory.Scanner
	// — handleListHosts reads its latest snapshot directly (no SSH poll
	// needed, it's in-process) to give the synthetic "localhost" row the
	// same findings-count badge every real managed host's row gets from
	// Manager.Overview. nil is valid, same as Local — the row just shows
	// no findings yet (matches "no scan has completed" for any host).
	LocalScanner *inventory.Scanner
	UI           fs.FS
	Log          *slog.Logger
}

// New builds the hub server.
func New(d Deps) *Server {
	return &Server{
		cfg: d.Cfg, db: d.DB, auth: d.Auth, hub: d.Hub,
		local: d.Local, localScanner: d.LocalScanner, ui: d.UI, log: d.Log,
	}
}

// Handler builds the HTTP router: the hub's own login, the host registry and
// install-progress endpoints, a reverse proxy onto each host's own API, and
// the embedded frontend — the same one plain nkt serves, since the pages
// under it are unmodified and simply point at /api/hosts/{id}/... instead
// of /api/... when a host is selected.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(s.requestLogger)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)

	r.Route("/api", func(r chi.Router) {
		// Long-lived WebSocket endpoints proxied onto the local or a
		// managed host — must never inherit the Timeout group below. Chi's
		// Timeout middleware caps a request's *total* lifetime, not idle
		// time; applied to a hijacked WS connection mid-proxy, it fires a
		// deferred w.WriteHeader(504) on that already-hijacked
		// ResponseWriter once the deadline passes, which both logs Go's
		// own "response.WriteHeader on hijacked connection" and leaves the
		// browser side of the session hung — any terminal/install/update
		// session that legitimately runs longer than the timeout would be
		// silently killed. Mirrors api/server.go's identical exemption for
		// these same underlying routes on the direct (single-host) server;
		// listed explicitly here too since the hub's /hosts/local/* and
		// /hosts/{id}/* wildcard mounts below don't otherwise distinguish
		// a WS upgrade from an ordinary REST call at the router level. A
		// sibling group, not nested inside the Timeout one — chi matches
		// the more specific literal path here over the wildcard regardless
		// of which group registered it.
		hubWSPaths := []string{
			"/terminal/ws",
			"/terminal/btop/ws",
			"/updates/ws",
			"/firewall/ufw-install/ws",
			"/firewall/firewalld-install/ws",
			"/system/dbus-install/ws",
			"/system/tmux-install/ws",
			"/system/btop-install/ws",
			"/services/{name}/install/ws",
			"/system/packages/install/ws",
			"/system/packages/remove/ws",
			"/system/apt/packages/{name}/install/ws",
			"/system/apt/packages/{name}/remove/ws",
		}
		r.Group(func(r chi.Router) {
			r.Use(s.auth.RequireAuth)
			for _, p := range hubWSPaths {
				r.Get("/hosts/local"+p, s.proxyLocal)
			}
			r.Group(func(r chi.Router) {
				r.Use(s.auth.RequireAdmin)
				for _, p := range hubWSPaths {
					r.Get("/hosts/{id}"+p, s.proxyHost)
				}
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(2 * time.Minute))

			r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "mode": string(s.cfg.Mode)})
			})
			r.Post("/auth/login", s.handleLogin)

			r.Group(func(r chi.Router) {
				r.Use(s.auth.RequireAuth)

				r.Get("/auth/me", s.handleMe)
				r.Post("/auth/logout", s.handleLogout)
				r.Post("/auth/password", s.handleChangePassword)

				r.Get("/hub/hosts", s.handleListHosts)
				r.Get("/hub/hosts/{id}/pubkey", s.handleHostPubKey)
				r.Get("/hub/hosts/{id}/install/latest", s.handleLatestInstallJob)
				r.Get("/hub/hosts/{id}/install/{job}", s.handleInstallJobStatus)

				// "localhost" (internal/hub/handlers.go's synthetic entry
				// prepended in handleListHosts) needs no RequireAdmin wrapper
				// the way /hosts/{id}/* below does: that wrapper exists
				// because a real managed host is always reached as *that
				// host's own* bootstrap-admin cookie regardless of the hub
				// user's actual role (Manager.cookieFor), so hub-level RBAC
				// is the only place a hub "viewer" can be stopped from
				// getting de-facto admin on a connected host. The embedded
				// local api.Server has no such blind spot — it shares this
				// same auth.Service/session, so its own RequireAuth/
				// RequireAdmin middleware already sees the real user and
				// role directly, the same as a plain single-host nkt install.
				r.HandleFunc("/hosts/local/*", s.proxyLocal)

				r.Group(func(r chi.Router) {
					r.Use(s.auth.RequireAdmin)

					r.Post("/hub/hosts", s.handleAddHost)
					r.Patch("/hub/hosts/{id}", s.handleUpdateHost)
					r.Delete("/hub/hosts/{id}", s.handleDeleteHost)
					r.Post("/hub/hosts/{id}/install", s.handleStartInstall)
					r.Post("/hub/hosts/{id}/install/cancel", s.handleCancelInstall)
					r.Post("/hub/hosts/{id}/sudo/remove", s.handleRemoveSudoAccess)
					r.Post("/hub/hosts/{id}/stop", s.handleStopHost)
					r.Post("/hub/hosts/{id}/start", s.handleStartHost)
					r.Get("/hub/export", s.handleExportHosts)
					r.Post("/hub/import", s.handleImportHosts)

					// Every other host-scoped call — reads and mutations alike —
					// crosses the SSH tunnel to that host's own nkt API,
					// authenticated there as *that host's* saved bootstrap-admin
					// account (see Manager.cookieFor), never as whoever is
					// actually sitting in the browser. A host's own
					// RequireAuth/RequireAdmin therefore always sees "admin",
					// regardless of the hub account's real role — so gating this
					// on RequireAdmin here, rather than trusting the host to
					// re-derive it, is the only place a hub "viewer" account's
					// restriction can actually be enforced. Without this, a
					// viewer — who cannot add/install/remove a host — could
					// still open any already-connected one and get full admin
					// control over it.
					r.HandleFunc("/hosts/{id}/*", s.proxyHost)
				})
			})

			r.NotFound(func(w http.ResponseWriter, r *http.Request) {
				writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "server.unknownApiMethod", r.URL.Path))
			})
		})
	})

	if s.ui != nil {
		r.Handle("/*", s.spaHandler())
	}
	return r
}

// spaHandler serves the embedded frontend — identical to api.Server's own,
// duplicated rather than shared since the two packages have no other reason
// to depend on each other.
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if f, err := s.ui.Open(path); err == nil {
			_ = f.Close()
			if strings.HasPrefix(path, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(s.ui, "index.html")
		if err != nil {
			writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "server.frontendNotBuilt"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if strings.HasPrefix(r.URL.Path, "/api") {
			s.log.Debug("http",
				"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"bytes", ww.BytesWritten(), "duration", time.Since(started).Round(time.Millisecond).String())
		}
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
