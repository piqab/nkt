package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/parse"
)

// Шаги переноса на хосте: упаковка (старый хост), файлы туда и обратно,
// распаковка с подъёмом (новый), откат, запуск и уборка старого.

// MigrateHostQueue — очередь шагов переноса на хосте. У задания хаба —
// своя (hub.migrateHubQueue): на машине хаба менеджер заданий общий, и
// одна очередь на двоих — взаимная блокировка (задание хаба ждёт шаг,
// шаг ждёт в очереди за ним).
const MigrateHostQueue = "migrate"

// migrateMaxBytes — предел одного файла переноса (тома бывают большими).
const migrateMaxBytes = 500 << 30

var (
	migrateIDRe   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	migrateFileRe = regexp.MustCompile(`^((vol|path)-\d{1,4}\.tgz|img-\d{1,4}\.tar|manifest\.json)$`)
)

// migrateFile — файл переноса: том, каталог или образ.
type migrateFile struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Volume string `json:"volume,omitempty"`
	Path   string `json:"path,omitempty"`
	Image  string `json:"image,omitempty"`
	Size   int64  `json:"size"`
}

// migrateManifest — что упаковано (лежит в manifest.json переноса).
type migrateManifest struct {
	Plan  MigratePlan   `json:"plan"`
	Files []migrateFile `json:"files"`
}

// migrateFilePath — путь файла переноса id/name: имена — по образцам, а
// итоговый путь (очищенный) обязан лежать внутри каталога переносов.
func (s *Server) migrateFilePath(id, name string) (string, string, error) {
	if !migrateIDRe.MatchString(id) {
		return "", "", msgs.Errorf("migrate.badID", id)
	}
	if !migrateFileRe.MatchString(name) || filepath.Base(name) != name {
		return "", "", msgs.Errorf("migrate.badFile", name)
	}
	base := filepath.Clean(filepath.Join(s.archiveDir(), "migrate"))
	dir := filepath.Clean(filepath.Join(base, id))
	p := filepath.Clean(filepath.Join(dir, name))
	if !strings.HasPrefix(dir, base+string(filepath.Separator)) || !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return "", "", msgs.Errorf("migrate.badFile", name)
	}
	return dir, p, nil
}

func (s *Server) migrateDir(id string) (string, error) {
	if !migrateIDRe.MatchString(id) {
		return "", msgs.Errorf("migrate.badID", id)
	}
	base := filepath.Clean(filepath.Join(s.archiveDir(), "migrate"))
	dir := filepath.Clean(filepath.Join(base, id))
	if !strings.HasPrefix(dir, base+string(filepath.Separator)) {
		return "", msgs.Errorf("migrate.badID", id)
	}
	return dir, nil
}

func (s *Server) readManifest(id string) (migrateManifest, string, error) {
	return s.readManifestIn(s.archiveDir(), id)
}

func (s *Server) readManifestIn(archives, id string) (migrateManifest, string, error) {
	var m migrateManifest
	if !migrateIDRe.MatchString(id) {
		return m, "", msgs.Errorf("migrate.badID", id)
	}
	dir := filepath.Join(archives, "migrate", id)
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, dir, msgs.Errorf("migrate.noManifest", id)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, dir, err
	}
	// Манифест пришёл с другого хоста: всё, что из него станет путём или
	// именем, — проверено здесь.
	for _, f := range m.Files {
		if !migrateFileRe.MatchString(f.Name) {
			return m, dir, msgs.Errorf("migrate.badFile", f.Name)
		}
		if f.Kind == "path" && !migrateSafePath(f.Path) {
			return m, dir, msgs.Errorf("migrate.badPath", f.Path)
		}
		if f.Kind == "volume" && !migrateNameRe.MatchString(f.Volume) {
			return m, dir, msgs.Errorf("migrate.badName", f.Volume)
		}
	}
	if !migrateNameRe.MatchString(m.Plan.Project) || (m.Plan.WorkingDir != "" && !migrateSafePath(m.Plan.WorkingDir)) {
		return m, dir, msgs.Errorf("migrate.badName", m.Plan.Project)
	}
	for _, f := range m.Plan.ConfigFiles {
		if !migrateSafePath(f) {
			return m, dir, msgs.Errorf("migrate.badPath", f)
		}
	}
	return m, dir, nil
}

// migratePackArgs — упаковка на старом хосте.
type migratePackArgs struct {
	ID         string   `json:"id"`
	Container  string   `json:"container"`
	SaveImages []string `json:"save_images,omitempty"`
}

// migrateIDArgs — шаг по уже упакованному переносу.
type migrateIDArgs struct {
	ID string `json:"id"`
}

// migrateRemoveArgs — убрать старый стек.
type migrateRemoveArgs struct {
	Containers []string `json:"containers"`
	Volumes    []string `json:"volumes,omitempty"`
	// Paths — каталоги старой копии (рабочий каталог, bind-каталоги).
	Paths []string `json:"paths,omitempty"`
	// Project — compose-проект: его собственные сети (по метке) — прочь.
	Project string `json:"project,omitempty"`
}

func (s *Server) startMigrateJob(ctx context.Context, user, op, titleKey, title string, args any) (int64, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return 0, err
	}
	return s.jobs.Start(ctx, jobs.Spec{
		Kind: KindHostOp, TitleKey: titleKey, TitleArgs: []any{title},
		Queue: MigrateHostQueue, Author: user, Steps: 1,
		Params: HostOpParams{Op: op, Args: raw},
	})
}

func runLogged(ctx context.Context, jc *jobs.Context, argv ...string) error {
	jc.Logf("$ %s", strings.Join(argv, " "))
	code, err := RunToolingStream(ctx, func(format string, a ...any) { jc.Logf(format, a...) }, argv...)
	if err == nil && code != 0 {
		err = msgs.Errorf("cmdjob.exitCode", code)
	}
	return err
}

// migratePack — остановить стек и упаковать данные и образы.
func (s *Server) migratePack(ctx context.Context, jc *jobs.Context, a migratePackArgs) (err error) {
	plan, err := s.migratePlan(ctx, a.Container)
	if err != nil {
		return err
	}
	if len(plan.Refuse) > 0 {
		return msgs.Errorf("migrate.refused", strings.Join(plan.Refuse, ", "))
	}
	dir, err := s.migrateDir(a.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var names []string
	for _, c := range plan.Containers {
		names = append(names, c.Name)
	}
	jc.StepKey(1, 1, "migrate.stepPack", plan.Project)
	// Данные копируются остановленными — иначе база на новом хосте может
	// оказаться несогласованной.
	if err := runLogged(ctx, jc, append([]string{"docker", "stop"}, names...)...); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			jc.Log("migrate.packFailedRestart")
			_ = runLogged(context.WithoutCancel(ctx), jc, append([]string{"docker", "start"}, names...)...)
			_ = os.RemoveAll(dir)
		}
	}()
	m := migrateManifest{Plan: plan}
	for i, v := range plan.Volumes {
		name := fmt.Sprintf("vol-%d.tgz", i+1)
		jc.Log("migrate.packVolume", v.Name)
		// Через контейнер (остановленный — тоже): tar его каталога тома.
		file := filepath.Join(dir, name)
		if err := runLogged(ctx, jc, "sh", "-c", `docker cp "$1" - > "$2.tar" && gzip -f "$2.tar" && mv -f "$2.tar.gz" "$2"`, "_", v.Container+":"+v.Dest, file); err != nil {
			return err
		}
		m.Files = append(m.Files, migrateFile{Name: name, Kind: "volume", Volume: v.Name})
	}
	for i, p := range plan.Paths {
		name := fmt.Sprintf("path-%d.tgz", i+1)
		jc.Log("migrate.packPath", p.Path)
		if err := runLogged(ctx, jc, "tar", "czf", filepath.Join(dir, name), "-C", path.Dir(p.Path), "--", path.Base(p.Path)); err != nil {
			return err
		}
		m.Files = append(m.Files, migrateFile{Name: name, Kind: "path", Path: p.Path})
	}
	want := map[string]bool{}
	for _, img := range a.SaveImages {
		want[img] = true
	}
	n := 0
	for _, img := range plan.Images {
		if !want[img] {
			continue
		}
		n++
		name := fmt.Sprintf("img-%d.tar", n)
		jc.Log("migrate.packImage", img)
		if err := runLogged(ctx, jc, "docker", "save", "-o", filepath.Join(dir, name), img); err != nil {
			return err
		}
		m.Files = append(m.Files, migrateFile{Name: name, Kind: "image", Image: img})
	}
	// Файлы созданы от root: читать их будет сам nkt, отдавая хабу.
	if len(m.Files) > 0 {
		args := []string{"sh", "-c", `chmod a+r -- "$@"`, "_"}
		for _, f := range m.Files {
			args = append(args, filepath.Join(dir, f.Name))
		}
		if err := runLogged(ctx, jc, args...); err != nil {
			return err
		}
	}
	for i, f := range m.Files {
		if st, err := os.Stat(filepath.Join(dir, f.Name)); err == nil {
			m.Files[i].Size = st.Size()
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		return err
	}
	jc.Log("migrate.packed", len(m.Files))
	return nil
}

// migrateCreated — что распаковка создала (для отката на новом хосте).
type migrateCreated struct {
	Volumes []string `json:"volumes"`
	Paths   []string `json:"paths"`
}

func (s *Server) saveCreated(dir string, c migrateCreated) {
	if raw, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "created.json"), raw, 0o600)
	}
}

// migrateComposeArgs — docker compose для стека переноса.
func migrateComposeArgs(p MigratePlan) []string {
	if p.Kind == "standalone" {
		dir := parse.ComposeStacksDir + "/" + p.Project
		return []string{"docker", "compose", "-p", p.Project, "--project-directory", dir, "-f", dir + "/docker-compose.yml"}
	}
	args := []string{"docker", "compose", "-p", p.Project}
	if p.WorkingDir != "" {
		args = append(args, "--project-directory", p.WorkingDir)
	}
	for _, f := range p.ConfigFiles {
		args = append(args, "-f", f)
	}
	return args
}

// migrateUnpack — тома, каталоги, образы, compose-файл и подъём.
func (s *Server) migrateUnpack(ctx context.Context, jc *jobs.Context, a migrateIDArgs) error {
	m, dir, err := s.readManifest(a.ID)
	if err != nil {
		return err
	}
	p := m.Plan
	jc.StepKey(1, 1, "migrate.stepUnpack", p.Project)
	c := s.scanner.Collector()
	// Что создано — в created.json после каждого шага (для отката).
	var created migrateCreated
	// Каталоги (рабочий каталог проекта, bind-тома) и образы — сначала:
	// без них compose не создаст контейнеры.
	for _, f := range m.Files {
		file := filepath.Join(dir, f.Name)
		switch f.Kind {
		case "path":
			if c.Exists(f.Path) {
				return msgs.Errorf("migrate.pathExists", f.Path)
			}
			jc.Log("migrate.unpackPath", f.Path)
			if err := runLogged(ctx, jc, "sh", "-c", `mkdir -p -- "$1" && tar xzf "$2" -C "$1"`, "_", path.Dir(f.Path), file); err != nil {
				return err
			}
			created.Paths = append(created.Paths, f.Path)
			s.saveCreated(dir, created)
		case "image":
			jc.Log("migrate.unpackImage", f.Image)
			if err := runLogged(ctx, jc, "docker", "load", "-i", file); err != nil {
				return err
			}
		}
	}
	if p.Kind == "standalone" {
		stack := parse.ComposeStacksDir + "/" + p.Project
		if c.Exists(stack) {
			return msgs.Errorf("migrate.pathExists", stack)
		}
		if err := c.WriteFile(stack+"/docker-compose.yml", []byte(p.Compose), 0o644); err != nil {
			return err
		}
		created.Paths = append(created.Paths, stack)
		s.saveCreated(dir, created)
		if p.Env != "" {
			if err := c.WriteFile(stack+"/.env", []byte(p.Env), 0o600); err != nil {
				return err
			}
		}
		// У одиночного тома в compose — external: создаются заранее.
		for _, v := range p.Volumes {
			if err := runLogged(ctx, jc, "docker", "volume", "create", v.Name); err != nil {
				return err
			}
			created.Volumes = append(created.Volumes, v.Name)
			s.saveCreated(dir, created)
		}
	}
	// Контейнеры и тома (с метками compose) — не запуская; данные томов —
	// в них через docker cp; потом подъём.
	if err := runLogged(ctx, jc, append(migrateComposeArgs(p), "up", "--no-start")...); err != nil {
		return err
	}
	for _, f := range m.Files {
		if f.Kind != "volume" {
			continue
		}
		var vol MigrateVolume
		for _, v := range p.Volumes {
			if v.Name == f.Volume {
				vol = v
			}
		}
		if !migrateNameRe.MatchString(vol.Container) || !strings.HasPrefix(vol.Dest, "/") || strings.Contains(vol.Dest, "..") {
			return msgs.Errorf("migrate.badPath", vol.Dest)
		}
		jc.Log("migrate.unpackVolume", f.Volume)
		if err := runLogged(ctx, jc, "sh", "-c", `gunzip -c "$1" | docker cp - "$2"`, "_", filepath.Join(dir, f.Name), vol.Container+":"+path.Dir(vol.Dest)); err != nil {
			return err
		}
	}
	jc.Log("migrate.up", p.Project)
	if err := runLogged(ctx, jc, append(migrateComposeArgs(p), "up", "-d", "--wait", "--wait-timeout", "300")...); err != nil {
		return err
	}
	jc.Log("migrate.upDone", p.Project)
	s.rescanLater()
	return nil
}

// migrateUndo — откат распаковки: стек вниз, созданные тома и каталоги —
// прочь (их не было до переноса).
func (s *Server) migrateUndo(ctx context.Context, jc *jobs.Context, a migrateIDArgs) error {
	m, dir, err := s.readManifest(a.ID)
	if err != nil {
		return err
	}
	jc.StepKey(1, 1, "migrate.stepUndo", m.Plan.Project)
	// Стек и его тома здесь созданы этим переносом (проверка убедилась,
	// что их не было), поэтому — с томами.
	_ = runLogged(ctx, jc, append(migrateComposeArgs(m.Plan), "down", "-v")...)
	var created migrateCreated
	if raw, err := os.ReadFile(filepath.Join(dir, "created.json")); err == nil {
		_ = json.Unmarshal(raw, &created)
	}
	for _, v := range created.Volumes {
		if migrateNameRe.MatchString(v) {
			_ = runLogged(ctx, jc, "docker", "volume", "rm", v)
		}
	}
	for _, p := range created.Paths {
		if migrateSafePath(p) {
			_ = runLogged(ctx, jc, "sh", "-c", `rm -rf -- "$1"`, "_", p)
		}
	}
	s.rescanLater()
	return nil
}

// migrateRemove — убрать старый стек (контейнеры, по желанию — тома).
func (s *Server) migrateRemove(ctx context.Context, jc *jobs.Context, a migrateRemoveArgs) error {
	jc.StepKey(1, 1, "migrate.stepRemove", strings.Join(a.Containers, ", "))
	for _, n := range append(append([]string{}, a.Containers...), a.Volumes...) {
		if !migrateNameRe.MatchString(n) {
			return msgs.Errorf("migrate.badName", n)
		}
	}
	for _, p := range a.Paths {
		if !migrateSafePath(p) {
			return msgs.Errorf("migrate.badPath", p)
		}
	}
	// Уже убранное (повтор, ручная уборка) — не ошибка: остатки ищутся
	// заново, удаляется то, что есть.
	if len(a.Containers) > 0 {
		args := []string{"sh", "-c", `for c in "$@"; do docker rm -f "$c" >/dev/null 2>&1 || true; done`, "_"}
		if err := runLogged(ctx, jc, append(args, a.Containers...)...); err != nil {
			return err
		}
	}
	for _, v := range a.Volumes {
		if res, err := RunTooling(ctx, "docker", "volume", "inspect", v); err == nil && res.OK() {
			if err := runLogged(ctx, jc, "docker", "volume", "rm", v); err != nil {
				return err
			}
		}
	}
	if a.Project != "" && migrateNameRe.MatchString(a.Project) {
		if res, err := RunTooling(ctx, "docker", "network", "ls", "-q", "--filter", "label=com.docker.compose.project="+a.Project); err == nil && res.OK() {
			for _, id := range strings.Fields(res.Stdout) {
				_ = runLogged(ctx, jc, "docker", "network", "rm", id)
			}
		}
	}
	for _, p := range a.Paths {
		jc.Log("migrate.removePath", p)
		if err := runLogged(ctx, jc, "sh", "-c", `rm -rf -- "$1"`, "_", p); err != nil {
			return err
		}
	}
	s.rescanLater()
	return nil
}

// runMigrateOp — операции переноса в задании хоста (op_jobs.go).
func (s *Server) runMigrateOp(ctx context.Context, jc *jobs.Context, op string, raw json.RawMessage) (handled bool, err error) {
	// Шаги меняют контейнеры хоста: снимок — сразу, до конца задания.
	// Хаб читает список контейнеров после него, и старый снимок (до пяти
	// минут) показал бы остановленное работающим.
	if strings.HasPrefix(op, "migrate.") {
		defer func() { _, _ = s.scanner.Scan(context.WithoutCancel(ctx)) }()
	}
	switch op {
	case "migrate.pack":
		var a migratePackArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return true, err
		}
		return true, s.migratePack(ctx, jc, a)
	case "migrate.unpack":
		var a migrateIDArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return true, err
		}
		return true, s.migrateUnpack(ctx, jc, a)
	case "migrate.undo":
		var a migrateIDArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return true, err
		}
		return true, s.migrateUndo(ctx, jc, a)
	case "migrate.remove":
		var a migrateRemoveArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return true, err
		}
		return true, s.migrateRemove(ctx, jc, a)
	}
	return false, nil
}

// --- HTTP ---

// handleMigrateJob — POST /migrate/{op} (pack, unpack, undo, remove):
// шаг заданием хоста.
func (s *Server) handleMigrateJob(w http.ResponseWriter, r *http.Request) {
	op := chi.URLParam(r, "op")
	var raw json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	titles := map[string]string{"pack": "migrate.jobPack", "unpack": "migrate.jobUnpack", "undo": "migrate.jobUndo", "remove": "migrate.jobRemove"}
	key, ok := titles[op]
	if !ok {
		writeError(w, http.StatusNotFound, msgs.Tc(r.Context(), "hostop.unknown", op))
		return
	}
	user := auth.Username(r.Context())
	var title struct {
		ID         string   `json:"id"`
		Container  string   `json:"container"`
		Containers []string `json:"containers"`
	}
	_ = json.Unmarshal(raw, &title)
	label := title.Container + strings.Join(title.Containers, ", ") + title.ID
	id, err := s.startMigrateJob(r.Context(), user, "migrate."+op, key, label, raw)
	s.db.Audit(r.Context(), user, "migrate."+op, label, auditResult(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// handleMigrateStart — POST /migrate/start {containers}: запустить
// (откат на старом хосте) или POST /migrate/stop — остановить.
func (s *Server) handleMigrateStartStop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Containers []string `json:"containers"`
	}
	if err := decodeJSON(r, &req); err != nil || len(req.Containers) == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "migrate.badName", ""))
		return
	}
	for _, n := range req.Containers {
		if !migrateNameRe.MatchString(n) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("migrate.badName", n))
			return
		}
	}
	verb := "start"
	if strings.HasSuffix(r.URL.Path, "/stop") {
		verb = "stop"
	}
	ctx, cancel := longCall(w, r)
	defer cancel()
	res, err := RunTooling(ctx, append([]string{"docker", verb}, req.Containers...)...)
	if err == nil && !res.OK() {
		err = msgs.Errorf("migrate.dockerFailed", verb, strings.TrimSpace(res.Output()))
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "migrate."+verb, strings.Join(req.Containers, ", "), auditResult(err), errText(err))
	// Снимок — до ответа: хаб сразу перечитывает список контейнеров.
	_, _ = s.scanner.Scan(context.WithoutCancel(ctx))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMigrateFileGet — GET /migrate/files/{id}/{name}: файл переноса.
func (s *Server) handleMigrateFileGet(w http.ResponseWriter, r *http.Request) {
	_, p, err := s.migrateFilePath(chi.URLParam(r, "id"), chi.URLParam(r, "name"))
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
}

// handleMigrateFilePut — PUT /migrate/files/{id}/{name}: принять файл
// (потоком; недописанный удаляется).
func (s *Server) handleMigrateFilePut(w http.ResponseWriter, r *http.Request) {
	dir, final, err := s.migrateFilePath(chi.URLParam(r, "id"), chi.URLParam(r, "name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	extendUpload(w)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	tmp := final + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, migrateMaxBytes))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, final)
	}
	if err != nil {
		_ = os.Remove(tmp)
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"size": n})
}

// handleMigrateStagingDelete — DELETE /migrate/staging/{id}: убрать файлы
// переноса.
func (s *Server) handleMigrateStagingDelete(w http.ResponseWriter, r *http.Request) {
	dir, err := s.migrateDir(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	_ = os.RemoveAll(dir)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
