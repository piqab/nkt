package api

import (
	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/control"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
)

func (s *Server) handlePodmanContainers(w http.ResponseWriter, r *http.Request) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		fail(w, r, err)
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
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if wantsJob(r) {
		steps, err := control.PodmanCreateCommands(req.Image, req.Name)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		podman := hostTool("podman")
		cmds := []cmdjob.Command{
			{Argv: append([]string{podman}, steps[0]...), StepKey: "podman.stepPull", StepArgs: []any{req.Image}},
			{Argv: append([]string{podman}, steps[1]...), StepKey: "podman.stepRun", StepArgs: []any{req.Name}},
		}
		s.startCmdJob(w, r, "podman.jobCreate", []any{req.Name}, "podman:create:"+req.Name, cmdjob.Params{Commands: cmds, Refresh: true}, "podman.create", req.Name)
		return
	}
	user := auth.Username(r.Context())
	if err := s.podman.CreateContainer(r.Context(), user, req.Image, req.Name); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
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
		writeErr(w, r, http.StatusBadRequest, err)
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
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
