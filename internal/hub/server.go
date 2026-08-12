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
	"github.com/althq/netknownsthat/internal/store"
)

// Server is the hub's HTTP entry point: its own auth/session handling (via
// internal/auth, unchanged) plus the host registry and per-host proxy
// routes backed by Manager.
type Server struct {
	cfg  *config.Config
	db   *store.DB
	auth *auth.Service
	hub  *Manager
	ui   fs.FS
	log  *slog.Logger
}

// Deps bundles the constructed subsystems, mirroring api.Deps.
type Deps struct {
	Cfg  *config.Config
	DB   *store.DB
	Auth *auth.Service
	Hub  *Manager
	UI   fs.FS
	Log  *slog.Logger
}

// New builds the hub server.
func New(d Deps) *Server {
	return &Server{cfg: d.Cfg, db: d.DB, auth: d.Auth, hub: d.Hub, ui: d.UI, log: d.Log}
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
	r.Use(middleware.Timeout(2 * time.Minute))
	r.Use(securityHeaders)

	r.Route("/api", func(r chi.Router) {
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

			r.Group(func(r chi.Router) {
				r.Use(s.auth.RequireAdmin)

				r.Post("/hub/hosts", s.handleAddHost)
				r.Patch("/hub/hosts/{id}", s.handleUpdateHost)
				r.Delete("/hub/hosts/{id}", s.handleDeleteHost)
				r.Post("/hub/hosts/{id}/install", s.handleStartInstall)
				r.Post("/hub/hosts/{id}/install/cancel", s.handleCancelInstall)
			})

			// Every other host-scoped call — reads and mutations alike —
			// crosses the SSH tunnel to that host's own nkt API, which
			// enforces its own RequireAuth/RequireAdmin exactly as it would
			// for a direct request; the hub does not re-implement that
			// check here.
			r.HandleFunc("/hosts/{id}/*", s.proxyHost)
		})

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "Неизвестный метод API: "+r.URL.Path)
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
			writeError(w, http.StatusNotFound, "Фронтенд не собран: запустите npm run build в каталоге web/")
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
