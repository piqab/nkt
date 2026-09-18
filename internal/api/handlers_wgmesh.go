package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/msgs"
)

// Туннель WireGuard между хостами кластера (см. control.WGManager).

func (s *Server) wgMesh() *control.WGManager {
	var run control.PrivilegedRunner
	if s.cfg.Mode == config.ModeLocal {
		run = RunUnrestricted
	}
	return control.NewWGManager(run)
}

func (s *Server) handleWGStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.wgMesh().Status(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleWGApply(w http.ResponseWriter, r *http.Request) {
	var mesh control.WGMesh
	if err := decodeJSON(r, &mesh); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	err := s.wgMesh().Apply(r.Context(), mesh)
	s.db.Audit(r.Context(), user, "wgmesh.apply", mesh.Name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWGRemove(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user := auth.Username(r.Context())
	err := s.wgMesh().Remove(r.Context(), name)
	s.db.Audit(r.Context(), user, "wgmesh.remove", name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleNetPing — отвечает ли адрес с хоста: одним ICMP-пакетом. Сухой
// прогон так убеждается, что хосты кластера видят друг друга, а после
// подъёма туннеля — что соседи отвечают по адресам туннеля.
func (s *Server) handleNetPing(w http.ResponseWriter, r *http.Request) {
	ip := strings.TrimSpace(r.URL.Query().Get("ip"))
	if net.ParseIP(ip) == nil {
		// Имя хоста тоже годится — но без shell-мусора.
		if ip == "" || strings.ContainsAny(ip, " \t\n'\"`$\\;&|") {
			writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "control.wgBadAddress", ip))
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	started := time.Now()
	// В своём юните ping лишён file capabilities (NoNewPrivileges) —
	// на настоящем хосте команда идёт вне песочницы, как virsh.
	argv := []string{"ping", "-c", "1", "-W", "3", ip}
	var res collect.CommandResult
	var err error
	if s.cfg.Mode == config.ModeLocal {
		res, err = RunTooling(ctx, argv...)
	} else {
		res, err = s.scanner.Collector().Run(ctx, argv[0], argv[1:]...)
	}
	out := map[string]any{"ip": ip, "ms": time.Since(started).Milliseconds()}
	switch {
	case err != nil:
		out["ok"], out["error"] = false, err.Error()
	case res.ExitCode != 0:
		out["ok"], out["error"] = false, lastLineOf(res.Output())
	default:
		out["ok"] = true
	}
	writeJSON(w, http.StatusOK, out)
}
