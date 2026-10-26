package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
)

func (s *Server) handleVMs(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, r, err)
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
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVMDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	removeStorage := r.URL.Query().Get("remove_storage") == "true"
	// force гасит запущенный домен перед удалением. Так удаляет хаб,
	// когда машину убирают целиком; из веб-интерфейса хоста остановка
	// по-прежнему отдельный видимый шаг оператора.
	force := r.URL.Query().Get("force") == "true"
	user := auth.Username(r.Context())
	if err := s.libvirt.UndefineVM(r.Context(), user, name, removeStorage, force); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	if s.guestCreds != nil {
		_ = s.guestCreds.Delete(r.Context(), "vm", chi.URLParam(r, "name"))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createDiskRequest struct {
	Path   string `json:"path"`
	SizeGB int    `json:"size_gb"`
}

// handleVMCreateDisk provisions a new qcow2 disk image for the VM-creation
// wizard — a hand-written or generated domain XML only ever references a
// disk path, it never creates the file itself.
func (s *Server) handleVMCreateDisk(w http.ResponseWriter, r *http.Request) {
	var req createDiskRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	if err := s.libvirt.CreateDisk(r.Context(), user, req.Path, req.SizeGB); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVMSnapshots — снимки машины.
func (s *Server) handleVMSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.libvirt.Snapshots(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if list == nil {
		list = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list})
}

// handleVMSnapshotCreate делает снимок ({"name": "…"}).
func (s *Server) handleVMSnapshotCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.libvirt.CreateSnapshot(r.Context(), auth.Username(r.Context()), chi.URLParam(r, "name"), req.Name); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVMSnapshotRevert возвращает машину к снимку.
func (s *Server) handleVMSnapshotRevert(w http.ResponseWriter, r *http.Request) {
	if err := s.libvirt.RevertSnapshot(r.Context(), auth.Username(r.Context()), chi.URLParam(r, "name"), chi.URLParam(r, "snap")); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVMSnapshotDelete удаляет снимок.
func (s *Server) handleVMSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.libvirt.DeleteSnapshot(r.Context(), auth.Username(r.Context()), chi.URLParam(r, "name"), chi.URLParam(r, "snap")); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVMResize меняет vCPU, память и диск ({"vcpus","memory_mb","disk_gb"},
// 0 — не менять).
func (s *Server) handleVMResize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VCPUs    int   `json:"vcpus"`
		MemoryMB int64 `json:"memory_mb"`
		DiskGB   int64 `json:"disk_gb"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	res, err := s.libvirt.Resize(r.Context(), auth.Username(r.Context()), chi.URLParam(r, "name"), req.VCPUs, req.MemoryMB, req.DiskGB)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, res)
}
