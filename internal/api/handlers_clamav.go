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
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/clamav"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// Ключи kv для последних прогонов — как vulnScanKVKey: перезапуск nkt не
// должен выглядеть как «никогда не сканировали».
const (
	clamHostKVKey   = "clamav_last_host"
	clamImagesKVKey = "clamav_last_images"
	clamPathsKVKey  = "clamav_paths"
	clamLogKeep     = 200
)

// clamState — одна операция ClamAV за раз (установка, обновление базы,
// скан хоста, скан образов) с потоковым журналом: clamscan по большому
// каталогу идёт минуты, и оператору нужно видеть, что он не завис.
type clamState struct {
	mu       sync.Mutex
	running  bool
	op       string
	progress string
	log      []string
	lastErr  string
	host     *model.ClamScan
	images   *model.ClamScan
	cancel   context.CancelFunc
}

func (c *clamState) append(line string) {
	c.mu.Lock()
	c.log = append(c.log, line)
	if len(c.log) > clamLogKeep {
		c.log = c.log[len(c.log)-clamLogKeep:]
	}
	c.mu.Unlock()
}

func (c *clamState) setProgress(p string) {
	c.mu.Lock()
	c.progress = p
	c.mu.Unlock()
}

// begin занимает состояние под операцию op; false — уже что-то идёт.
func (c *clamState) begin(op string) (context.Context, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.running, c.op, c.progress, c.lastErr, c.log, c.cancel = true, op, "", "", nil, cancel
	return ctx, true
}

func (c *clamState) end(err error) {
	c.mu.Lock()
	c.running, c.op, c.progress = false, "", ""
	if err != nil {
		c.lastErr = err.Error()
	}
	c.cancel = nil
	c.mu.Unlock()
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
		"running":    s.clam.running,
		"op":         s.clam.op,
		"progress":   s.clam.progress,
		"log":        append([]string{}, s.clam.log...),
		"error":      s.clam.lastErr,
		"host_scan":  s.clam.host,
		"image_scan": s.clam.images,
	}
	s.clam.mu.Unlock()
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

// startClamOp запускает операцию в фоне; занято — 409.
func (s *Server) startClamOp(w http.ResponseWriter, r *http.Request, op string, run func(ctx context.Context) error) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "clamav.fixturesDisabled"))
		return
	}
	ctx, ok := s.clam.begin(op)
	if !ok {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "clamav.busy"))
		return
	}
	ctx = msgs.WithLang(ctx, msgs.FromContext(r.Context()))
	user := auth.Username(r.Context())
	go func() {
		err := run(ctx)
		s.db.Audit(context.Background(), user, "clamav."+op, "", auditResult(err), errText(err))
		s.clam.end(err)
	}()
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// streamScript выполняет скрипт вне песочницы и отдаёт строки вывода
// построчно — и в журнал операции, и вызывающему для разбора.
func (s *Server) streamScript(ctx context.Context, script string, onLine func(string)) error {
	cmd := unrestrictedQuietCommand(ctx, map[string]string{"DEBIAN_FRONTEND": "noninteractive", "LC_ALL": "C"}, "sh", "-c", script)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		s.clam.append(line)
		if onLine != nil {
			onLine(line)
		}
	}
	return cmd.Wait()
}

func (s *Server) handleClamInstall(w http.ResponseWriter, r *http.Request) {
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	s.startClamOp(w, r, "install", func(ctx context.Context) error {
		s.clam.setProgress(msgs.Tc(ctx, "clamav.installing"))
		if err := s.streamScript(ctx, clamav.InstallScript, nil); err != nil {
			return msgs.Errorf("clamav.installFailed", err)
		}
		return nil
	})
}

func (s *Server) handleClamUpdateDB(w http.ResponseWriter, r *http.Request) {
	s.startClamOp(w, r, "update", func(ctx context.Context) error {
		s.clam.setProgress(msgs.Tc(ctx, "clamav.updatingDB"))
		if err := s.streamScript(ctx, clamav.UpdateScript, nil); err != nil {
			return msgs.Errorf("clamav.updateFailed", err)
		}
		return nil
	})
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
	s.startClamOp(w, r, "scan", func(ctx context.Context) error {
		result := &model.ClamScan{Kind: "host", Targets: paths, StartedAt: time.Now(), Hits: []model.ClamHit{}}
		var existing []string
		for _, p := range paths {
			if s.scanner.Collector().Exists(p) {
				existing = append(existing, p)
			} else {
				result.Warnings = append(result.Warnings, msgs.Tc(ctx, "clamav.pathMissing", p))
			}
		}
		if len(existing) == 0 {
			return msgs.Errorf("clamav.nothingToScan")
		}
		s.clam.setProgress(msgs.Tc(ctx, "clamav.scanning", strings.Join(existing, ", ")))
		args := clamav.ScanArgs(existing)
		err := s.streamScript(ctx, shellJoin(args), func(line string) {
			if hit, counter, n := clamav.ParseLine(line); hit != nil {
				result.Hits = append(result.Hits, *hit)
				s.clam.setProgress(msgs.Tc(ctx, "clamav.scanningFound", len(result.Hits)))
			} else if counter == "Scanned files" {
				result.Scanned = n
			}
		})
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
		return nil
	})
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
		if strings.ContainsAny(ref, " \n\t\x00") || ref == "" {
			writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "clamav.badImage", ref))
			return
		}
	}
	tool := "docker"
	if !collect.Which(r.Context(), s.scanner.Collector(), "docker") && collect.Which(r.Context(), s.scanner.Collector(), "podman") {
		tool = "podman"
	}
	s.startClamOp(w, r, "scan-images", func(ctx context.Context) error {
		result := &model.ClamScan{Kind: "images", Targets: images, StartedAt: time.Now(), Hits: []model.ClamHit{}}
		for i, ref := range images {
			s.clam.setProgress(msgs.Tc(ctx, "clamav.scanningImage", i+1, len(images), ref))
			s.clam.append("== " + ref)
			err := s.streamScript(ctx, clamav.ImageScanScript(tool, ref), func(line string) {
				if hit, counter, n := clamav.ParseLine(line); hit != nil {
					hit.Target = ref
					result.Hits = append(result.Hits, *hit)
				} else if counter == "Scanned files" {
					result.Scanned += n
				}
			})
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
		return nil
	})
}

// handleClamCancel прерывает текущую операцию.
func (s *Server) handleClamCancel(w http.ResponseWriter, r *http.Request) {
	s.clam.mu.Lock()
	cancel := s.clam.cancel
	s.clam.mu.Unlock()
	if cancel != nil {
		cancel()
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
