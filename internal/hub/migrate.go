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
	"sync/atomic"
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
	// SwitchFly — move: цели Traffic Fly из этих контейнеров — на новый
	// хост с той же ролью (рабочая остаётся рабочей).
	SwitchFly bool `json:"switch_fly,omitempty"`
	// CleanLeftovers — move: на новом хосте лежат остатки старой копии
	// этого же стека (перенос туда и обратно) — убрать их перед переносом.
	CleanLeftovers bool `json:"clean_leftovers,omitempty"`
}

// migrateFlyTarget — цель Traffic Fly, которую затронул перенос.
type migrateFlyTarget struct {
	Route     string `json:"route"`
	Title     string `json:"title"`
	Ref       string `json:"ref"`
	Port      int    `json:"port"`
	Old       string `json:"old"`
	New       string `json:"new,omitempty"`
	WasActive bool   `json:"was_active"`
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
	Ports    []string `json:"ports"`
	Networks []struct {
		Name   string   `json:"name"`
		Others []string `json:"others"`
	} `json:"networks"`
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
	MissingNets   []string `json:"missing_networks"`
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
	// Paths — каталоги стека на старом хосте (уборка «и данные»,
	// остатки при переносе обратно).
	Paths []string `json:"paths,omitempty"`
	// State — moved (старый остановлен), rolled_back, removed, returned
	// (перенесён обратно — остатки старой копии убраны).
	State    string `json:"state"`
	Reminded bool   `json:"reminded,omitempty"`
	Author   string `json:"author,omitempty"`
	// Fly — цели Traffic Fly, переключённые переносом (для отката и уборки).
	Fly []migrateFlyTarget `json:"fly,omitempty"`
}

const migrationsKey = "migrations"

// migrateHubQueue — очередь заданий переноса на хабе; не та, что у шагов
// на хостах (api.MigrateHostQueue): на машине хаба менеджер заданий общий.
const migrateHubQueue = "hub-migrate"

// migrateStallTimeout — передача файла без единого байта столько —
// обрыв (канал умер, а TCP этого ещё не заметил) и откат.
var migrateStallTimeout = 10 * time.Minute

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
	if len(c.MissingNets) > 0 {
		out = append(out, [2]string{"migrate.netMissing", strings.Join(c.MissingNets, ", ")})
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
	// Перенос обратно: «уже есть» из-за остатков старой копии — не
	// препятствие, если их убрать (галочка в окне).
	var leftovers map[string]any
	if rec := s.migrateLeftovers(ctx, req.From, req.To, p.Project); rec != nil {
		cl, paths, vols := withoutLeftovers(c, rec)
		var clean []string
		for _, pr := range migrateProblems(p, cl) {
			clean = append(clean, msgs.Tc(ctx, pr[0], pr[1]))
		}
		leftovers = map[string]any{"record": rec.ID, "host": rec.FromName, "ts": rec.TS, "containers": rec.Containers, "paths": paths, "volumes": vols, "problems": clean}
	}
	var names []string
	for _, c := range p.Containers {
		names = append(names, c.Name)
	}
	// Внешняя сеть, к которой на старом хосте подключены и другие: после
	// переноса они потеряют связь с этим стеком — предупреждение.
	var warnings []string
	for _, n := range p.Networks {
		if len(n.Others) > 0 {
			warnings = append(warnings, msgs.Tc(ctx, "migrate.netShared", n.Name, strings.Join(n.Others, ", ")))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": p.Raw, "check": c, "problems": problems, "warnings": warnings, "fly": s.migrateFlyAffected(ctx, req.From, names), "leftovers": leftovers})
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
		Queue: migrateHubQueue, Author: user, Steps: 6, Params: p,
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
	err := s.waitHostJobVia(ctx, jc, user, hostID, started.JobID)
	if err != nil && ctx.Err() != nil {
		// Перенос отменён — и шаг на хосте тоже: упаковка сама запустит
		// контейнеры обратно, распаковку откатит задание переноса.
		_, _ = s.hostCallLong(context.WithoutCancel(ctx), user, hostID, "POST", "/api/jobs/"+strconv.FormatInt(started.JobID, 10)+"/cancel", nil, nil)
	}
	return err
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
	for _, n := range plan.Networks {
		if len(n.Others) > 0 {
			jc.Log("migrate.netShared", n.Name, strings.Join(n.Others, ", "))
		}
	}
	var leftover *migrateRecord
	var lpaths, lvols []string
	if p.CleanLeftovers {
		if leftover = s.migrateLeftovers(ctx, p.From, p.To, plan.Project); leftover != nil {
			check, lpaths, lvols = withoutLeftovers(check, leftover)
		}
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
	if leftover != nil {
		// Остатки старой копии на новом хосте — прочь: актуальные данные
		// едут отсюда.
		jc.Log("migrate.leftoversCleaning", plan.Project, toName, leftover.TS[:10])
		body := map[string]any{"containers": leftover.Containers, "volumes": lvols, "paths": lpaths, "project": leftover.Project}
		if err := s.hostJob(ctx, jc, user, p.To, "/api/migrate/remove", body); err != nil {
			return err
		}
		s.updateMigrations(ctx, func(l []migrateRecord) []migrateRecord {
			for i := range l {
				if l[i].ID == leftover.ID {
					l[i].State = "returned"
				}
			}
			return l
		})
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
	var fly []migrateFlyTarget
	if p.SwitchFly {
		jc.StepKey(5, 6, "migrate.stepFly")
		var ferr error
		if fly, ferr = s.migrateFlySwitch(ctx, jc, user, p.From, p.To, names); ferr != nil {
			// Стек уже работает на новом хосте: перенос удался, маршруты
			// — в журнале и вручную на карте Traffic Fly.
			jc.Log("migrate.flyFailed", msgs.Localize(jc.Lang(), ferr))
		}
	}
	jc.StepKey(5, 6, "migrate.stepRecord")
	var vols, paths []string
	for _, v := range plan.Volumes {
		vols = append(vols, v.Name)
	}
	for _, x := range plan.Paths {
		paths = append(paths, x.Path)
	}
	rec := migrateRecord{ID: id, TS: store.Now(), From: p.From, FromName: fromName, To: p.To, ToName: toName, Project: plan.Project, Kind: plan.Kind,
		Containers: names, Volumes: vols, Paths: paths, State: "moved", Author: jc.Job.Author, Fly: fly}
	s.updateMigrations(ctx, func(l []migrateRecord) []migrateRecord { return append(l, rec) })
	jc.StepKey(6, 6, "migrate.stepDone")
	jc.Log("migrate.done", plan.Project, toName, fromName)
	return nil
}

// migrateCopy — файл переноса со старого хоста на новый потоком через хаб.
func (s *Server) migrateCopy(ctx context.Context, jc *jobs.Context, user string, from, to int64, id, name string, size int64) error {
	path := "/api/migrate/files/" + id + "/" + name
	// Сторож: нет данных migrateStallTimeout — обрыв передачи.
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lastByte atomic.Int64
	lastByte.Store(time.Now().UnixNano())
	stalled := atomic.Bool{}
	go func() {
		t := time.NewTicker(migrateStallTimeout / 10)
		defer t.Stop()
		for {
			select {
			case <-cctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, lastByte.Load())) > migrateStallTimeout {
					stalled.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	ctx = cctx
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
		if k > 0 {
			lastByte.Store(time.Now().UnixNano())
		}
		done += int64(k)
		if time.Since(last) >= 5*time.Second && size > 0 {
			last = time.Now()
			jc.Log("migrate.copyProgress", name, done*100/size)
		}
		return k, err
	})
	if err := s.hostUpload(ctx, user, to, path, pr, size); err != nil {
		if stalled.Load() {
			return msgs.Errorf("migrate.transferStalled", name, int(migrateStallTimeout.Minutes()))
		}
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
		s.migrateFlyRevert(ctx, jc, user, rec.Fly)
		state = "rolled_back"
	case "remove":
		jc.StepKey(1, 1, "migrate.stepRemoveOld", rec.Project, rec.FromName)
		body := map[string]any{"containers": rec.Containers, "project": rec.Project}
		if p.Data {
			body["volumes"] = rec.Volumes
			body["paths"] = rec.Paths
		}
		if err := s.hostJob(ctx, jc, user, rec.From, "/api/migrate/remove", body); err != nil {
			return err
		}
		s.migrateFlyRemoveOld(ctx, jc, user, rec.Fly)
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

// --- Traffic Fly при переносе ---

// flyStepsAsLines — журнал применения Traffic Fly строками внутри
// задания переноса (его шаги не сбивают шаги переноса).
type flyStepsAsLines struct{ jc *jobs.Context }

func (f flyStepsAsLines) Log(key string, args ...any)     { f.jc.Log(key, args...) }
func (f flyStepsAsLines) Logf(format string, args ...any) { f.jc.Logf(format, args...) }
func (f flyStepsAsLines) StepKey(_, _ int, key string, args ...any) {
	f.jc.Log(key, args...)
}

// migrateFlyAffected — цели Traffic Fly из этих контейнеров старого хоста.
func (s *Server) migrateFlyAffected(ctx context.Context, from int64, names []string) []migrateFlyTarget {
	out := []migrateFlyTarget{}
	for _, r := range s.flyDoc(ctx).Routes {
		for _, t := range r.Targets {
			if t.HostID == from && t.Kind == "container" && slicesContains(names, t.Ref) {
				out = append(out, migrateFlyTarget{Route: r.ID, Title: flyRouteTitle(r), Ref: t.Ref, Port: t.Port, Old: t.ID, WasActive: !t.Drain})
			}
		}
	}
	return out
}

func slicesContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// flyRouteTitle — маршрут словами для журнала и окна.
func flyRouteTitle(r FlyRoute) string {
	if len(r.Names) > 0 {
		return strings.Join(r.Names, ", ")
	}
	return r.Kind + " :" + strconv.Itoa(r.Port)
}

// migrateFlyOps — операции над целями к текущей версии, новая версия и
// применение (как задание fly.ops, но внутри задания переноса).
func (s *Server) migrateFlyOps(ctx context.Context, jc *jobs.Context, user string, ops []FlyOp, note string) ([]FlyRoute, error) {
	d := s.flyDoc(ctx)
	routes, err := s.flyApplyOps(ctx, d.Routes, ops, s.flyAliveTargets(ctx))
	if err != nil {
		return nil, err
	}
	saved, err := s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: routes}, user, note)
	if err != nil {
		return nil, err
	}
	if err := s.flyApplyRun(ctx, flyStepsAsLines{jc}, user, FlyApplyParams{Revision: saved.Revision}); err != nil {
		return saved.Routes, err
	}
	return saved.Routes, nil
}

// migrateFlyWait — сколько ждать, пока шлюз увидит новую цель живой.
var migrateFlyWait = 90 * time.Second

// migrateFlySwitch — цели на новом хосте с той же ролью: новая — резервом,
// ожидание проверки, у бывшей рабочей — обмен (новая в работу, старая в
// резерв). Не ожила — обмен всё равно: старая уже остановлена.
func (s *Server) migrateFlySwitch(ctx context.Context, jc *jobs.Context, user string, from, to int64, names []string) ([]migrateFlyTarget, error) {
	list := s.migrateFlyAffected(ctx, from, names)
	if len(list) == 0 {
		return nil, nil
	}
	var ops []FlyOp
	for _, t := range list {
		jc.Log("migrate.flyAdd", t.Title, t.Ref)
		ops = append(ops, FlyOp{Op: "add", Route: t.Route, HostID: to, Kind: "container", Ref: t.Ref, Port: t.Port})
	}
	before := map[string]bool{}
	for _, r := range s.flyDoc(ctx).Routes {
		for _, t := range r.Targets {
			before[r.ID+"/"+t.ID] = true
		}
	}
	routes, err := s.migrateFlyOps(ctx, jc, user, ops, msgs.T(jc.Lang(), "migrate.flyNote"))
	if routes == nil {
		return nil, err
	}
	if err != nil {
		jc.Log("migrate.flyFailed", msgs.Localize(jc.Lang(), err))
	}
	for i, t := range list {
		for _, r := range routes {
			if r.ID != t.Route {
				continue
			}
			for _, nt := range r.Targets {
				if nt.HostID == to && nt.Ref == t.Ref && !before[r.ID+"/"+nt.ID] {
					list[i].New = nt.ID
				}
			}
		}
	}
	var swaps []FlyOp
	deadline := time.Now().Add(migrateFlyWait)
	for _, t := range list {
		if !t.WasActive || t.New == "" {
			continue
		}
		for {
			if s.flyAliveTargets(ctx)[t.Route+"/"+t.New] {
				jc.Log("migrate.flyAlive", t.Title, t.Ref)
				break
			}
			if time.Now().After(deadline) {
				jc.Log("migrate.flyNotAlive", t.Title, t.Ref, int(migrateFlyWait.Seconds()))
				break
			}
			if !sleepCtx(ctx, 3*time.Second) {
				return list, ctx.Err()
			}
		}
		// Сначала новую в работу, потом старую в резерв: рабочая цель у
		// маршрута есть всё время.
		swaps = append(swaps, FlyOp{Op: "active", Route: t.Route, Target: t.New}, FlyOp{Op: "reserve", Route: t.Route, Target: t.Old})
	}
	if len(swaps) > 0 {
		if _, err := s.migrateFlyOps(ctx, jc, user, swaps, msgs.T(jc.Lang(), "migrate.flyNote")); err != nil {
			return list, err
		}
		jc.Log("migrate.flySwitched", len(swaps)/2)
	}
	return list, nil
}

// migrateFlyRevert — откат: у бывших рабочих старая снова в работу,
// новая — в резерв.
func (s *Server) migrateFlyRevert(ctx context.Context, jc *jobs.Context, user string, list []migrateFlyTarget) {
	var ops []FlyOp
	for _, t := range list {
		if t.WasActive && t.New != "" {
			ops = append(ops, FlyOp{Op: "active", Route: t.Route, Target: t.Old}, FlyOp{Op: "reserve", Route: t.Route, Target: t.New})
		}
	}
	if len(ops) == 0 {
		return
	}
	if _, err := s.migrateFlyOps(ctx, jc, user, ops, msgs.T(jc.Lang(), "migrate.flyRevertNote")); err != nil {
		jc.Log("migrate.flyFailed", msgs.Localize(jc.Lang(), err))
		return
	}
	jc.Log("migrate.flyReverted", len(ops)/2)
}

// migrateFlyRemoveOld — уборка старого: старые цели (они уже резервные)
// — из маршрутов.
func (s *Server) migrateFlyRemoveOld(ctx context.Context, jc *jobs.Context, user string, list []migrateFlyTarget) {
	var ops []FlyOp
	for _, t := range list {
		if t.New != "" {
			ops = append(ops, FlyOp{Op: "remove", Route: t.Route, Target: t.Old})
		}
	}
	if len(ops) == 0 {
		return
	}
	if _, err := s.migrateFlyOps(ctx, jc, user, ops, msgs.T(jc.Lang(), "migrate.flyRemoveNote")); err != nil {
		jc.Log("migrate.flyFailed", msgs.Localize(jc.Lang(), err))
		return
	}
	jc.Log("migrate.flyRemoved", len(ops))
}

// migrateLeftovers — запись переноса, чья старая копия лежит на хосте to:
// тот же стек переносили с to на from (и он там «перенесён» или убран без
// данных). Тогда «уже есть» на to — это её остатки.
func (s *Server) migrateLeftovers(ctx context.Context, from, to int64, project string) *migrateRecord {
	list := s.loadMigrations(ctx)
	for i := len(list) - 1; i >= 0; i-- {
		r := list[i]
		if r.From == to && r.To == from && r.Project == project && (r.State == "moved" || r.State == "removed") {
			return &r
		}
	}
	return nil
}

// withoutLeftovers — препятствия проверки без тех, что объясняются
// остатками старой копии rec; второе — что именно из остатков есть.
func withoutLeftovers(c migrateCheckJSON, rec *migrateRecord) (migrateCheckJSON, []string, []string) {
	if rec == nil {
		return c, nil, nil
	}
	in := func(list []string, v string) bool { return slicesContains(list, v) }
	known := rec.Paths
	if len(known) == 0 {
		// Запись до 1.13.49 каталогов не помнит: каталог стека nkt.
		known = []string{"/srv/compose/" + rec.Project}
	}
	var paths, vols []string
	out := c
	out.ExistingPaths, out.ExistingVols = nil, nil
	for _, p := range c.ExistingPaths {
		if in(known, p) {
			paths = append(paths, p)
		} else {
			out.ExistingPaths = append(out.ExistingPaths, p)
		}
	}
	for _, v := range c.ExistingVols {
		if in(rec.Volumes, v) {
			vols = append(vols, v)
		} else {
			out.ExistingVols = append(out.ExistingVols, v)
		}
	}
	// Стек с тем же именем — это сама старая копия (её контейнеры).
	out.ProjectExists = false
	return out, paths, vols
}
