package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

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
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	err := s.osusers.Create(r.Context(), req)
	s.db.Audit(r.Context(), user, "osuser.create", req.Name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	users, err := s.osusers.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// handleOSGroupList — группы хоста и допустимые оболочки (для окна).
func (s *Server) handleOSGroupList(w http.ResponseWriter, r *http.Request) {
	groups, err := s.osusers.Groups(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "shells": s.osusers.Shells()})
}

// handleOSUserUpdate — группы, оболочка, sudo nkt и ключи учётной записи.
func (s *Server) handleOSUserUpdate(w http.ResponseWriter, r *http.Request) {
	var req control.UpdateOptions
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	name := chi.URLParam(r, "name")
	err := s.osusers.Update(r.Context(), name, req)
	s.db.Audit(r.Context(), auth.Username(r.Context()), "osuser.update", name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.handleOSUserList(w, r)
}

// handleOSUserDelete — удалить учётную запись (?home=1 — с домашним
// каталогом).
func (s *Server) handleOSUserDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	withHome := r.URL.Query().Get("home") == "1"
	// ?job=1 с домашним каталогом — заданием: большой каталог удаляется
	// дольше, чем браузер ждёт ответа.
	if wantsJob(r) && withHome {
		s.startHostOp(w, r, "osuser.delete", osUserDeleteArgs{Name: name, Home: true}, "hostop.osUserDeleteJob", []any{name}, "host")
		return
	}
	err := s.osusers.Delete(r.Context(), name, withHome)
	target := name
	if withHome {
		target += " +home"
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "osuser.delete", target, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.handleOSUserList(w, r)
}
