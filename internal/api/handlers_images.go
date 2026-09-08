package api

import (
	"net/http"
	"strings"

	"github.com/piqab/nkt/internal/auth"
)

// handleImages lists Docker images, each marked with whether a container is
// currently running from it.
func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	images, err := s.images.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"images":     images,
		"backup_dir": s.images.BackupDir(),
	})
}

type imagesActionRequest struct {
	// Refs are ids or repo:tag references. A list rather than one per
	// request: removing a handful of images is one intent, and reporting it
	// as one outcome beats N requests whose partial failure the caller has
	// to piece together.
	Refs  []string `json:"refs"`
	Force bool     `json:"force"`
}

// imageOutcome is what happened to one reference in a batch.
type imageOutcome struct {
	Ref   string `json:"ref"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Path  string `json:"path,omitempty"`
}

// handleImagesRemove deletes the selected images, reporting each separately.
//
// One failing image does not stop the rest: they are independent, and an
// image held by a running container failing to delete is an ordinary
// outcome rather than a reason to abandon the others.
func (s *Server) handleImagesRemove(w http.ResponseWriter, r *http.Request) {
	var req imagesActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Refs) == 0 {
		writeError(w, http.StatusBadRequest, "не выбрано ни одного образа")
		return
	}
	user := auth.Username(r.Context())

	results := make([]imageOutcome, 0, len(req.Refs))
	for _, ref := range req.Refs {
		err := s.images.Remove(r.Context(), ref, req.Force)
		results = append(results, imageOutcome{Ref: ref, OK: err == nil, Error: errText(err)})
		s.db.Audit(r.Context(), user, "image.remove", ref, auditResult(err), errText(err))
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleImagesSave writes each selected image to a tar archive on the host.
func (s *Server) handleImagesSave(w http.ResponseWriter, r *http.Request) {
	var req imagesActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Refs) == 0 {
		writeError(w, http.StatusBadRequest, "не выбрано ни одного образа")
		return
	}
	user := auth.Username(r.Context())

	results := make([]imageOutcome, 0, len(req.Refs))
	for _, ref := range req.Refs {
		path, err := s.images.Save(r.Context(), ref)
		results = append(results, imageOutcome{
			Ref: ref, OK: err == nil, Error: errText(err), Path: path,
		})
		s.db.Audit(r.Context(), user, "image.save", ref, auditResult(err), errText(err))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":    results,
		"backup_dir": s.images.BackupDir(),
	})
}

// handleImagesPrune drops every dangling image — the layers left behind by
// previous builds, which is what actually fills a disk.
func (s *Server) handleImagesPrune(w http.ResponseWriter, r *http.Request) {
	reclaimed, err := s.images.Prune(r.Context())
	user := auth.Username(r.Context())
	s.db.Audit(r.Context(), user, "image.prune", "", auditResult(err), errText(err))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{"reclaimed": reclaimed})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func auditResult(err error) string {
	if err == nil {
		return "ok"
	}
	return "error"
}
