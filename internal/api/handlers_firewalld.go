package api

import (
	"net/http"
	"strings"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/control"
)

func (s *Server) handleFirewalldAdd(w http.ResponseWriter, r *http.Request) {
	var spec control.FirewalldPortSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.firewalld.AddRule(r.Context(), auth.Username(r.Context()), spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
}

// handleFirewalldDelete removes a port or service from a zone, in whichever
// store(s) the request specifies. Unlike ufw's split DeleteRule/
// DeleteRuleBySpec, there is only one delete path here — firewall-cmd's own
// --remove-port/--remove-service already target a (zone, port/service)
// pair directly, with no positional index in the middle that could shift.
func (s *Server) handleFirewalldDelete(w http.ResponseWriter, r *http.Request) {
	var spec control.FirewalldPortSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.firewalld.DeleteRule(r.Context(), auth.Username(r.Context()), spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
}

func (s *Server) handleFirewalldReload(w http.ResponseWriter, r *http.Request) {
	res, err := s.firewalld.Reload(r.Context(), auth.Username(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
}
