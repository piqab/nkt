package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Архивы образов на хосте — «Скачать» на компьютер и «Загрузить» с
// компьютера для Docker, Podman и LXD; каталог тот же, куда «Образы»
// сохраняют docker save (ImageManager.BackupDir). Сохранение, загрузка в
// движок, экспорт и импорт LXD — фоновыми заданиями (?job=1), как
// установка движков: образ — это минуты и гигабайты.
//
// Имена файлов: <движок>__<что>__<время>.<расширение>; старые сохранения
// без приставки — Docker.

var archiveNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+=-]{0,200}$`)

// archiveExtRe — что считается архивом образа.
var archiveExtRe = regexp.MustCompile(`\.(tar|tar\.gz|tgz|tar\.xz|tar\.zst|squashfs|root)$`)

type imageArchive struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
	// Kind — docker | podman | lxd (по приставке имени).
	Kind string `json:"kind"`
}

func archiveKind(name string) string {
	for _, k := range []string{"podman", "lxd", "docker"} {
		if strings.HasPrefix(name, k+"__") {
			return k
		}
	}
	return "docker"
}

func (s *Server) archiveDir() string { return s.images.BackupDir() }

// archivePath — путь архива по имени (проверенному).
func (s *Server) archivePath(name string) (string, error) {
	if !archiveNameRe.MatchString(name) || strings.Contains(name, "..") {
		return "", msgs.Errorf("api.archiveBadName", name)
	}
	return filepath.Join(s.archiveDir(), name), nil
}

// safePart — часть имени файла из ссылки или псевдонима.
func safePart(s string) string {
	s = strings.NewReplacer("/", "_", ":", "_", "@", "_", " ", "_").Replace(s)
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// handleImageArchives — GET /images/archives.
func (s *Server) handleImageArchives(w http.ResponseWriter, r *http.Request) {
	out := []imageArchive{}
	entries, err := os.ReadDir(s.archiveDir())
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !archiveExtRe.MatchString(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, imageArchive{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime().UTC().Format(time.RFC3339), Kind: archiveKind(e.Name())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime > out[j].ModTime })
	writeJSON(w, http.StatusOK, map[string]any{"archives": out, "dir": s.archiveDir()})
}

// handleImageArchiveDownload — GET /images/archives/{name}/download.
func (s *Server) handleImageArchiveDownload(w http.ResponseWriter, r *http.Request) {
	p, err := s.archivePath(chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	defer f.Close()
	serveDownload(w, r, f, filepath.Base(p))
	s.db.Audit(r.Context(), auth.Username(r.Context()), "image.archive_download", filepath.Base(p), "ok", nil)
}

// serveDownload — файл целиком, без предела времени записи (гигабайты).
func serveDownload(w http.ResponseWriter, r *http.Request, f *os.File, name string) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(vmImageUploadTimeout))
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = io.Copy(w, f)
}

// handleImageArchiveDelete — DELETE /images/archives/{name}.
func (s *Server) handleImageArchiveDelete(w http.ResponseWriter, r *http.Request) {
	p, err := s.archivePath(chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	err = os.Remove(p)
	s.db.Audit(r.Context(), auth.Username(r.Context()), "image.archive_delete", filepath.Base(p), auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleImageArchiveUpload — PUT /images/archives/upload?name=: файл с
// компьютера в каталог архивов (потоком; недописанный удаляется).
func (s *Server) handleImageArchiveUpload(w http.ResponseWriter, r *http.Request) {
	// С токеном — передача, заведённая заданием (POST /uploads/begin).
	if t := uploadTokenOf(r); t != "" {
		s.receiveUpload(w, r, t)
		return
	}
	p, err := s.archivePath(strings.TrimSpace(r.URL.Query().Get("name")))
	if err == nil && !archiveExtRe.MatchString(p) {
		err = msgs.Errorf("api.archiveBadName", filepath.Base(p))
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	extendUpload(w)
	if err := os.MkdirAll(s.archiveDir(), 0o700); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	tmp := p + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	user := auth.Username(r.Context())
	if err != nil {
		_ = os.Remove(tmp)
		s.db.Audit(r.Context(), user, "image.archive_upload", filepath.Base(p), "error", err.Error())
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), user, "image.archive_upload", filepath.Base(p), "ok", fmt.Sprint(n))
	out := map[string]any{"name": filepath.Base(p), "size": n}
	// ?load=1 — сразу загрузить в движок заданием, запущенным здесь же, как
	// только файл получен целиком: дальше от открытого окна ничего не
	// зависит.
	if r.URL.Query().Get("load") == "1" {
		id, err := s.startArchiveLoadJob(r.Context(), user, archiveKind(filepath.Base(p)), p)
		if err != nil {
			out["load_error"] = msgs.Localize(msgs.LangFromRequest(r), err)
		} else if id > 0 {
			out["job_id"] = id
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// startArchiveLoadJob — docker (podman) load из архива фоновым заданием.
// 0 без ошибки — загружать некуда (демо-режим, архив LXD).
func (s *Server) startArchiveLoadJob(ctx context.Context, user, engine, path string) (int64, error) {
	if s.cfg.IsFixtures() || s.jobs == nil || (engine != "docker" && engine != "podman") {
		return 0, nil
	}
	name := filepath.Base(path)
	engineName := map[string]string{"docker": "Docker", "podman": "Podman"}[engine]
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: cmdjob.Kind, TitleKey: "archives.loadJobTitle", TitleArgs: []any{name, engineName},
		Queue: "session:image-load:" + engine, Author: user, Steps: 1,
		Params: cmdjob.Params{Commands: []cmdjob.Command{{
			Argv: []string{engine, "load", "-i", path}, StepKey: "archives.stepLoad", StepArgs: []any{engineName},
		}}, Refresh: true},
	})
	if err != nil {
		return 0, err
	}
	s.db.Audit(ctx, user, "image.archive_load", name, "ok", map[string]any{"job_id": id})
	return id, nil
}

// archiveCommand — команда движка заданием (?job=1) или живым выводом.
func (s *Server) archiveCommand(w http.ResponseWriter, r *http.Request, key, audit, target string, argv ...string) {
	if s.cfg.IsFixtures() {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	build := func() *exec.Cmd { return unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, argv...) }
	s.runUpdateSession(w, r, key, build, audit, target, s.cfg.TerminalIdleTimeout)
}

func engineOf(v string) (string, error) {
	switch v {
	case "", "docker":
		return "docker", nil
	case "podman":
		return "podman", nil
	}
	return "", msgs.Errorf("api.archiveBadEngine", v)
}

// handleImageArchiveSave — POST /images/archives/save?engine=&ref=: docker
// (podman) save в каталог архивов.
func (s *Server) handleImageArchiveSave(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	engine, err := engineOf(q.Get("engine"))
	if err == nil {
		err = control.ValidImageRef(q.Get("ref"))
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ref := q.Get("ref")
	if err := os.MkdirAll(s.archiveDir(), 0o700); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	name := fmt.Sprintf("%s__%s__%s.tar", engine, safePart(ref), time.Now().UTC().Format("20060102-150405"))
	s.archiveCommand(w, r, "image-save:"+engine, "image.archive_save", ref, engine, "save", "-o", filepath.Join(s.archiveDir(), name), ref)
}

// handleImageArchiveLoad — POST /images/archives/{name}/load?engine=:
// docker (podman) load из архива.
func (s *Server) handleImageArchiveLoad(w http.ResponseWriter, r *http.Request) {
	engine, err := engineOf(r.URL.Query().Get("engine"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	p, err := s.archivePath(chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if _, err := os.Stat(p); err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	s.archiveCommand(w, r, "image-load:"+engine, "image.archive_load", filepath.Base(p), engine, "load", "-i", p)
}

var lxdFingerprintRe = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
var lxdAliasRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,100}$`)

// handleLXDImageExport — POST /lxd/images/{fp}/export?alias=: lxc image
// export в каталог архивов (один файл или пара «метаданные + корень»).
func (s *Server) handleLXDImageExport(w http.ResponseWriter, r *http.Request) {
	fp := chi.URLParam(r, "fp")
	if !lxdFingerprintRe.MatchString(fp) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("api.archiveBadName", fp))
		return
	}
	label := fp[:12]
	if a := r.URL.Query().Get("alias"); a != "" && lxdAliasRe.MatchString(a) {
		label = safePart(a)
	}
	if err := os.MkdirAll(s.archiveDir(), 0o700); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	base := filepath.Join(s.archiveDir(), fmt.Sprintf("lxd__%s__%s", label, time.Now().UTC().Format("20060102-150405")))
	s.archiveCommand(w, r, "lxd-export", "lxd.image_export", fp, "lxc", "image", "export", fp, base)
}

// handleLXDImageImport — POST /lxd/images/import?meta=&rootfs=&alias=:
// lxc image import из архивов (rootfs — для раздельного образа).
func (s *Server) handleLXDImageImport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	meta, err := s.archivePath(q.Get("meta"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	argv := []string{"lxc", "image", "import", meta}
	if rf := q.Get("rootfs"); rf != "" {
		p, err := s.archivePath(rf)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		argv = append(argv, p)
	}
	if a := q.Get("alias"); a != "" {
		if !lxdAliasRe.MatchString(a) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("api.archiveBadName", a))
			return
		}
		argv = append(argv, "--alias", a)
	}
	s.archiveCommand(w, r, "lxd-import", "lxd.image_import", filepath.Base(meta), argv...)
}

// handleVMImageFileDownload — GET /vm/images/file/download?where=&name=:
// образ машины на компьютер — из библиотеки nkt (where=library) или из
// каталога дисков libvirt (where=host; только файлы из его списка).
func (s *Server) handleVMImageFileDownload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	user := auth.Username(r.Context())
	if r.URL.Query().Get("where") == "host" {
		path := ""
		for _, im := range vmcreate.HostImages(r.Context(), RunTooling) {
			if im.Name == name {
				path = im.Path
			}
		}
		if path == "" || strings.ContainsAny(name, "/\\") {
			writeErr(w, r, http.StatusNotFound, msgs.Errorf("api.archiveBadName", name))
			return
		}
		// Каталог дисков libvirt — у root: читается вне песочницы.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), vmImageUploadTimeout)
		defer cancel()
		cmd := unrestrictedCommand(nil, "cat", "--", path)
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		go func() { <-ctx.Done(); _ = cmd.Process.Kill() }()
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(vmImageUploadTimeout))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		_, _ = io.Copy(w, out)
		_ = cmd.Wait()
		s.db.Audit(r.Context(), user, "vmimage.download", name, "ok", nil)
		return
	}
	if s.vmimages == nil || !archiveNameRe.MatchString(name) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("api.archiveBadName", name))
		return
	}
	f, err := os.Open(filepath.Join(s.vmimages.Dir(), name))
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	defer f.Close()
	serveDownload(w, r, f, name)
	s.db.Audit(r.Context(), user, "vmimage.download", name, "ok", nil)
}
