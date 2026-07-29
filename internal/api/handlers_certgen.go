package api

import (
	"net/http"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/control"
)

// handleGenerateSelfSigned issues a self-signed certificate on the host. It
// never edits nginx or haproxy configuration: the caller pastes the returned
// snippet through the validated config editor, which already handles the
// validate-or-roll-back path.
func (s *Server) handleGenerateSelfSigned(w http.ResponseWriter, r *http.Request) {
	var req control.SelfSignedRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user := auth.Username(r.Context())
	res, err := s.certs.GenerateSelfSigned(r.Context(), user, req)
	if err != nil {
		s.db.Audit(r.Context(), user, "cert.generate_selfsigned", "", "error", err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The new file does not appear in the certificate inventory until some
	// configuration references it, but a rescan costs nothing and keeps the
	// snapshot current for anything else that changed underneath it.
	s.rescanLater()
	writeJSON(w, http.StatusOK, res)
}
