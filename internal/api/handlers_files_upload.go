package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/files"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Загрузка с планом и историей: план (что новое, что изменится, что
// защищено), загрузка под номером — прежние версии перезаписанных файлов
// уходят в историю, — список загрузок и откат заданием.

// protectKeyPrefix — защищённые шаблоны каталога в KV; их версии — в
// истории под путём protectPathPrefix+каталог.
const (
	protectKeyPrefix  = "files.protect:"
	protectPathPrefix = "nkt-protect:"
)

// protectPatterns — шаблоны каталога: свои, иначе по умолчанию.
func (s *Server) protectPatterns(ctx context.Context, dir string) []string {
	if raw, ok, err := s.db.KVGet(ctx, protectKeyPrefix+dir); err == nil && ok {
		return splitPatterns(raw)
	}
	return files.DefaultProtected
}

func splitPatterns(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// uploadBudget — сколько байт больших файлов уже ушло в историю в рамках
// загрузки (лимит на загрузку).
type uploadBudget struct {
	mu   sync.Mutex
	used map[int64]int64
}

func (b *uploadBudget) take(id, size, limit int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used == nil {
		b.used = map[int64]int64{}
	}
	if b.used[id]+size > limit {
		return false
	}
	b.used[id] += size
	return true
}

func (b *uploadBudget) forget(id int64) {
	b.mu.Lock()
	delete(b.used, id)
	b.mu.Unlock()
}

// handleFilesUploadPlan — POST /files/upload/plan {dir, entries}.
func (s *Server) handleFilesUploadPlan(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	var req struct {
		Dir     string            `json:"dir"`
		Entries []files.PlanEntry `json:"entries"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	protect := s.protectPatterns(r.Context(), req.Dir)
	items, err := m.Plan(r.Context(), req.Dir, req.Entries, protect)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	out := map[string]any{"items": items, "protect": protect}
	if s.configs != nil {
		out["history"] = s.configs.FileHistoryUsage(r.Context())
	}
	writeJSON(w, http.StatusOK, out)
}

// handleFilesUploadBegin — POST /files/upload/begin {dir, note}: номер
// загрузки, под которым её файлы попадут в историю.
func (s *Server) handleFilesUploadBegin(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	var req struct {
		Dir  string `json:"dir"`
		Note string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	dir, err := m.Check(req.Dir)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	id, err := s.db.CreateUpload(r.Context(), auth.Username(r.Context()), dir, strings.TrimSpace(req.Note))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

// handleFilesUploadFinish — POST /files/upload/{id}/finish: загрузка
// закончена; заодно вытеснение истории по лимитам.
func (s *Server) handleFilesUploadFinish(w http.ResponseWriter, r *http.Request) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	if err := s.db.SetUploadStatus(r.Context(), id, store.UploadDone); err != nil {
		fail(w, r, err)
		return
	}
	s.uploads.forget(id)
	pruned := 0
	if s.configs != nil {
		pruned = s.configs.PruneFileHistory(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "pruned": pruned})
}

// uploadWithHistory — загрузка одного файла под номером загрузки: защищённый
// существующий файл без force — отказ; прежнее содержимое — в историю
// (маленькие всегда, большие — пока хватает лимита загрузки).
func (s *Server) uploadWithHistory(r *http.Request, m *files.Manager, uploadID int64, dir, name string, force bool, body io.Reader) (string, error) {
	ctx := r.Context()
	up, err := s.db.UploadByID(ctx, uploadID)
	if err != nil || up.Status != store.UploadOpen {
		return "", msgs.Errorf("files.uploadNotOpen", uploadID)
	}
	target, err := m.UploadTarget(dir, name)
	if err != nil {
		return "", err
	}
	user := auth.Username(ctx)
	existed := false
	var versionID int64
	if rc, size, err := m.Open(target); err == nil {
		existed = true
		rel := strings.TrimPrefix(target, strings.TrimRight(up.Dir, "/")+"/")
		if !force && files.Protected(rel, s.protectPatterns(ctx, up.Dir)) {
			rc.Close()
			return "", msgs.Errorf("files.protectedFile", rel)
		}
		limits := s.configs.FileHistoryLimits(ctx)
		if size <= files.MaxEditBytes || s.uploads.take(uploadID, size, int64(limits.PerUploadMB)<<20) {
			service := "files"
			if svc, err := s.configs.ServiceForPath(target); err == nil {
				service = svc
			}
			versionID, _, err = s.configs.RecordFileSnapshot(ctx, target, service, user, store.ActionUpload,
				msgs.Tc(ctx, "files.beforeUpload", uploadID), rc)
			if err != nil {
				versionID = 0
			}
		}
		rc.Close()
	}
	hash := sha256.New()
	saved, err := m.Upload(ctx, dir, name, io.TeeReader(body, hash))
	if err != nil {
		return "", err
	}
	_ = s.db.AddUploadItem(ctx, store.FileUploadItem{
		UploadID: uploadID, Path: saved, Existed: existed, VersionID: versionID, SHAAfter: hex.EncodeToString(hash.Sum(nil)),
	})
	return saved, nil
}

// handleFilesUploads — GET /files/uploads?dir=: загрузки в каталог.
func (s *Server) handleFilesUploads(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	dir, err := m.Check(r.URL.Query().Get("dir"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	list, err := s.db.ListUploads(r.Context(), dir, 200)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploads": list})
}

// handleFilesUploadItems — GET /files/uploads/{id}: файлы загрузки.
func (s *Server) handleFilesUploadItems(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	up, err := s.db.UploadByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	if _, err := m.Check(up.Dir); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	items, err := s.db.UploadItems(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"upload": up, "items": items})
}

// KindUploadRollback — задание «откатить загрузку».
const KindUploadRollback = "files.uploadRollback"

// UploadRollbackParams — вход задания.
type UploadRollbackParams struct {
	UploadID int64 `json:"upload_id"`
}

// handleFilesUploadRollback — POST /files/uploads/{id}/rollback: заданием.
func (s *Server) handleFilesUploadRollback(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return
	}
	up, err := s.db.UploadByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	if _, err := m.Check(up.Dir); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	user := auth.Username(r.Context())
	jobID, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindUploadRollback, TitleKey: "files.rollbackTitle", TitleArgs: []any{id, up.Dir},
		Queue: "files", Author: user, Params: UploadRollbackParams{UploadID: id}, Steps: 1,
	})
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	s.db.Audit(r.Context(), user, "files.upload.rollback", up.Dir, outcome, map[string]any{"upload": id})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

// UploadRollbackRunner — откат загрузки: перезаписанные файлы — из
// истории, добавленные — удаляются, если с тех пор не менялись. Текущее
// содержимое перед заменой само уходит в историю (откат откатываем).
type UploadRollbackRunner struct {
	DB      *store.DB
	Configs *control.ConfigManager
	Files   *files.Manager
}

// Run выполняет откат.
func (u *UploadRollbackRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p UploadRollbackParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	items, err := u.DB.UploadItems(ctx, p.UploadID)
	if err != nil {
		return err
	}
	jc.StepKey(1, 1, "files.rollbackStep", len(items))
	failed := 0
	for _, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cur, curErr := u.Files.Hash(it.Path)
		switch {
		case !it.Existed:
			if curErr != nil {
				continue // уже нет
			}
			if cur != it.SHAAfter {
				jc.Log("files.rollbackChanged", it.Path)
				continue
			}
			if err := u.Files.Remove(ctx, it.Path); err != nil {
				failed++
				jc.Log("files.rollbackFailed", it.Path, err.Error())
				continue
			}
			jc.Log("files.rollbackRemoved", it.Path)
		case it.VersionID == 0:
			jc.Log("files.rollbackNoHistory", it.Path)
		default:
			if curErr == nil && cur != it.SHAAfter {
				jc.Log("files.rollbackChanged", it.Path)
				continue
			}
			_, src, err := u.Configs.VersionFile(ctx, it.VersionID)
			if err == nil {
				if rc, size, oerr := u.Files.Open(it.Path); oerr == nil {
					if size <= files.MaxEditBytes {
						_, _, _ = u.Configs.RecordFileSnapshot(ctx, it.Path, "files", jc.Job.Author, store.ActionRollback,
							msgs.T(jc.Lang(), "files.beforeRollback", p.UploadID), rc)
					}
					rc.Close()
				}
				err = u.Files.Restore(ctx, it.Path, src)
			}
			if err != nil {
				failed++
				jc.Log("files.rollbackFailed", it.Path, err.Error())
				continue
			}
			jc.Log("files.rollbackRestored", it.Path)
		}
	}
	if failed > 0 {
		return msgs.Errorf("files.rollbackFailedCount", failed, len(items))
	}
	return u.DB.SetUploadStatus(ctx, p.UploadID, store.UploadRolledBack)
}

// handleFilesProtect — GET /files/protect?dir=: защищённые шаблоны.
func (s *Server) handleFilesProtect(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	dir, err := m.Check(r.URL.Query().Get("dir"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	_, custom, _ := s.db.KVGet(r.Context(), protectKeyPrefix+dir)
	writeJSON(w, http.StatusOK, map[string]any{
		"dir": dir, "patterns": s.protectPatterns(r.Context(), dir), "custom": custom,
		"defaults": files.DefaultProtected, "path": protectPathPrefix + dir,
	})
}

// handleFilesProtectWrite — PUT /files/protect {dir, patterns}: новые
// шаблоны (версией в истории, как правка файла).
func (s *Server) handleFilesProtectWrite(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	var req struct {
		Dir      string `json:"dir"`
		Patterns string `json:"patterns"`
		Note     string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	dir, err := m.Check(req.Dir)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	before := strings.Join(s.protectPatterns(r.Context(), dir), "\n") + "\n"
	after := strings.Join(splitPatterns(req.Patterns), "\n") + "\n"
	if err := s.db.KVSet(r.Context(), protectKeyPrefix+dir, after); err != nil {
		fail(w, r, err)
		return
	}
	user := auth.Username(r.Context())
	if s.configs != nil {
		_, _ = s.configs.RecordDoc(r.Context(), protectPathPrefix+dir, "files", user, store.ActionEdit, strings.TrimSpace(req.Note), []byte(before), []byte(after))
	}
	s.db.Audit(r.Context(), user, "files.protect", dir, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"patterns": splitPatterns(after)})
}

// handleFilesHistory — GET /files/history: занятость, лимиты, крупнейшие.
func (s *Server) handleFilesHistory(w http.ResponseWriter, r *http.Request) {
	if s.configs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.fileBrowserUnavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"usage":   s.configs.FileHistoryUsage(r.Context()),
		"biggest": s.configs.BigFileVersions(r.Context(), 50),
	})
}

// handleFilesHistorySettings — PUT /files/history/settings.
func (s *Server) handleFilesHistorySettings(w http.ResponseWriter, r *http.Request) {
	if s.configs == nil {
		return
	}
	var req control.FileHistoryLimits
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.configs.SetFileHistoryLimits(r.Context(), req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "files.history.settings", "", "ok", req)
	pruned := s.configs.PruneFileHistory(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"usage": s.configs.FileHistoryUsage(r.Context()), "pruned": pruned})
}

// handleFilesHistoryDelete — POST /files/history/delete: версии по ID,
// все версии пути, загрузку целиком или всё старше даты.
func (s *Server) handleFilesHistoryDelete(w http.ResponseWriter, r *http.Request) {
	if s.configs == nil {
		return
	}
	var req struct {
		VersionIDs []int64 `json:"version_ids"`
		Path       string  `json:"path"`
		UploadID   int64   `json:"upload_id"`
		Before     string  `json:"before"` // RFC3339
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	ids := append([]int64(nil), req.VersionIDs...)
	all, err := s.db.FileHistoryVersions(ctx)
	if err != nil {
		fail(w, r, err)
		return
	}
	allowed := map[int64]bool{}
	for _, v := range all {
		allowed[v.ID] = true
		if req.Path != "" && (v.Path == req.Path || strings.HasPrefix(v.Path, strings.TrimRight(req.Path, "/")+"/")) {
			ids = append(ids, v.ID)
		}
		if req.Before != "" && v.TS < req.Before {
			ids = append(ids, v.ID)
		}
	}
	if req.UploadID > 0 {
		items, _ := s.db.UploadItems(ctx, req.UploadID)
		for _, it := range items {
			if it.VersionID > 0 {
				ids = append(ids, it.VersionID)
			}
		}
	}
	// Только версии истории файлов: версии конфигураций здесь не удаляются.
	var del []int64
	seen := map[int64]bool{}
	for _, id := range ids {
		if allowed[id] && !seen[id] {
			seen[id] = true
			del = append(del, id)
		}
	}
	if err := s.configs.DeleteFileVersions(ctx, del); err != nil {
		fail(w, r, err)
		return
	}
	if req.UploadID > 0 {
		_ = s.db.DeleteUpload(ctx, req.UploadID)
	}
	b, _ := json.Marshal(req)
	s.db.Audit(ctx, auth.Username(ctx), "files.history.delete", fmt.Sprintf("%d", len(del)), "ok", string(b))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": len(del), "usage": s.configs.FileHistoryUsage(ctx)})
}

// fileVersionPath — путь версии, доступный проводнику: обычный путь в
// корнях или защищённые шаблоны каталога в корнях.
func fileVersionPath(m *files.Manager, p string) error {
	if dir, ok := strings.CutPrefix(p, protectPathPrefix); ok {
		_, err := m.Check(dir)
		return err
	}
	_, err := m.Check(p)
	return err
}
