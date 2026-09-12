package api

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	gopath "path"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/files"
	"github.com/piqab/nkt/internal/jobs"
)

// Проводник по каталогам хоста — раздел «Диски → Файлы». Границы задаёт
// files.Manager (корни), здесь только разбор запросов и журнал действий.

func (s *Server) filesOrFail(w http.ResponseWriter) *files.Manager {
	if s.files == nil {
		writeError(w, http.StatusServiceUnavailable, "проводник недоступен в этом режиме")
		return nil
	}
	return s.files
}

func (s *Server) handleFilesRoots(w http.ResponseWriter, _ *http.Request) {
	m := s.filesOrFail(w)
	if m == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roots": m.Roots(), "max_upload": files.MaxUploadBytes})
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w)
	if m == nil {
		return
	}
	entries, err := m.List(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	m := s.filesOrFail(w)
	if m == nil {
		return
	}
	var req filesPathRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	target, err := do(m, req)
	if err != nil {
		s.db.Audit(r.Context(), user, "files."+action, req.Path, "error", err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
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
	m := s.filesOrFail(w)
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "files.upload", target, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "path": target})
}

// handleFilesDownload отдаёт файл как есть, с именем для сохранения.
func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w)
	if m == nil {
		return
	}
	p := r.URL.Query().Get("path")
	rc, size, err := m.Open(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

func (s *Server) handleFilesDeployKey(w http.ResponseWriter, _ *http.Request) {
	m := s.filesOrFail(w)
	if m == nil {
		return
	}
	_, pub, err := m.DeployKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	m := s.filesOrFail(w)
	if m == nil {
		return
	}
	if s.jobs == nil || s.cloneRunner == nil {
		writeError(w, http.StatusServiceUnavailable, "фоновые задания недоступны")
		return
	}
	var req cloneRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dest, err := files.DestFor(req.Dir, req.URL, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params := files.CloneParams{URL: req.URL, Branch: req.Branch, Dest: dest, Auth: req.Auth, Username: req.Username}
	if err := s.cloneRunner.Prepare(&params, req.Secret); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: files.KindClone, Title: "git clone " + gopath.Base(dest),
		Queue: "files", Author: user, Params: params, Steps: 2,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "files.clone", dest, "ok", req.URL)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "dest": dest})
}

// handleFilesRead отдаёт текст файла для редактора.
func (s *Server) handleFilesRead(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w)
	if m == nil {
		return
	}
	txt, err := m.Read(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, txt)
}

type filesWriteRequest struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ExpectedSHA256 string `json:"expected_sha256"`
	Name           string `json:"name,omitempty"`
}

// handleFilesWrite записывает правку редактора (и переносит при новом
// имени).
func (s *Server) handleFilesWrite(w http.ResponseWriter, r *http.Request) {
	m := s.filesOrFail(w)
	if m == nil {
		return
	}
	var req filesWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	target, err := m.Write(r.Context(), req.Path, req.Content, req.ExpectedSHA256, strings.TrimSpace(req.Name))
	if err != nil {
		s.db.Audit(r.Context(), user, "files.write", req.Path, "error", err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "files.write", target, "ok", nil)
	txt, err := m.Read(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, txt)
}
