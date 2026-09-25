package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/backup"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Бэкапы машин, контейнеров и compose-стеков — см. internal/backup.

func (s *Server) backupRoot() string { return filepath.Join(s.cfg.DataDir, "backups") }

// handleBackupsList — GET /backups?kind=vm&name=web-vm.
func (s *Server) handleBackupsList(w http.ResponseWriter, r *http.Request) {
	list, err := backup.List(s.backupRoot(), r.URL.Query().Get("kind"), r.URL.Query().Get("name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if list == nil {
		list = []backup.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": list, "root": s.backupRoot()})
}

// handleBackupCreate — POST /backups {kind, name, project_dir?, include_images?}: задание.
func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	var p backup.Params
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !backup.ValidKind(p.Kind) || !backup.ValidName(p.Name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("backup.badTarget", p.Kind, p.Name))
		return
	}
	s.startBackupJob(w, r, backup.JobKindBackup, "backup.jobTitle", []any{p.Kind, p.Name}, "backup:"+p.Kind+":"+p.Name, p, p.Kind+":"+p.Name)
}

// handleBackupRestore — POST /backups/restore {path, new_name?}: задание.
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	var p backup.RestoreParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	path, err := backup.Resolve(s.backupRoot(), p.Path)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	p.Path = path
	if p.NewName != "" && !backup.ValidName(p.NewName) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("backup.badTarget", "", p.NewName))
		return
	}
	s.startBackupJob(w, r, backup.JobKindRestore, "backup.restoreJobTitle", []any{filepath.Base(path)}, "backup-restore", p, path)
}

func (s *Server) startBackupJob(w http.ResponseWriter, r *http.Request, kind, titleKey string, titleArgs []any, queue string, params any, target string) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: kind, TitleKey: titleKey, TitleArgs: titleArgs, Queue: queue, Author: user, Steps: 1, Params: params,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, kind, target, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// handleBackupDelete — POST /backups/delete {path}.
func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	path, err := backup.Resolve(s.backupRoot(), req.Path)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := os.Remove(path); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "backup.delete", path, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleBackupDownload — GET /backups/download?path=…: архив целиком.
// Бэкап — гигабайты: срок записи ответа продлевается.
func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	path, err := backup.Resolve(s.backupRoot(), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(6 * time.Hour))
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	s.db.Audit(r.Context(), auth.Username(r.Context()), "backup.download", path, "ok", nil)
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}
