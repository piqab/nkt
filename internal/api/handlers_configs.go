package api

import (
	"errors"
	"net/http"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/control"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/msgs"
)

func (s *Server) handleConfigList(w http.ResponseWriter, r *http.Request) {
	files, err := s.configs.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	// ConfigManager.List walks disk directly (live, for versioning/editing)
	// and knows nothing about server_name/host — that's parsed separately,
	// into Endpoint.Names, by the inventory scanner. Cross-referencing the
	// latest scan here (best-effort: a scan failure just means no site
	// names on this response, not that the file list itself failed) is
	// what lets "Файлы" show which sites each file declares.
	if snap, err := s.scanner.LatestOrScan(r.Context()); err == nil {
		model.AttachSiteNames(files, snap.Endpoints)
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) handleConfigBrowse(w http.ResponseWriter, r *http.Request) {
	entries, err := s.configs.BrowseDir(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

type mkdirRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleConfigMkdir(w http.ResponseWriter, r *http.Request) {
	var req mkdirRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	if err := s.configs.Mkdir(req.Path); err != nil {
		s.db.Audit(r.Context(), user, "config.mkdir", req.Path, "error", err.Error())
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), user, "config.mkdir", req.Path, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleConfigRead(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	file, err := s.configs.Read(path)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

type configWriteRequest struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Note     string `json:"note"`
	Apply    bool   `json:"apply"`
	Expected string `json:"expected_sha256"`
}

func (s *Server) handleConfigWrite(w http.ResponseWriter, r *http.Request) {
	var req configWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())

	// Optimistic locking: refuse to silently overwrite a file that changed on
	// disk after the editor loaded it.
	if req.Expected != "" {
		current, err := s.configs.Read(req.Path)
		if err != nil {
			fail(w, r, err)
			return
		}
		if current.SHA256 != req.Expected {
			writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "configs.staleContent"))
			return
		}
	}

	res, err := s.configs.Write(r.Context(), user, req.Path, req.Content, req.Note, req.Apply)
	if err != nil {
		s.db.Audit(r.Context(), user, "config.write", req.Path, "error", err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "result": res})
		return
	}
	s.db.Audit(r.Context(), user, "config.write", req.Path, "ok", map[string]any{
		"version": res.VersionID, "applied": res.Applied, "note": req.Note,
	})
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleConfigBlocks(w http.ResponseWriter, r *http.Request) {
	blocks, err := s.configs.ListBlocks(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks})
}

type blockWriteRequest struct {
	Path string `json:"path"`
	control.BlockWriteRequest
}

func (s *Server) handleConfigBlockWrite(w http.ResponseWriter, r *http.Request) {
	var req blockWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())

	res, err := s.configs.WriteBlock(r.Context(), user, req.Path, req.BlockWriteRequest)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, control.ErrStaleContent) {
			status = http.StatusConflict
		}
		s.db.Audit(r.Context(), user, "config.block."+req.Op, req.Path, "error", err.Error())
		writeJSON(w, status, map[string]any{"error": err.Error(), "result": res})
		return
	}
	s.db.Audit(r.Context(), user, "config.block."+req.Op, req.Path, "ok", map[string]any{
		"kind": req.Kind, "version": res.VersionID, "applied": res.Applied,
	})
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleConfigVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.configs.Versions(r.Context(), r.URL.Query().Get("path"), intParam(r, "limit", 100))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (s *Server) handleConfigVersion(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	version, content, err := s.configs.VersionContent(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "content": content})
}

func (s *Server) handleConfigDiff(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	diff, err := s.configs.Diff(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": diff})
}

type rollbackRequest struct {
	Apply bool `json:"apply"`
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	var req rollbackRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	user := auth.Username(r.Context())

	res, err := s.configs.Rollback(r.Context(), user, id, req.Apply)
	if err != nil {
		s.db.Audit(r.Context(), user, "config.rollback", res.Path, "error", err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "result": res})
		return
	}
	s.db.Audit(r.Context(), user, "config.rollback", res.Path, "ok",
		map[string]any{"restored_from": id, "new_version": res.VersionID})
	writeJSON(w, http.StatusOK, res)
}
