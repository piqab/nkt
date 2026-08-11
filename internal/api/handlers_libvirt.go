package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/auth"
)

func (s *Server) handleVMs(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": snap.VMs})
}

// handleVMAction covers both lifecycle actions (start/shutdown/destroy/
// reboot/suspend/resume) and the autostart toggle (autostart-on/
// autostart-off) — one route, since both are just "do X to this domain"
// from the caller's point of view.
func (s *Server) handleVMAction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	action := chi.URLParam(r, "action")
	user := auth.Username(r.Context())

	var err error
	switch action {
	case "autostart-on":
		err = s.libvirt.SetAutostart(r.Context(), user, name, true)
	case "autostart-off":
		err = s.libvirt.SetAutostart(r.Context(), user, name, false)
	default:
		err = s.libvirt.VMAction(r.Context(), user, name, action)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVMDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	removeStorage := r.URL.Query().Get("remove_storage") == "true"
	user := auth.Username(r.Context())
	if err := s.libvirt.UndefineVM(r.Context(), user, name, removeStorage); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
