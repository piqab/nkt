package api

import (
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"io"
	"mime"
	"net/http"
	"os"
	gopath "path"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/files"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Проводник по каталогам хоста — раздел «Диски → Файлы». Границы задаёт
// files.Manager (корни), здесь только разбор запросов и журнал действий.

func (s *Server) filesOrFail(w http.ResponseWriter, r *http.Request) *files.Manager {
	if s.files == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.fileBrowserUnavailable"))
		return nil
	}
	return s.files
}

func (s *Server) handleFilesRoots(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	roots := m.ExistingRoots()
	out := map[string]any{"max_upload": files.MaxUploadBytes}
	// Старый юнит с ProtectHome=yes: /home для службы пуст, загруженное
	// туда снаружи «не появляется». Предупредить сразу, а не после
	// первой загрузки.
	if os.Getenv("INVOCATION_ID") != "" {
		d := readUnit("netknownsthat.service")
		if d.Found && (d.ProtectHome == "yes" || d.ProtectHome == "true" || d.ProtectHome == "tmpfs") {
			out["warning"] = msgs.Tc(r.Context(), "files.protectHomeWarning", d.Path)
		}
		// Старый юнит с PrivateTmp=yes: /tmp у службы свой — показывать его
		// значило бы показывать не тот каталог, куда кладут файлы снаружи.
		if d.Found && (d.PrivateTmp == "yes" || d.PrivateTmp == "true") {
			kept := roots[:0]
			for _, root := range roots {
				if root != "/tmp" {
					kept = append(kept, root)
				}
			}
			roots = kept
		}
	}
	out["roots"] = roots
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	entries, err := m.List(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

type filesPathRequest struct {
	Path string `json:"path"`
	To   string `json:"to,omitempty"`
	Dest string `json:"dest,omitempty"`
}

func (s *Server) filesMutation(w http.ResponseWriter, r *http.Request, action string,
	do func(m *files.Manager, req filesPathRequest) (string, error)) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	var req filesPathRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	target, err := do(m, req)
	if err != nil {
		s.db.Audit(r.Context(), user, "files."+action, req.Path, "error", err.Error())
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), user, "files."+action, req.Path, "ok", target)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "path": target})
}

func (s *Server) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	s.filesMutation(w, r, "mkdir", func(m *files.Manager, req filesPathRequest) (string, error) {
		return req.Path, m.Mkdir(r.Context(), req.Path)
	})
}

func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	s.filesMutation(w, r, "rename", func(m *files.Manager, req filesPathRequest) (string, error) {
		return req.To, m.Rename(r.Context(), req.Path, req.To)
	})
}

func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	s.filesMutation(w, r, "delete", func(m *files.Manager, req filesPathRequest) (string, error) {
		return req.Path, m.Delete(r.Context(), req.Path)
	})
}

func (s *Server) handleFilesExtract(w http.ResponseWriter, r *http.Request) {
	s.filesMutation(w, r, "extract", func(m *files.Manager, req filesPathRequest) (string, error) {
		dest := req.Dest
		if dest == "" {
			dest = gopath.Dir(req.Path)
		}
		return dest, m.Extract(r.Context(), req.Path, dest)
	})
}

// extendTransfer продлевает сроки соединения на время передачи файла:
// умолчания http.Server (30 с на чтение, 2 мин на запись) рассчитаны на
// обычные запросы, а не на файл в сотни мегабайт.
func extendTransfer(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(files.TransferTimeout)
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
}

// handleFilesUpload принимает тело запроса как файл: ?dir=…&name=…
func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	extendTransfer(w)
	dir := r.URL.Query().Get("dir")
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	user := auth.Username(r.Context())
	target, err := m.Upload(r.Context(), dir, name, http.MaxBytesReader(w, r.Body, files.MaxUploadBytes+1))
	if err != nil {
		s.db.Audit(r.Context(), user, "files.upload", gopath.Join(dir, name), "error", err.Error())
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), user, "files.upload", target, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "path": target})
}

// handleFilesDownload отдаёт файл как есть, с именем для сохранения.
func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	p := r.URL.Query().Get("path")
	rc, size, err := m.Open(p)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	defer rc.Close()
	extendTransfer(w)
	name := gopath.Base(p)
	ctype := mime.TypeByExtension(gopath.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", strings.ReplaceAll(strings.ReplaceAll(name, "%", "%25"), " ", "%20")))
	_, _ = io.Copy(w, rc)
}

func (s *Server) handleFilesDeployKey(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	_, pub, err := m.DeployKey()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": pub})
}

type cloneRequest struct {
	URL      string `json:"url"`
	Branch   string `json:"branch,omitempty"`
	Dir      string `json:"dir"`
	Name     string `json:"name,omitempty"`
	Auth     string `json:"auth"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

// handleFilesClone ставит клонирование заданием: секрет остаётся в памяти
// исполнителя по билету и в параметры задания (в базу) не попадает.
func (s *Server) handleFilesClone(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	if s.jobs == nil || s.cloneRunner == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var req cloneRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	dest, err := files.DestFor(req.Dir, req.URL, req.Name)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	params := files.CloneParams{URL: req.URL, Branch: req.Branch, Dest: dest, Auth: req.Auth, Username: req.Username}
	if err := s.cloneRunner.Prepare(&params, req.Secret); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: files.KindClone, Title: "git clone " + gopath.Base(dest),
		Queue: "files", Author: user, Params: params, Steps: 2,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "files.clone", dest, "ok", req.URL)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "dest": dest})
}

// handleFilesRead отдаёт текст файла для редактора.
func (s *Server) handleFilesRead(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	txt, err := m.Read(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	// Файл — ещё и конфигурация известного сервиса: редактор пишет его
	// как в «Конфигурациях» — с проверкой сервисом и откатом при ошибке.
	out := struct {
		*files.Text
		ConfigService string `json:"config_service,omitempty"`
	}{Text: txt}
	if s.configs != nil {
		if svc, err := s.configs.ServiceForPath(txt.Path); err == nil {
			out.ConfigService = svc
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type filesWriteRequest struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ExpectedSHA256 string `json:"expected_sha256"`
	Name           string `json:"name,omitempty"`
	// Note — заметка к правке, в истории версий.
	Note string `json:"note,omitempty"`
}

// handleFilesWrite записывает правку редактора (и переносит при новом
// имени).
func (s *Server) handleFilesWrite(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil {
		return
	}
	var req filesWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	var before []byte
	if cur, err := m.Read(req.Path); err == nil {
		before = []byte(cur.Content)
	}
	target, err := m.Write(r.Context(), req.Path, req.Content, req.ExpectedSHA256, strings.TrimSpace(req.Name))
	if err != nil {
		s.db.Audit(r.Context(), user, "files.write", req.Path, "error", err.Error())
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), user, "files.write", target, "ok", nil)
	s.recordFileVersion(r, target, store.ActionEdit, strings.TrimSpace(req.Note), before, []byte(req.Content))
	txt, err := m.Read(target)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, txt)
}

// История правок файлов — та же, что у «Конфигураций» (таблица версий по
// пути): у файла-конфига правки из «Файлов» и «Конфигураций» в одном
// списке.

// recordFileVersion — версия после записи; до первой правки заодно
// сохраняется прежнее содержимое, чтобы было куда откатиться.
func (s *Server) recordFileVersion(r *http.Request, path, action, note string, before, after []byte) {
	if s.configs == nil {
		return
	}
	service := "files"
	if svc, err := s.configs.ServiceForPath(path); err == nil {
		service = svc
	}
	_, _ = s.configs.RecordDoc(r.Context(), path, service, auth.Username(r.Context()), action, note, before, after)
}

// fileVersion — версия, путь которой проводник вообще разрешает.
func (s *Server) fileVersion(w http.ResponseWriter, r *http.Request, m *files.Manager) (store.ConfigVersion, string, bool) {
	id, err := int64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "configs.invalidVersionNumber"))
		return store.ConfigVersion{}, "", false
	}
	v, err := s.db.VersionByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return v, "", false
	}
	if _, err := m.Check(v.Path); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return v, "", false
	}
	v, content, err := s.configs.VersionContent(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return v, "", false
	}
	return v, content, true
}

// handleFilesVersions — GET /files/versions?path=: история файла.
func (s *Server) handleFilesVersions(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil || s.configs == nil {
		return
	}
	p, err := m.Check(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	list, err := s.db.ListVersions(r.Context(), p, 200)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list})
}

// handleFilesVersionDiff — GET /files/versions/{id}/diff: версия против
// текущего файла.
func (s *Server) handleFilesVersionDiff(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil || s.configs == nil {
		return
	}
	v, old, ok := s.fileVersion(w, r, m)
	if !ok {
		return
	}
	cur := ""
	if txt, err := m.Read(v.Path); err == nil {
		cur = txt.Content
	}
	diff := control.UnifiedDiff(r.Context(), msgs.Tc(r.Context(), "control.version", v.ID, v.TS), msgs.Tc(r.Context(), "control.currentFile"), old, cur)
	writeJSON(w, http.StatusOK, map[string]string{"diff": diff})
}

// handleFilesVersionRollback — POST /files/versions/{id}/rollback: вернуть
// файл к версии (тоже новой версией «откат»).
func (s *Server) handleFilesVersionRollback(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w, r)
	if m == nil || s.configs == nil {
		return
	}
	v, content, ok := s.fileVersion(w, r, m)
	if !ok {
		return
	}
	user := auth.Username(r.Context())
	expected := ""
	var before []byte
	if cur, err := m.Read(v.Path); err == nil {
		expected, before = cur.SHA256, []byte(cur.Content)
	}
	if _, err := m.Write(r.Context(), v.Path, content, expected, ""); err != nil {
		s.db.Audit(r.Context(), user, "files.rollback", v.Path, "error", err.Error())
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), user, "files.rollback", v.Path, "ok", map[string]any{"version": v.ID})
	s.recordFileVersion(r, v.Path, store.ActionRollback, msgs.Tc(r.Context(), "control.rollbackVersion", v.ID, v.TS), before, []byte(content))
	writeJSON(w, http.StatusOK, map[string]any{"path": v.Path, "message": msgs.Tc(r.Context(), "configs.versionRestored", v.ID)})
}
