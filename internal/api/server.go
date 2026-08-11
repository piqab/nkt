// Package api exposes the dashboard over HTTP: a JSON API under /api and the
// embedded single-page frontend on every other path.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/monitor"
	"github.com/althq/netknownsthat/internal/store"
)

// Server holds everything the handlers need.
type Server struct {
	cfg       *config.Config
	db        *store.DB
	auth      *auth.Service
	scanner   *inventory.Scanner
	scheduler *monitor.Scheduler
	services  *control.ServiceManager
	configs   *control.ConfigManager
	firewall  *control.FirewallManager
	certs     *control.CertManager
	podman    *control.PodmanManager
	lxd       *control.LXDManager
	libvirt   *control.LibvirtManager
	ui        fs.FS
	log       *slog.Logger
}

// Deps bundles the constructed subsystems.
type Deps struct {
	Cfg       *config.Config
	DB        *store.DB
	Auth      *auth.Service
	Scanner   *inventory.Scanner
	Scheduler *monitor.Scheduler
	Services  *control.ServiceManager
	Configs   *control.ConfigManager
	Firewall  *control.FirewallManager
	Certs     *control.CertManager
	Podman    *control.PodmanManager
	LXD       *control.LXDManager
	Libvirt   *control.LibvirtManager
	UI        fs.FS
	Log       *slog.Logger
}

// New builds the HTTP server.
func New(d Deps) *Server {
	return &Server{
		cfg: d.Cfg, db: d.DB, auth: d.Auth, scanner: d.Scanner, scheduler: d.Scheduler,
		services: d.Services, configs: d.Configs, firewall: d.Firewall, certs: d.Certs,
		podman: d.Podman, lxd: d.LXD, libvirt: d.Libvirt,
		ui: d.UI, log: d.Log,
	}
}

// Handler builds the complete routing tree.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(s.requestLogger)
	r.Use(middleware.Recoverer)
	// Comfortably longer than CertbotTimeout: an ACME renewal that's still
	// legitimately running must get its response back, not be cut off by a
	// blanket request ceiling sized for the fast host commands everything
	// else on this router uses.
	r.Use(middleware.Timeout(s.cfg.CertbotTimeout + time.Minute))
	r.Use(s.cors)
	r.Use(securityHeaders)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Post("/auth/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.auth.RequireAuth)

			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/logout", s.handleLogout)
			r.Post("/auth/password", s.handleChangePassword)

			// --- read-only surface, available to viewers -------------------------
			r.Get("/overview", s.handleOverview)
			r.Get("/inventory", s.handleInventory)
			r.Get("/findings", s.handleFindings)
			r.Get("/topology", s.handleTopology)
			r.Get("/services", s.handleServices)
			r.Get("/containers", s.handleContainers)
			r.Get("/podman/containers", s.handlePodmanContainers)
			r.Get("/lxd/instances", s.handleLXDInstances)
			r.Get("/vms", s.handleVMs)
			r.Get("/firewall", s.handleFirewall)
			r.Get("/firewall/rules", s.handleFirewallNumbered)
			r.Get("/certificates", s.handleCertificates)

			r.Get("/configs", s.handleConfigList)
			r.Get("/configs/file", s.handleConfigRead)
			r.Get("/configs/browse", s.handleConfigBrowse)
			r.Get("/configs/blocks", s.handleConfigBlocks)
			r.Get("/configs/versions", s.handleConfigVersions)
			r.Get("/configs/versions/{id}", s.handleConfigVersion)
			r.Get("/configs/versions/{id}/diff", s.handleConfigDiff)

			r.Get("/monitor/targets", s.handleTargets)
			r.Get("/monitor/targets/{id}/history", s.handleTargetHistory)
			r.Get("/monitor/heatmap", s.handleAvailabilityHeatmap)
			r.Get("/monitor/outages", s.handleOutages)
			r.Get("/monitor/usage", s.handleUsage)
			r.Get("/monitor/usage/top", s.handleUsageTop)
			r.Get("/monitor/usage/heatmap", s.handleUsageHeatmap)
			r.Get("/monitor/jobs", s.handleJobs)

			r.Get("/audit", s.handleAudit)
			r.Get("/snapshots", s.handleSnapshots)

			// --- mutations, admin only ------------------------------------------
			r.Group(func(r chi.Router) {
				r.Use(s.auth.RequireAdmin)

				r.Post("/inventory/refresh", s.handleRefresh)
				r.Post("/services/{name}/validate", s.handleServiceValidate)
				r.Post("/services/{name}/{action}", s.handleServiceAction)
				r.Post("/containers/{name}/{action}", s.handleContainerAction)
				r.Post("/podman/containers", s.handlePodmanContainerCreate)
				r.Post("/podman/containers/{name}/{action}", s.handlePodmanContainerAction)
				r.Delete("/podman/containers/{name}", s.handlePodmanContainerDelete)
				r.Post("/lxd/instances", s.handleLXDInstanceCreate)
				r.Post("/lxd/instances/{name}/{action}", s.handleLXDInstanceAction)
				r.Delete("/lxd/instances/{name}", s.handleLXDInstanceDelete)
				r.Post("/vms/{name}/{action}", s.handleVMAction)
				r.Delete("/vms/{name}", s.handleVMDelete)

				r.Put("/configs/file", s.handleConfigWrite)
				r.Post("/configs/mkdir", s.handleConfigMkdir)
				r.Post("/configs/blocks", s.handleConfigBlockWrite)
				r.Post("/configs/versions/{id}/rollback", s.handleConfigRollback)

				r.Post("/firewall/rules", s.handleFirewallAdd)
				r.Delete("/firewall/rules/{number}", s.handleFirewallDelete)
				r.Post("/firewall/reload", s.handleFirewallReload)

				r.Post("/certificates/self-signed", s.handleGenerateSelfSigned)
				r.Post("/certificates/issue", s.handleIssueCertbot)
				r.Post("/certificates/renew", s.handleRenewCertbot)
				r.Get("/certificates/renew/{job}", s.handleRenewJobStatus)
				r.Get("/certificates/lineages", s.handleCertLineages)
				r.Get("/certificates/haproxy-paths", s.handleHAProxyCertPaths)
				r.Post("/certificates/combine", s.handleCombineForHAProxy)

				r.Post("/monitor/targets/{id}/check", s.handleTargetCheck)
				r.Patch("/monitor/targets/{id}", s.handleTargetPatch)

				r.Get("/users", s.handleUserList)
				r.Post("/users", s.handleUserCreate)
				r.Patch("/users/{name}", s.handleUserPatch)
				r.Delete("/users/{name}", s.handleUserDelete)
			})
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

// spaHandler serves the embedded frontend, falling back to index.html so that
// client-side routes survive a page reload.
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if f, err := s.ui.Open(path); err == nil {
			_ = f.Close()
			// Hashed asset names are immutable; index.html must never be cached.
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

// ------------------------------------------------------------------ middleware

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

// cors allows the Vite dev server to talk to a locally running backend.
func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.cfg.CORSOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
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

// --------------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	enc := json.NewEncoder(w)
	if err := enc.Encode(payload); err != nil {
		// The status line is already sent; nothing useful is left to do.
		return
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps a domain error onto an HTTP status.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, control.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, control.ErrPathNotAllowed):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, control.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("некорректное тело запроса: " + err.Error())
	}
	return nil
}

func intParam(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func int64Path(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, name), 10, 64)
}

// sinceParam turns a "24h"/"7d"/"30d" window into a storage timestamp.
func sinceParam(r *http.Request, def time.Duration) string {
	raw := r.URL.Query().Get("since")
	d := def
	if raw != "" {
		if strings.HasSuffix(raw, "d") {
			if n, err := strconv.Atoi(strings.TrimSuffix(raw, "d")); err == nil {
				d = time.Duration(n) * 24 * time.Hour
			}
		} else if parsed, err := time.ParseDuration(raw); err == nil {
			d = parsed
		}
	}
	if d <= 0 || d > 400*24*time.Hour {
		d = def
	}
	return store.FormatTime(time.Now().Add(-d))
}

// tzParam reads the browser's UTC offset so that "по часам" means local hours.
func tzParam(r *http.Request) int {
	n := intParam(r, "tz", 0)
	if n < -14*60 || n > 14*60 {
		return 0
	}
	return n
}

func granularityParam(r *http.Request, def string) string {
	switch g := r.URL.Query().Get("granularity"); g {
	case "minute", "hour", "day":
		return g
	default:
		return def
	}
}
