package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/vmcreate"
	"github.com/piqab/nkt/internal/vmimage"
)

// Загрузка файла с компьютера — заданием хоста с первого байта.
//
// Файл передаёт браузер, и пока он идёт, раньше нигде не было видно, что
// что-то происходит: задание (load в движок) появлялось только после
// передачи. Теперь браузер сначала просит начать загрузку
// (POST /uploads/begin): хост заводит задание и отдаёт одноразовый токен,
// с которым потом приходит сам файл. Задание показывает проценты по
// байтам, дошедшим до хоста, — с любого устройства и из любого раздела, —
// а после передачи делает остальное (load в Docker/Podman, перенос образа
// машины в каталог libvirt). Оборванная передача — ошибка задания с
// процентом, на котором она оборвалась, а не тишина.

// KindUpload — вид задания «загрузка файла с компьютера».
const KindUpload = "upload.transfer"

// Куда идёт файл.
const (
	uploadTargetArchive = "archive"
	uploadTargetVMImage = "vmimage"
)

// uploadIdleTimeout — сколько ждать первого байта или следующей порции:
// браузер, который так долго молчит, передачу уже не продолжит.
const uploadIdleTimeout = 10 * time.Minute

// UploadParams — вход задания. Токена здесь нет: он — разрешение на
// передачу и живёт только в памяти, а параметры видны в списке заданий.
type UploadParams struct {
	Target string `json:"target"`
	Name   string `json:"name"`
	Engine string `json:"engine,omitempty"`
	Size   int64  `json:"size"`
	Load   bool   `json:"load,omitempty"`
}

// uploadSession — одна передача: её видят и обработчик, принимающий файл,
// и задание, которое ждёт его и сообщает ход.
type uploadSession struct {
	params UploadParams

	mu       sync.Mutex
	received int64
	lastByte time.Time
	started  bool
	finished bool
	aborted  bool
	err      error
	path     string // готовый файл (архив) или временный (образ машины)
	done     chan struct{}
}

func (u *uploadSession) progress() (int64, time.Time, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.received, u.lastByte, u.started
}

// add учитывает порцию; false — передачу уже отменили (задание
// отменено или истёк срок ожидания), дальше писать незачем.
func (u *uploadSession) add(n int) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.received += int64(n)
	u.lastByte = time.Now()
	return !u.aborted
}

// finish отмечает конец передачи — успех (path) или ошибку.
func (u *uploadSession) finish(path string, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.finished {
		return
	}
	u.finished, u.path, u.err = true, path, err
	close(u.done)
}

// abort — задание больше не ждёт: обработчик прервёт запись.
func (u *uploadSession) abort() {
	u.mu.Lock()
	u.aborted = true
	u.mu.Unlock()
}

// begin занимает сессию для запроса с файлом: второй запрос с тем же
// токеном — отказ.
func (u *uploadSession) begin() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.started || u.aborted {
		return false
	}
	u.started = true
	u.lastByte = time.Now()
	return true
}

// countingReader считает прочитанное в сессию и обрывает чтение, если
// задание передачу отменило.
type countingReader struct {
	r io.Reader
	u *uploadSession
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 && !c.u.add(n) {
		return n, msgs.Errorf("upload.canceled")
	}
	return n, err
}

func newUploadToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) putUploadSession(token string, u *uploadSession) {
	s.transfersMu.Lock()
	defer s.transfersMu.Unlock()
	if s.transfers == nil {
		s.transfers = map[string]*uploadSession{}
	}
	s.transfers[token] = u
}

func (s *Server) uploadSession(token string) *uploadSession {
	s.transfersMu.Lock()
	defer s.transfersMu.Unlock()
	return s.transfers[token]
}

func (s *Server) dropUploadSession(token string) {
	s.transfersMu.Lock()
	defer s.transfersMu.Unlock()
	delete(s.transfers, token)
	for id, t := range s.transferJobs {
		if t == token {
			delete(s.transferJobs, id)
		}
	}
}

// bindUpload связывает задание с передачей: токен не пишется в параметры
// задания (их видно в списке), поэтому живёт рядом, в памяти.
func (s *Server) bindUpload(jobID int64, token string) {
	s.transfersMu.Lock()
	defer s.transfersMu.Unlock()
	if s.transferJobs == nil {
		s.transferJobs = map[int64]string{}
	}
	s.transferJobs[jobID] = token
}

// uploadTokenFor — токен передачи задания. Задание может начаться раньше,
// чем обработчик успел записать связь, — недолго ждём; после перезапуска
// nkt связи нет вовсе.
func (s *Server) uploadTokenFor(ctx context.Context, jobID int64) string {
	for i := 0; i < 50; i++ {
		s.transfersMu.Lock()
		t := s.transferJobs[jobID]
		s.transfersMu.Unlock()
		if t != "" {
			return t
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(100 * time.Millisecond):
		}
	}
	return ""
}

// validateUpload проверяет, куда и под каким именем пойдёт файл.
func (s *Server) validateUpload(p *UploadParams) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Size < 0 || p.Size > maxUploadBytes {
		return msgs.Errorf("upload.tooLarge")
	}
	switch p.Target {
	case uploadTargetArchive:
		if _, err := s.archivePath(p.Name); err != nil {
			return err
		}
		if !archiveExtRe.MatchString(p.Name) {
			return msgs.Errorf("api.archiveBadName", p.Name)
		}
		p.Engine = archiveKind(p.Name)
		if p.Engine == "lxd" {
			p.Load = false
		}
		return nil
	case uploadTargetVMImage:
		if s.vmimages == nil {
			return msgs.Errorf("api.imageManagementUnavailable")
		}
		if p.Name == "" || strings.ContainsAny(p.Name, `/\`) || strings.Contains(p.Name, "..") {
			return msgs.Errorf("vmcreate.invalidFileName", p.Name)
		}
		p.Load = false
		return nil
	}
	return msgs.Errorf("upload.badTarget", p.Target)
}

// handleUploadBegin — POST /uploads/begin {target, name, size, load}:
// задание и токен, с которым придёт сам файл.
func (s *Server) handleUploadBegin(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var p UploadParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.validateUpload(&p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	token := newUploadToken()
	u := &uploadSession{params: p, done: make(chan struct{})}
	s.putUploadSession(token, u)
	steps := 1
	if p.Load || p.Target == uploadTargetVMImage {
		steps = 2
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindUpload, TitleKey: "upload.jobTitle", TitleArgs: []any{p.Name},
		// Своя очередь на каждую передачу: они не ждут друг друга и не
		// держат общую очередь хоста.
		Queue: "upload:" + token[:12], Author: user, Steps: steps, Params: p,
	})
	if err != nil {
		s.dropUploadSession(token)
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.bindUpload(id, token)
	s.db.Audit(r.Context(), user, "upload.begin", p.Name, "ok", map[string]any{"job_id": id, "target": p.Target, "size": p.Size})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "token": token})
}

// receiveUpload — тело запроса с файлом по токену: в каталог архивов или
// во временный файл образа машины. Ответ — сразу после передачи; дальше
// работает задание.
func (s *Server) receiveUpload(w http.ResponseWriter, r *http.Request, token string) {
	u := s.uploadSession(token)
	if u == nil || !u.begin() {
		writeError(w, http.StatusNotFound, msgs.Tc(r.Context(), "upload.noSession"))
		return
	}
	extendUpload(w)
	body := countingReader{r: http.MaxBytesReader(w, r.Body, maxUploadBytes), u: u}
	var path string
	var err error
	switch u.params.Target {
	case uploadTargetArchive:
		path, err = s.writeArchive(u.params.Name, body)
	case uploadTargetVMImage:
		path, err = s.vmimages.SaveTemp(u.params.Name, body)
	}
	u.finish(path, err)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": u.params.Name, "size": u.received})
}

// writeArchive пишет архив потоком во временный файл и переименовывает
// в конце; недописанный удаляется.
func (s *Server) writeArchive(name string, src io.Reader) (string, error) {
	p, err := s.archivePath(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.archiveDir(), 0o700); err != nil {
		return "", err
	}
	tmp := p + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return p, nil
}

// uploadRunner ждёт передачу, сообщая ход, и делает то, ради чего файл
// загружали.
type uploadRunner struct{ s *Server }

// Resumable — нет: передача живёт в памяти процесса; после перезапуска
// браузер её не продолжит, и задание честно заканчивается ошибкой.
func (u *uploadRunner) Resumable() bool { return false }

func (u *uploadRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p UploadParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	token := u.s.uploadTokenFor(ctx, jc.Job.ID)
	sess := u.s.uploadSession(token)
	if sess == nil {
		return msgs.Errorf("upload.lost")
	}
	defer u.s.dropUploadSession(token)
	pct := func(n int64) int64 {
		if p.Size <= 0 {
			return 0
		}
		v := n * 100 / p.Size
		if v > 100 {
			v = 100
		}
		return v
	}
	// Шаг — проценты передачи (N из 100) с самого начала: «шаг 1 из 1» до
	// первого байта выглядел бы в полосе как уже сделанное.
	jc.StepKey(0, 100, "upload.stepTransferPct", p.Name, 0)
	jc.Log("upload.waiting", p.Name, vmimage.HumanBytes(jc.Lang(), p.Size))
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	created := time.Now()
	var lastLogged int64 = -1
	for {
		select {
		case <-sess.done:
		case <-ctx.Done():
			sess.abort()
			return ctx.Err()
		case <-tick.C:
			n, last, started := sess.progress()
			since := created
			if started {
				since = last
			}
			if time.Since(since) > uploadIdleTimeout {
				sess.abort()
				return msgs.Errorf("upload.stalled", pct(n))
			}
			// Шаг — проценты передачи (N из 100): окно журнала рисует по
			// нему полосу; строка журнала — раз в 10 процентов.
			if started {
				jc.StepKey(int(pct(n)), 100, "upload.stepTransferPct", p.Name, pct(n))
				if q := pct(n) / 10; q != lastLogged {
					lastLogged = q
					jc.Log("upload.progress", vmimage.HumanBytes(jc.Lang(), n), vmimage.HumanBytes(jc.Lang(), p.Size), pct(n))
				}
			}
			continue
		}
		break
	}
	n, _, _ := sess.progress()
	sess.mu.Lock()
	path, err := sess.path, sess.err
	sess.mu.Unlock()
	if err != nil {
		return msgs.Errorf("upload.interrupted", pct(n), err)
	}
	jc.StepKey(100, 100, "upload.stepTransferPct", p.Name, 100)
	jc.Log("upload.received", vmimage.HumanBytes(jc.Lang(), n))

	switch p.Target {
	case uploadTargetArchive:
		jc.Log("archives.saved", path)
		if !p.Load {
			return nil
		}
		engineName := map[string]string{"docker": "Docker", "podman": "Podman"}[p.Engine]
		jc.StepKey(2, 2, "archives.stepLoad", engineName)
		if u.s.cfg == nil || u.s.cfg.IsFixtures() {
			jc.Log("upload.loadSkippedDemo")
			return nil
		}
		argv := []string{p.Engine, "load", "-i", path}
		jc.Logf("$ %s", strings.Join(argv, " "))
		code, err := RunToolingStream(ctx, func(format string, args ...any) { jc.Logf(format, args...) }, argv...)
		if err == nil && code != 0 {
			err = msgs.Errorf("cmdjob.exitCode", code)
		}
		if err != nil {
			return err
		}
		jc.Log("archives.loaded", engineName)
		u.s.rescanLater()
	case uploadTargetVMImage:
		jc.StepKey(2, 2, "upload.stepVMImage", p.Name)
		target, err := vmcreate.PutHostImage(ctx, RunTooling, path, p.Name)
		if err != nil {
			u.s.vmimages.RemoveTemp(path)
			return err
		}
		jc.Log("vmimage.done", target)
	}
	return nil
}

// uploadTokenOf — токен передачи из запроса с файлом.
func uploadTokenOf(r *http.Request) string {
	t := r.URL.Query().Get("upload")
	if len(t) != 48 || strings.Trim(t, "0123456789abcdef") != "" {
		return ""
	}
	return t
}
