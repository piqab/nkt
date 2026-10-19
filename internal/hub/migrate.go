package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Перенос контейнера Docker (и его compose-проекта) с хоста на хост:
// задание хаба ведёт шаги на хостах (internal/api/migrate*.go) — план,
// проверка нового хоста, упаковка на старом (остановка, tar, docker save),
// передача файлов потоком через хаб, распаковка и подъём на новом. Ошибка
// после остановки — откат: на новом всё созданное убирается, старый
// запускается. Старый после переноса остаётся остановленным — откатить или
// убрать его можно с карты хостов; через 7 дней — напоминание.

// KindContainerMigrate — задание переноса (и отката, и уборки старого).
const KindContainerMigrate = "container.migrate"

// MigrateParams — вход задания.
type MigrateParams struct {
	Action    string `json:"action"` // move | rollback | remove
	From      int64  `json:"from"`
	To        int64  `json:"to"`
	Container string `json:"container,omitempty"`
	// Record — запись переноса (для rollback и remove).
	Record string `json:"record,omitempty"`
	// Data — remove: и тома старого стека.
	Data bool `json:"data,omitempty"`
}

// migratePlanJSON — план хоста (то, что нужно хабу).
type migratePlanJSON struct {
	Kind       string `json:"kind"`
	Project    string `json:"project"`
	Containers []struct {
		Name    string `json:"name"`
		Service string `json:"service"`
		Image   string `json:"image"`
	} `json:"containers"`
	Images   []string `json:"images"`
	ImageIDs []string `json:"image_ids"`
	Volumes  []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"volumes"`
	Paths []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	} `json:"paths"`
	Ports   []string `json:"ports"`
	Arch    string   `json:"arch"`
	Refuse  []string `json:"refuse"`
	Skipped []string `json:"skipped"`
	Raw     json.RawMessage
}

type migrateCheckJSON struct {
	Docker        bool     `json:"docker"`
	Arch          string   `json:"arch"`
	BusyPorts     []string `json:"busy_ports"`
	ExistingPaths []string `json:"existing_paths"`
	ExistingVols  []string `json:"existing_volumes"`
	HaveImages    []string `json:"have_images"`
	FreeBytes     int64    `json:"free_bytes"`
	ProjectExists bool     `json:"project_exists"`
}

// migrateRecord — перенос в учёте хаба (KV): метка на карте, откат,
// уборка, напоминание.
type migrateRecord struct {
	ID         string   `json:"id"`
	TS         string   `json:"ts"`
	From       int64    `json:"from"`
	FromName   string   `json:"from_name"`
	To         int64    `json:"to"`
	ToName     string   `json:"to_name"`
	Project    string   `json:"project"`
	Kind       string   `json:"kind"`
	Containers []string `json:"containers"`
	Volumes    []string `json:"volumes,omitempty"`
	// State — moved (старый остановлен), rolled_back, removed.
	State    string `json:"state"`
	Reminded bool   `json:"reminded,omitempty"`
	Author   string `json:"author,omitempty"`
}

const migrationsKey = "migrations"

var migrationsMu sync.Mutex

func (s *Server) loadMigrations(ctx context.Context) []migrateRecord {
	raw, ok, err := s.db.KVGet(ctx, migrationsKey)
	if err != nil || !ok || raw == "" {
		return nil
	}
	var out []migrateRecord
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func (s *Server) updateMigrations(ctx context.Context, fn func([]migrateRecord) []migrateRecord) {
	migrationsMu.Lock()
	defer migrationsMu.Unlock()
	list := fn(s.loadMigrations(ctx))
	// Хранятся последние 200.
	if len(list) > 200 {
		list = list[len(list)-200:]
	}
	if raw, err := json.Marshal(list); err == nil {
		_ = s.db.KVSet(ctx, migrationsKey, string(raw))
	}
}

func (s *Server) hostName(ctx context.Context, id int64) string {
	if id == localHostID {
		if e := s.localHostEntry(ctx); e != nil {
			return e.Name
		}
		return "localhost"
	}
	if h, err := s.db.HostByID(ctx, id); err == nil {
		return h.Name
	}
	return "#" + strconv.FormatInt(id, 10)
}

// migrateProblems — что мешает перенести (ключи каталога и аргументы).
func migrateProblems(p migratePlanJSON, c migrateCheckJSON) [][2]string {
	var out [][2]string
	for _, r := range p.Refuse {
		k, arg, _ := strings.Cut(r, ":")
		out = append(out, [2]string{k, arg})
	}
	if !c.Docker {
		out = append(out, [2]string{"migrate.noDocker", ""})
	}
	if p.Arch != "" && c.Arch != "" && p.Arch != c.Arch {
		out = append(out, [2]string{"migrate.archMismatch", p.Arch + " → " + c.Arch})
	}
	if len(c.BusyPorts) > 0 {
		out = append(out, [2]string{"migrate.portsBusy", strings.Join(c.BusyPorts, ", ")})
	}
	if len(c.ExistingPaths) > 0 {
		out = append(out, [2]string{"migrate.pathsExist", strings.Join(c.ExistingPaths, ", ")})
	}
	if len(c.ExistingVols) > 0 {
		out = append(out, [2]string{"migrate.volumesExist", strings.Join(c.ExistingVols, ", ")})
	}
	if c.ProjectExists {
		out = append(out, [2]string{"migrate.projectExists", p.Project})
	}
	var need int64
	for _, v := range p.Volumes {
		need += v.Size
	}
	for _, x := range p.Paths {
		need += x.Size
	}
	// Данные распаковываются рядом с архивом: нужно вдвое и с запасом.
	if c.FreeBytes > 0 && need*2+(1<<30) > c.FreeBytes {
		out = append(out, [2]string{"migrate.noSpace", strconv.FormatInt((need*2+(1<<30))>>20, 10)})
	}
	return out
}

// migrateInspect — план на старом и проверка на новом хосте.
func (s *Server) migrateInspect(ctx context.Context, user string, from, to int64, container string) (migratePlanJSON, migrateCheckJSON, error) {
	var p migratePlanJSON
	var c migrateCheckJSON
	var raw json.RawMessage
	// Хост без переноса (nkt старее) отвечает 404: понятная причина.
	old := func(code int, err error, host int64) error {
		if code == http.StatusNotFound {
			return msgs.Errorf("migrate.hostOld", s.hostName(ctx, host))
		}
		return err
	}
	if code, err := s.hostCallLong(ctx, user, from, "GET", "/api/migrate/plan?container="+container, nil, &raw); err != nil {
		return p, c, old(code, err, from)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, c, err
	}
	p.Raw = raw
	if code, err := s.hostCallLong(ctx, user, to, "POST", "/api/migrate/check", raw, &c); err != nil {
		return p, c, old(code, err, to)
	}
	return p, c, nil
}

// handleMigratePreview — POST /hub/migrate/preview {from, to, container}:
// что переносится и что мешает (окно переноса на карте).
func (s *Server) handleMigratePreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From      int64  `json:"from"`
		To        int64  `json:"to"`
		Container string `json:"container"`
	}
	if err := decodeJSON(r, &req); err != nil || req.From == req.To || req.Container == "" {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "migrate.badRequest"))
		return
	}
	ctx := r.Context()
	p, c, err := s.migrateInspect(ctx, auth.Username(ctx), req.From, req.To, req.Container)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	var problems []string
	for _, pr := range migrateProblems(p, c) {
		problems = append(problems, msgs.Tc(ctx, pr[0], pr[1]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": p.Raw, "check": c, "problems": problems})
}

// handleMigrateStart — POST /hub/migrate {from, to, container}: перенос.
func (s *Server) handleMigrateStart(w http.ResponseWriter, r *http.Request) {
	var req MigrateParams
	if err := decodeJSON(r, &req); err != nil || req.From == req.To || req.Container == "" {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "migrate.badRequest"))
		return
	}
	req.Action = "move"
	s.startMigrate(w, r, req, req.Container)
}

// handleMigrationAction — POST /hub/migrations/{id}/{action} (rollback,
// remove {data}).
func (s *Server) handleMigrationAction(w http.ResponseWriter, r *http.Request) {
	id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	var body struct {
		Data bool `json:"data"`
	}
	_ = decodeJSON(r, &body)
	var rec *migrateRecord
	for _, m := range s.loadMigrations(r.Context()) {
		if m.ID == id {
			mm := m
			rec = &mm
		}
	}
	if rec == nil || rec.State != "moved" || (action != "rollback" && action != "remove") {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "migrate.badRequest"))
		return
	}
	s.startMigrate(w, r, MigrateParams{Action: action, From: rec.From, To: rec.To, Record: id, Data: body.Data}, rec.Project)
}

// handleMigrations — GET /hub/migrations: учёт переносов (для карты).
func (s *Server) handleMigrations(w http.ResponseWriter, r *http.Request) {
	list := s.loadMigrations(r.Context())
	if list == nil {
		list = []migrateRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"migrations": list})
}

func (s *Server) startMigrate(w http.ResponseWriter, r *http.Request, p MigrateParams, title string) {
	ctx := r.Context()
	user := auth.Username(ctx)
	if allow := s.scopeFilter(ctx); allow != nil && (!allow(p.From) || !allow(p.To)) {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("auth.tokenHostDenied"))
		return
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindContainerMigrate, TitleKey: "migrate.title." + p.Action, TitleArgs: []any{title, s.hostName(ctx, p.From), s.hostName(ctx, p.To)},
		Queue: "migrate", Author: user, Steps: 6, Params: p,
	})
	s.db.Audit(ctx, user, "migrate."+p.Action, title, auditOutcome(err), map[string]any{"from": p.From, "to": p.To})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// MigrateRunner — исполнитель переноса.
type MigrateRunner struct{ s *Server }

// NewMigrateRunner строит исполнителя.
func NewMigrateRunner(s *Server) *MigrateRunner { return &MigrateRunner{s: s} }

// Run — по действию задания.
func (r *MigrateRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p MigrateParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	s := r.s
	user := s.actingUser(ctx, jc.Job.Author, s.firstAdmin(ctx))
	switch p.Action {
	case "move":
		return s.migrateMove(ctx, jc, user, p)
	case "rollback", "remove":
		return s.migrateAfter(ctx, jc, user, p)
	}
	return msgs.Errorf("migrate.badRequest")
}

func newMigrateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// hostJob — шаг-задание на хосте и ожидание его конца.
func (s *Server) hostJob(ctx context.Context, jc *jobs.Context, user string, hostID int64, path string, body any) error {
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := s.hostCallLong(ctx, user, hostID, "POST", path, body, &started); err != nil {
		return err
	}
	return s.waitHostJobVia(ctx, jc, user, hostID, started.JobID)
}

func (s *Server) migrateMove(ctx context.Context, jc *jobs.Context, user string, p MigrateParams) (err error) {
	fromName, toName := s.hostName(ctx, p.From), s.hostName(ctx, p.To)
	jc.StepKey(1, 6, "migrate.stepPlan", p.Container, fromName, toName)
	plan, check, err := s.migrateInspect(ctx, user, p.From, p.To, p.Container)
	if err != nil {
		return err
	}
	var names []string
	for _, c := range plan.Containers {
		names = append(names, c.Name)
	}
	jc.Log("migrate.planLine", plan.Project, strings.Join(names, ", "), len(plan.Volumes), len(plan.Paths))
	for _, sk := range plan.Skipped {
		jc.Log("migrate.skippedPath", sk)
	}
	if probs := migrateProblems(plan, check); len(probs) > 0 {
		for _, pr := range probs {
			jc.Log(pr[0], pr[1])
		}
		return msgs.Errorf("migrate.cannot", len(probs))
	}
	have := map[string]bool{}
	for _, img := range check.HaveImages {
		have[img] = true
	}
	var save []string
	for _, img := range plan.Images {
		if !have[img] {
			save = append(save, img)
		}
	}
	id := newMigrateID()
	jc.StepKey(2, 6, "migrate.hubPack", plan.Project, fromName)
	if err := s.hostJob(ctx, jc, user, p.From, "/api/migrate/pack", map[string]any{"id": id, "container": p.Container, "save_images": save}); err != nil {
		return err
	}
	// Старый остановлен: дальше любая ошибка — откат.
	defer func() {
		cleanup := context.WithoutCancel(ctx)
		if err != nil {
			jc.Log("migrate.rollingBack")
			if uerr := s.hostJob(cleanup, jc, user, p.To, "/api/migrate/undo", map[string]any{"id": id}); uerr != nil {
				jc.Log("migrate.undoFailed", msgs.Localize(jc.Lang(), uerr))
			}
			if _, serr := s.hostCallLong(cleanup, user, p.From, "POST", "/api/migrate/start", map[string]any{"containers": names}, nil); serr != nil {
				jc.Log("migrate.restartFailed", msgs.Localize(jc.Lang(), serr))
			} else {
				jc.Log("migrate.restarted", fromName)
			}
		}
		_, _ = s.hostCallLong(cleanup, user, p.From, "DELETE", "/api/migrate/staging/"+id, nil, nil)
		_, _ = s.hostCallLong(cleanup, user, p.To, "DELETE", "/api/migrate/staging/"+id, nil, nil)
	}()
	jc.StepKey(3, 6, "migrate.stepTransfer", fromName, toName)
	var manifest struct {
		Files []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	var manRaw json.RawMessage
	if _, err := s.hostCallLong(ctx, user, p.From, "GET", "/api/migrate/files/"+id+"/manifest.json", nil, &manRaw); err != nil {
		return err
	}
	if err := json.Unmarshal(manRaw, &manifest); err != nil {
		return err
	}
	for _, f := range manifest.Files {
		if err := s.migrateCopy(ctx, jc, user, p.From, p.To, id, f.Name, f.Size); err != nil {
			return err
		}
	}
	if err := s.migrateCopy(ctx, jc, user, p.From, p.To, id, "manifest.json", int64(len(manRaw))); err != nil {
		return err
	}
	jc.StepKey(4, 6, "migrate.hubUnpack", plan.Project, toName)
	if err := s.hostJob(ctx, jc, user, p.To, "/api/migrate/unpack", map[string]any{"id": id}); err != nil {
		return err
	}
	jc.StepKey(5, 6, "migrate.stepRecord")
	var vols []string
	for _, v := range plan.Volumes {
		vols = append(vols, v.Name)
	}
	rec := migrateRecord{ID: id, TS: store.Now(), From: p.From, FromName: fromName, To: p.To, ToName: toName, Project: plan.Project, Kind: plan.Kind,
		Containers: names, Volumes: vols, State: "moved", Author: jc.Job.Author}
	s.updateMigrations(ctx, func(l []migrateRecord) []migrateRecord { return append(l, rec) })
	jc.StepKey(6, 6, "migrate.stepDone")
	jc.Log("migrate.done", plan.Project, toName, fromName)
	return nil
}

// migrateCopy — файл переноса со старого хоста на новый потоком через хаб.
func (s *Server) migrateCopy(ctx context.Context, jc *jobs.Context, user string, from, to int64, id, name string, size int64) error {
	path := "/api/migrate/files/" + id + "/" + name
	src, n, err := s.hostDownload(ctx, user, from, path)
	if err != nil {
		return err
	}
	defer src.Close()
	if n > 0 {
		size = n
	}
	jc.Log("migrate.copying", name, float64(size)/(1<<20))
	var done int64
	last := time.Now()
	pr := readFunc(func(b []byte) (int, error) {
		k, err := src.Read(b)
		done += int64(k)
		if time.Since(last) >= 5*time.Second && size > 0 {
			last = time.Now()
			jc.Log("migrate.copyProgress", name, done*100/size)
		}
		return k, err
	})
	if err := s.hostUpload(ctx, user, to, path, pr, size); err != nil {
		return err
	}
	return nil
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(b []byte) (int, error) { return f(b) }

// hostDownload — GET с телом потоком (машина хаба — через свой API).
func (s *Server) hostDownload(ctx context.Context, user string, hostID int64, path string) (io.ReadCloser, int64, error) {
	if hostID != localHostID {
		return s.hub.HostAPIDownload(ctx, hostID, path)
	}
	req, done, err := s.localRequest(ctx, user, http.MethodGet, path, nil)
	if err != nil {
		return nil, 0, err
	}
	pr, pw := io.Pipe()
	rw := &pipeResponse{w: pw, header: http.Header{}}
	go func() {
		defer done()
		s.local.ServeHTTP(rw, req)
		if rw.code != 0 && rw.code != http.StatusOK {
			_ = pw.CloseWithError(msgs.Errorf("control.code", http.MethodGet, path, rw.code, hostAPIError(rw.errBody)))
			return
		}
		_ = pw.Close()
	}()
	return pr, 0, nil
}

// hostUpload — PUT с телом потоком.
func (s *Server) hostUpload(ctx context.Context, user string, hostID int64, path string, body io.Reader, size int64) error {
	if hostID != localHostID {
		code, raw, err := s.hub.HostAPIStream(ctx, hostID, http.MethodPut, path, body, size)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return msgs.Errorf("control.code", http.MethodPut, path, code, hostAPIError(raw))
		}
		return nil
	}
	req, done, err := s.localRequest(ctx, user, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	defer done()
	req.ContentLength = size
	rec := httptest.NewRecorder()
	s.local.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return msgs.Errorf("control.code", http.MethodPut, path, rec.Code, hostAPIError(rec.Body.Bytes()))
	}
	return nil
}

// localRequest — запрос к встроенному API машины хаба от имени
// действующего администратора (сессия на время запроса).
func (s *Server) localRequest(ctx context.Context, username, method, path string, body io.Reader) (*http.Request, func(), error) {
	if s.local == nil {
		return nil, nil, msgs.Errorf("hub.f2bNoLocal")
	}
	user, err := s.db.UserByName(ctx, s.actingUser(ctx, username, s.firstAdmin(ctx)))
	if err != nil {
		return nil, nil, err
	}
	token, err := auth.NewToken()
	if err != nil {
		return nil, nil, err
	}
	if err := s.db.CreateSession(ctx, token, user.ID, time.Now().Add(6*time.Hour), "nkt-hub-job"); err != nil {
		return nil, nil, err
	}
	lctx := context.WithValue(ctx, chi.RouteCtxKey, (*chi.Context)(nil))
	req := httptest.NewRequestWithContext(lctx, method, path, body)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	req.RemoteAddr = "127.0.0.1:0"
	return req, func() { _ = s.db.DeleteSession(context.Background(), token) }, nil
}

// pipeResponse — ResponseWriter, пишущий тело в трубу (200) или копящий
// причину отказа.
type pipeResponse struct {
	w       *io.PipeWriter
	header  http.Header
	code    int
	errBody []byte
}

func (p *pipeResponse) Header() http.Header { return p.header }
func (p *pipeResponse) WriteHeader(code int) {
	if p.code == 0 {
		p.code = code
	}
}
func (p *pipeResponse) Write(b []byte) (int, error) {
	if p.code == 0 {
		p.code = http.StatusOK
	}
	if p.code != http.StatusOK {
		if len(p.errBody) < 64<<10 {
			p.errBody = append(p.errBody, b...)
		}
		return len(b), nil
	}
	return p.w.Write(b)
}

// migrateAfter — откат (новый остановить, старый запустить) или уборка
// старого стека.
func (s *Server) migrateAfter(ctx context.Context, jc *jobs.Context, user string, p MigrateParams) error {
	var rec *migrateRecord
	for _, m := range s.loadMigrations(ctx) {
		if m.ID == p.Record {
			mm := m
			rec = &mm
		}
	}
	if rec == nil || rec.State != "moved" {
		return msgs.Errorf("migrate.badRequest")
	}
	state := ""
	switch p.Action {
	case "rollback":
		jc.StepKey(1, 2, "migrate.stepRollbackStop", rec.Project, rec.ToName)
		if _, err := s.hostCallLong(ctx, user, rec.To, "POST", "/api/migrate/stop", map[string]any{"containers": rec.Containers}, nil); err != nil {
			return err
		}
		jc.StepKey(2, 2, "migrate.stepRollbackStart", rec.Project, rec.FromName)
		if _, err := s.hostCallLong(ctx, user, rec.From, "POST", "/api/migrate/start", map[string]any{"containers": rec.Containers}, nil); err != nil {
			return err
		}
		jc.Log("migrate.rolledBack", rec.Project, rec.FromName, rec.ToName)
		state = "rolled_back"
	case "remove":
		jc.StepKey(1, 1, "migrate.stepRemoveOld", rec.Project, rec.FromName)
		body := map[string]any{"containers": rec.Containers}
		if p.Data {
			body["volumes"] = rec.Volumes
		}
		if err := s.hostJob(ctx, jc, user, rec.From, "/api/migrate/remove", body); err != nil {
			return err
		}
		state = "removed"
	}
	s.updateMigrations(ctx, func(l []migrateRecord) []migrateRecord {
		for i := range l {
			if l[i].ID == rec.ID {
				l[i].State = state
			}
		}
		return l
	})
	return nil
}

// migrateRemindAfter — через сколько напомнить об остановленном старом.
const migrateRemindAfter = 7 * 24 * time.Hour

// StartMigrationReminders — раз в час: старый стек лежит остановленным
// дольше недели — событие в «Оповещениях» (один раз).
func (s *Server) StartMigrationReminders(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			s.remindMigrations(ctx, time.Now())
		}
	}()
}

func (s *Server) remindMigrations(ctx context.Context, now time.Time) {
	var due []migrateRecord
	s.updateMigrations(ctx, func(l []migrateRecord) []migrateRecord {
		for i := range l {
			ts, err := time.Parse(time.RFC3339, l[i].TS)
			if err != nil || l[i].State != "moved" || l[i].Reminded || now.Sub(ts) < migrateRemindAfter {
				continue
			}
			l[i].Reminded = true
			due = append(due, l[i])
		}
		return l
	})
	for _, m := range due {
		_, _ = s.db.AddHostEvent(ctx, store.HostEvent{HostID: m.From, HostName: m.FromName, Kind: store.EventMigrationLeftover, Severity: "info",
			DetailKey: "migrate.reminder", DetailArgs: msgs.EncodeArgs([]any{m.Project, m.ToName, m.TS[:10]})})
	}
}
