package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/clamav"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Ключи kv для последних прогонов — как vulnScanKVKey: перезапуск nkt не
// должен выглядеть как «никогда не сканировали».
const (
	clamHostKVKey   = "clamav_last_host"
	clamImagesKVKey = "clamav_last_images"
	clamPathsKVKey  = "clamav_paths"
)

// clamState — последние результаты сканов (живут и в kv). Сами операции
// ClamAV — установка, база, сканы — идут заданиями очереди clamQueue: по
// одной за раз, с журналом в стандартном окне и отменой, которая
// останавливает clamscan, а не только ожидание.
type clamState struct {
	mu     sync.Mutex
	host   *model.ClamScan
	images *model.ClamScan
}

// KindClamAV — задание ClamAV; clamQueue — его очередь (одно за раз:
// freshclam и clamscan делят базу, два скана разом только мешают друг другу).
const (
	KindClamAV = "clamav.run"
	clamQueue  = "clamav"
)

// clamParams — вход задания: операция и что сканировать. Команды
// собирает сервер, клиент присылает только пути и имена образов.
type clamParams struct {
	Op     string   `json:"op"`
	Paths  []string `json:"paths,omitempty"`
	Images []string `json:"images,omitempty"`
	Tool   string   `json:"tool,omitempty"`
}

type clamRunner struct{ s *Server }

func (c *clamRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p clamParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	return c.s.runClam(ctx, jc, p)
}

func (s *Server) quarantineDir() string { return filepath.Join(s.cfg.DataDir, "quarantine") }

// clamPaths — что сканировать на хосте: сохранённый список или умолчание.
func (s *Server) clamPaths(ctx context.Context) []string {
	if raw, ok, err := s.db.KVGet(ctx, clamPathsKVKey); err == nil && ok {
		var paths []string
		if json.Unmarshal([]byte(raw), &paths) == nil && len(paths) > 0 {
			return paths
		}
	}
	return append([]string(nil), clamav.DefaultPaths...)
}

func (s *Server) loadClamScan(ctx context.Context, key string) *model.ClamScan {
	raw, ok, err := s.db.KVGet(ctx, key)
	if err != nil || !ok {
		return nil
	}
	var sc model.ClamScan
	if json.Unmarshal([]byte(raw), &sc) != nil {
		return nil
	}
	return &sc
}

func (s *Server) saveClamScan(ctx context.Context, key string, sc *model.ClamScan) {
	if raw, err := json.Marshal(sc); err == nil {
		if err := s.db.KVSet(ctx, key, string(raw)); err != nil {
			s.log.Warn("could not save clamav scan", "error", err)
		}
	}
}

// activeClamJob — идущее или ждущее задание ClamAV (nil — нет).
func (s *Server) activeClamJob(ctx context.Context) *store.Job {
	if s.jobs == nil {
		return nil
	}
	list, err := s.db.UnfinishedJobs(ctx)
	if err != nil {
		return nil
	}
	for i := range list {
		if list[i].Queue == clamQueue {
			return &list[i]
		}
	}
	return nil
}

// handleClamStatus — всё для карточки ClamAV одним запросом.
func (s *Server) handleClamStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := clamav.Status(ctx, s.scanner.Collector())
	s.clam.mu.Lock()
	if s.clam.host == nil {
		s.clam.host = s.loadClamScan(ctx, clamHostKVKey)
	}
	if s.clam.images == nil {
		s.clam.images = s.loadClamScan(ctx, clamImagesKVKey)
	}
	resp := map[string]any{
		"status":     st,
		"running":    false,
		"host_scan":  s.clam.host,
		"image_scan": s.clam.images,
	}
	s.clam.mu.Unlock()
	if job := s.activeClamJob(ctx); job != nil {
		var p clamParams
		_ = json.Unmarshal([]byte(job.Params), &p)
		resp["running"], resp["op"], resp["job_id"] = true, p.Op, job.ID
	}
	resp["paths"] = s.clamPaths(ctx)
	images := s.runningImages(ctx)
	if images == nil {
		images = []string{}
	}
	resp["images"] = images
	resp["quarantine"] = s.quarantineList()
	resp["apt"] = collect.Which(ctx, s.scanner.Collector(), "apt-get")
	writeJSON(w, http.StatusOK, resp)
}

// startClamJob заводит задание ClamAV и отвечает {job_id}; уже идёт
// другое — 409 с его номером, чтобы окно открылось на нём.
func (s *Server) startClamJob(w http.ResponseWriter, r *http.Request, p clamParams, titleArgs ...any) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "clamav.fixturesDisabled"))
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	if job := s.activeClamJob(r.Context()); job != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": msgs.T(msgs.LangFromRequest(r), "clamav.busy"), "job_id": job.ID})
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindClamAV, Queue: clamQueue, Author: user, Params: p,
		TitleKey: "clamav.job." + p.Op, TitleArgs: titleArgs,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "clamav."+p.Op, strings.Join(append(p.Paths, p.Images...), " "), "ok", map[string]any{"job_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// runClam — тело задания.
func (s *Server) runClam(ctx context.Context, jc *jobs.Context, p clamParams) error {
	switch p.Op {
	case "install":
		jc.StepKey(0, 0, "clamav.installing")
		if err := s.clamStream(ctx, jc, clamav.InstallScript, nil); err != nil {
			return msgs.Errorf("clamav.installFailed", err)
		}
		return nil
	case "update":
		jc.StepKey(0, 0, "clamav.updatingDB")
		if err := s.clamStream(ctx, jc, clamav.UpdateScript, nil); err != nil {
			return msgs.Errorf("clamav.updateFailed", err)
		}
		return nil
	case "scan":
		return s.runClamHostScan(ctx, jc, p.Paths)
	case "scan-images":
		return s.runClamImageScan(ctx, jc, p.Tool, p.Images)
	}
	return msgs.Errorf("clamav.badOp", p.Op)
}

func (s *Server) runClamHostScan(ctx context.Context, jc *jobs.Context, paths []string) error {
	result := &model.ClamScan{Kind: "host", Targets: paths, StartedAt: time.Now(), Hits: []model.ClamHit{}}
	var existing []string
	for _, p := range paths {
		if s.scanner.Collector().Exists(p) {
			existing = append(existing, p)
		} else {
			w := msgs.T(jc.Lang(), "clamav.pathMissing", p)
			result.Warnings = append(result.Warnings, w)
			jc.Logf("! %s", w)
		}
	}
	if len(existing) == 0 {
		return msgs.Errorf("clamav.nothingToScan")
	}
	jc.StepKey(0, 0, "clamav.scanning", strings.Join(existing, ", "))
	err := s.clamStream(ctx, jc, shellJoin(clamav.ScanArgs(existing)), func(line string) {
		if hit, counter, n := clamav.ParseLine(line); hit != nil {
			result.Hits = append(result.Hits, *hit)
			jc.StepKey(0, 0, "clamav.scanningFound", len(result.Hits))
		} else if counter == "Scanned files" {
			result.Scanned = n
		}
	})
	if ctx.Err() != nil {
		// Отменённый скан не затирает прошлый полный результат.
		return ctx.Err()
	}
	// clamscan: 0 — чисто, 1 — найдено, 2 — ошибка.
	if err != nil {
		var ee *exec.ExitError
		if !isExit(err, &ee, 1) {
			result.Error = err.Error()
		}
	}
	result.FinishedAt = time.Now()
	s.clam.mu.Lock()
	s.clam.host = result
	s.clam.mu.Unlock()
	s.saveClamScan(context.Background(), clamHostKVKey, result)
	if result.Error != "" {
		return msgs.Errorf("clamav.scanFailed", result.Error)
	}
	jc.Log("clamav.scanResult", result.Scanned, len(result.Hits))
	return nil
}

func (s *Server) runClamImageScan(ctx context.Context, jc *jobs.Context, tool string, images []string) error {
	if tool != "podman" {
		tool = "docker"
	}
	result := &model.ClamScan{Kind: "images", Targets: images, StartedAt: time.Now(), Hits: []model.ClamHit{}}
	for i, ref := range images {
		jc.StepKey(i+1, len(images), "clamav.scanningImage", i+1, len(images), ref)
		jc.Logf("== %s", ref)
		err := s.clamStream(ctx, jc, clamav.ImageScanScript(tool, ref), func(line string) {
			if hit, counter, n := clamav.ParseLine(line); hit != nil {
				hit.Target = ref
				result.Hits = append(result.Hits, *hit)
			} else if counter == "Scanned files" {
				result.Scanned += n
			}
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			var ee *exec.ExitError
			if !isExit(err, &ee, 1) {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %s", ref, err.Error()))
			}
		}
	}
	result.FinishedAt = time.Now()
	s.clam.mu.Lock()
	s.clam.images = result
	s.clam.mu.Unlock()
	s.saveClamScan(context.Background(), clamImagesKVKey, result)
	jc.Log("clamav.scanResult", result.Scanned, len(result.Hits))
	return nil
}

// clamStream выполняет скрипт вне песочницы, отдавая строки в журнал
// задания и onLine. Отмена задания останавливает всё дерево процессов:
// при systemd — своим юнитом с KillMode=control-group (у обычного
// «тихого» запуска KillMode=process, и clamscan пережил бы остановку sh),
// без него — группой процессов.
func (s *Server) clamStream(ctx context.Context, jc *jobs.Context, script string, onLine func(string)) error {
	env := map[string]string{"DEBIAN_FRONTEND": "noninteractive", "LC_ALL": "C"}
	var cmd *exec.Cmd
	unit := ""
	if usingSystemdSandbox() {
		unit = fmt.Sprintf("nkt-clamav-%d-%d", jc.Job.ID, time.Now().UnixNano()%1_000_000)
		args := systemdRunQuietArgs(env, "sh", "-c", script)
		for i, a := range args {
			if a == "KillMode=process" {
				args[i] = "KillMode=control-group"
			}
		}
		cmd = exec.Command("systemd-run", append([]string{"--unit=" + unit}, args...)...)
	} else {
		cmd = unrestrictedQuietCommand(context.Background(), env, "sh", "-c", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-stop:
		case <-ctx.Done():
			jc.Log("clamav.stopping")
			if unit != "" {
				_, _ = RunUnrestricted(context.Background(), "systemctl", "stop", unit)
			} else if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			}
		}
	}()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		jc.Logf("%s", line)
		if onLine != nil {
			onLine(line)
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (s *Server) handleClamInstall(w http.ResponseWriter, r *http.Request) {
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	s.startClamJob(w, r, clamParams{Op: "install"})
}

func (s *Server) handleClamUpdateDB(w http.ResponseWriter, r *http.Request) {
	s.startClamJob(w, r, clamParams{Op: "update"})
}

// handleClamScan сканирует каталоги хоста; список сохраняется как
// умолчание для следующего раза.
func (s *Server) handleClamScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	paths := req.Paths
	if len(paths) == 0 {
		paths = s.clamPaths(r.Context())
	}
	for _, p := range paths {
		if !clamav.ValidPath(p) {
			writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "clamav.badPath", p))
			return
		}
	}
	if raw, err := json.Marshal(paths); err == nil {
		_ = s.db.KVSet(r.Context(), clamPathsKVKey, string(raw))
	}
	s.startClamJob(w, r, clamParams{Op: "scan", Paths: paths}, strings.Join(paths, ", "))
}

// handleClamScanImages сканирует образы контейнеров: выбранные или все,
// что запущены на хосте.
func (s *Server) handleClamScanImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Images []string `json:"images"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	images := req.Images
	if len(images) == 0 {
		images = s.runningImages(r.Context())
	}
	if len(images) == 0 {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "clamav.noImages"))
		return
	}
	for _, ref := range images {
		if ref == "" || strings.ContainsAny(ref, " \n\t\x00") || strings.HasPrefix(ref, "-") {
			writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "clamav.badImage", ref))
			return
		}
	}
	tool := "docker"
	if !collect.Which(r.Context(), s.scanner.Collector(), "docker") && collect.Which(r.Context(), s.scanner.Collector(), "podman") {
		tool = "podman"
	}
	s.startClamJob(w, r, clamParams{Op: "scan-images", Images: images, Tool: tool}, len(images))
}

// handleClamCancel — для старого интерфейса: отменяет идущее задание ClamAV.
func (s *Server) handleClamCancel(w http.ResponseWriter, r *http.Request) {
	if job := s.activeClamJob(r.Context()); job != nil && s.jobs != nil {
		if err := s.jobs.Cancel(r.Context(), job.ID); err != nil {
			writeErr(w, r, http.StatusConflict, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleClamQuarantine убирает файл в карантин: переносится в каталог
// данных nkt без прав на выполнение, рядом — запись, откуда он. Удалять
// сразу нельзя: ложное срабатывание на нужный файл бывает.
func (s *Server) handleClamQuarantine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path      string `json:"path"`
		Signature string `json:"signature"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !clamav.ValidPath(req.Path) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "clamav.badPath", req.Path))
		return
	}
	dir := s.quarantineDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	name := clamav.QuarantineName(req.Path, now)
	dest := filepath.Join(dir, name)
	// mv — вне песочницы: исходный файл где угодно на хосте; каталог
	// карантина — StateDirectory юнита, туда запись разрешена.
	res, err := RunUnrestricted(r.Context(), "sh", "-c", fmt.Sprintf("mv -f %s %s && chmod 000 %s", shellQuote(req.Path), shellQuote(dest), shellQuote(dest)))
	user := auth.Username(r.Context())
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("%s", strings.TrimSpace(res.Stderr))
	}
	s.db.Audit(r.Context(), user, "clamav.quarantine", req.Path, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, msgs.Errorf("clamav.quarantineFailed", req.Path, err))
		return
	}
	var size int64
	if st, err := os.Stat(dest); err == nil {
		size = st.Size()
	}
	meta := model.QuarantineItem{ID: name, Original: req.Path, Signature: req.Signature, Size: size, At: now}
	if raw, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(dest+".json", raw, 0o600)
	}
	writeJSON(w, http.StatusOK, meta)
}

// handleClamRestore возвращает файл из карантина на место.
func (s *Server) handleClamRestore(w http.ResponseWriter, r *http.Request) {
	s.clamQuarantineAction(w, r, "restore")
}

// handleClamPurge удаляет файл из карантина насовсем.
func (s *Server) handleClamPurge(w http.ResponseWriter, r *http.Request) {
	s.clamQuarantineAction(w, r, "purge")
}

func (s *Server) clamQuarantineAction(w http.ResponseWriter, r *http.Request, action string) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.ID == "" || strings.ContainsAny(req.ID, "/\\") || strings.Contains(req.ID, "..") {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "clamav.badQuarantineID"))
		return
	}
	file := filepath.Join(s.quarantineDir(), req.ID)
	raw, err := os.ReadFile(file + ".json")
	if err != nil {
		writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "clamav.quarantineNotFound"))
		return
	}
	var meta model.QuarantineItem
	_ = json.Unmarshal(raw, &meta)
	user := auth.Username(r.Context())
	if action == "restore" {
		res, err := RunUnrestricted(r.Context(), "sh", "-c", fmt.Sprintf("chmod 644 %s && mv -f %s %s", shellQuote(file), shellQuote(file), shellQuote(meta.Original)))
		if err == nil && res.ExitCode != 0 {
			err = fmt.Errorf("%s", strings.TrimSpace(res.Stderr))
		}
		s.db.Audit(r.Context(), user, "clamav.restore", meta.Original, auditResult(err), errText(err))
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
	} else {
		err := os.Remove(file)
		s.db.Audit(r.Context(), user, "clamav.purge", meta.Original, auditResult(err), errText(err))
		if err != nil && !os.IsNotExist(err) {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
	}
	_ = os.Remove(file + ".json")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) quarantineList() []model.QuarantineItem {
	items := []model.QuarantineItem{}
	matches, _ := filepath.Glob(filepath.Join(s.quarantineDir(), "*.json"))
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var it model.QuarantineItem
		if json.Unmarshal(raw, &it) == nil && it.ID != "" {
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].At.After(items[j].At) })
	return items
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

// isExit — завершилась ли команда именно с кодом code.
func isExit(err error, ee **exec.ExitError, code int) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*ee = e
		return e.ExitCode() == code
	}
	return false
}
