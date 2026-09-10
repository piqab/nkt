package api

import (
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
)

// handleOSUserList отдаёт учётные записи самой операционной системы с их
// SSH-ключами — не путать с /users, где живут учётки веб-интерфейса.
func (s *Server) handleOSUserList(w http.ResponseWriter, r *http.Request) {
	users, err := s.osusers.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// handleOSUserCreate заводит учётную запись и кладёт ей публичный ключ.
func (s *Server) handleOSUserCreate(w http.ResponseWriter, r *http.Request) {
	var req control.CreateOptions
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	err := s.osusers.Create(r.Context(), req)
	s.db.Audit(r.Context(), user, "osuser.create", req.Name, auditResult(err), errText(err))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	users, err := s.osusers.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}
