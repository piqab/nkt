package api

import (
	"net/http"
	"strings"

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

type renewRequest struct {
	Lineage string `json:"lineage"`
}

// handleRenewCertbot re-issues a certbot-managed certificate lineage in
// place, calling certbot itself rather than writing any file directly.
func (s *Server) handleRenewCertbot(w http.ResponseWriter, r *http.Request) {
	var req renewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Lineage == "" {
		writeError(w, http.StatusBadRequest, "укажите lineage")
		return
	}

	user := auth.Username(r.Context())
	res, err := s.certs.RenewCertbot(r.Context(), user, req.Lineage)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// certbot rewrote the files the running config already points at, but the
	// cached snapshot still has the old expiry until the next scan.
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"output":    strings.TrimSpace(res.Output()),
		"simulated": res.Simulated,
	})
}

// handleCertLineages lists the certbot lineages found on the host, with
// their expiry, to populate the "собрать PEM для haproxy" form without the
// operator having to know or type the exact directory name.
func (s *Server) handleCertLineages(w http.ResponseWriter, r *http.Request) {
	lineages, err := s.certs.ListLetsEncryptLineages()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lineages": lineages})
}

// handleHAProxyCertPaths lists the haproxy certificate paths the last scan
// actually found — the exact rows "Подробности" shows — so the combine form
// can offer to overwrite one of them in place.
func (s *Server) handleHAProxyCertPaths(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"paths": s.certs.ListHAProxyCertPaths()})
}

type combineRequest struct {
	Lineage    string `json:"lineage"`
	TargetPath string `json:"target_path"`
}

// handleCombineForHAProxy packages an already-issued certbot lineage into
// the single PEM haproxy's "crt" needs. Unlike renew, this never calls
// certbot: it only repackages a certificate that already exists. With
// TargetPath set to a path handleHAProxyCertPaths returned, it overwrites
// that file in place and reloads haproxy; left empty, it writes a new file
// and returns a directive to paste in by hand.
func (s *Server) handleCombineForHAProxy(w http.ResponseWriter, r *http.Request) {
	var req combineRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Lineage == "" {
		writeError(w, http.StatusBadRequest, "укажите lineage")
		return
	}

	user := auth.Username(r.Context())
	res, err := s.certs.CombineForHAProxy(r.Context(), user, req.Lineage, req.TargetPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The new/overwritten file does not appear in the certificate inventory
	// with its new expiry until the next scan, but a rescan costs nothing.
	s.rescanLater()
	writeJSON(w, http.StatusOK, res)
}
