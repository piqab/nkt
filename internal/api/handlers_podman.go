package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/auth"
)

func (s *Server) handlePodmanContainers(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": snap.Podman})
}

type podmanCreateRequest struct {
	Image string `json:"image"`
	Name  string `json:"name"`
}

func (s *Server) handlePodmanContainerCreate(w http.ResponseWriter, r *http.Request) {
	var req podmanCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	if err := s.podman.CreateContainer(r.Context(), user, req.Image, req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handlePodmanContainerAction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	action := chi.URLParam(r, "action")
	user := auth.Username(r.Context())
	if err := s.podman.ContainerAction(r.Context(), user, name, action); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handlePodmanContainerDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	force := r.URL.Query().Get("force") == "true"
	user := auth.Username(r.Context())
	if err := s.podman.DeleteContainer(r.Context(), user, name, force); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
