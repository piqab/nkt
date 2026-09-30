package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/parse"
	"github.com/piqab/nkt/internal/site"
	"github.com/piqab/nkt/internal/store"
)

// Выкладка compose-стека с хаба (конвейер action: compose): файлы стека
// пишутся сразу, в обработчике — вместе с .env, который в параметры
// задания попасть не должен (там секреты), — с историей версий и
// проверкой docker compose config; подъём (pull, up --wait) идёт
// заданием хоста, хаб ждёт его и пересказывает журнал.

// KindComposeDeploy — задание «поднять стек».
const KindComposeDeploy = "compose.deploy"

// composeDeployMaxBytes — предел всех файлов стека: это описание и
// конфиги, не данные.
const composeDeployMaxBytes = 4 << 20

type composeDeployRequest struct {
	Project string `json:"project"`
	// File — compose-файл стека (путь внутри стека).
	File string `json:"file"`
	// Files — путь внутри стека → содержимое (compose-файл тоже здесь).
	Files map[string]string `json:"files"`
	// Env — содержимое .env стека (секреты; права 0600, в историю не идёт).
	Env *string `json:"env,omitempty"`
	// EnvSHA — sha256 .env, записанного прошлой выкладкой хаба: другой на
	// хосте — значит, его правили вручную (ответ env_edited).
	EnvSHA string `json:"env_sha,omitempty"`
	// SiteService / SitePort — сухой прогон: сайт конвейера смотрит на этот
	// сервис и порт контейнера (есть ли они в стеке и в образе).
	SiteService string `json:"site_service,omitempty"`
	SitePort    int    `json:"site_port,omitempty"`
	// Pull — скачать образы перед подъёмом.
	Pull bool `json:"pull"`
	// WaitTimeout — сколько ждать, пока контейнеры поднимутся и пройдут
	// healthcheck, секунды (0 — 300).
	WaitTimeout int    `json:"wait_timeout"`
	Note        string `json:"note"`
}

// ComposeDeployParams — вход задания (без содержимого файлов).
type ComposeDeployParams struct {
	Project     string `json:"project"`
	File        string `json:"file"`
	Engine      string `json:"engine"`
	Pull        bool   `json:"pull"`
	WaitTimeout int    `json:"wait_timeout"`
}

func composeStackDir(project string) string { return parse.ComposeStacksDir + "/" + project }

// validate — проверки запроса, общие для выкладки и сухого прогона.
func (req *composeDeployRequest) validate() error {
	if !site.ValidName(req.Project) {
		return msgs.Errorf("compose.badProject", req.Project)
	}
	if _, ok := req.Files[req.File]; !ok || !deploy.ValidPath(req.File) {
		return msgs.Errorf("compose.noFile", req.File)
	}
	total := 0
	for rel, content := range req.Files {
		base := path.Base(rel)
		if !deploy.ValidPath(rel) || base == ".env" || base == site.OverrideFile {
			return msgs.Errorf("compose.badPath", rel)
		}
		total += len(content)
	}
	if total > composeDeployMaxBytes || len(req.Files) > 200 {
		return msgs.Errorf("compose.tooLarge")
	}
	if names := deploy.BuildOnlyServices(req.Files[req.File]); len(names) > 0 {
		return msgs.Errorf("compose.buildOnly", strings.Join(names, ", "))
	}
	return nil
}

// composeEngine — docker, а без него podman.
func composeEngine(ctx context.Context, c collect.Collector) string {
	if collect.Which(ctx, c, "docker") {
		return "docker"
	}
	if collect.Which(ctx, c, "podman") {
		return "podman"
	}
	return ""
}

// composeArgs — «compose -p <стек> -f <файл> [-f compose.nkt.yml]».
func composeArgs(c collect.Collector, project, file string) []string {
	return composeArgsIn(c, composeStackDir(project), project, file)
}

// composeArgsIn — то же для стека в каталоге dir (сухой прогон — во
// временном каталоге, под именем настоящего стека).
func composeArgsIn(c collect.Collector, dir, project, file string) []string {
	args := []string{"compose", "-p", project, "--project-directory", dir, "-f", dir + "/" + file}
	if ov := dir + "/" + site.OverrideFile; c.Exists(ov) {
		args = append(args, "-f", ov)
	}
	return args
}

// handleComposeDeploy — POST /compose/stacks/deploy.
func (s *Server) handleComposeDeploy(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var req composeDeployRequest
	if err := decodeJSONLimit(r, &req, composeDeployMaxBytes+1<<20); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	c := s.scanner.Collector()
	if err := req.validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	engine := composeEngine(ctx, c)
	if engine == "" {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("compose.noEngine"))
		return
	}
	user := auth.Username(ctx)
	dir := composeStackDir(req.Project)
	note := strings.TrimSpace(req.Note)

	// Запись: прежнее содержимое — для отката, если compose файл не примет.
	type prev struct {
		data   []byte
		exists bool
	}
	before := map[string]prev{}
	restore := func() {
		for p, b := range before {
			if b.exists {
				_ = c.WriteFile(p, b.data, 0o644)
			} else {
				_ = c.DeleteFile(p)
			}
		}
	}
	write := func(p string, data []byte, mode fs.FileMode) error {
		old, err := c.ReadFile(p)
		before[p] = prev{data: old, exists: err == nil}
		return c.WriteFile(p, data, mode)
	}
	for rel, content := range req.Files {
		if err := write(dir+"/"+rel, []byte(content), 0o644); err != nil {
			restore()
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	envEdited := false
	if req.Env != nil {
		envEdited = envEditedOnHost(c, dir, req.EnvSHA)
		if err := write(dir+"/.env", []byte(*req.Env), 0o600); err != nil {
			restore()
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	res, err := c.RunTimeout(ctx, 2*time.Minute, engine, append(composeArgs(c, req.Project, req.File), "config", "-q")...)
	if err == nil && !res.OK() {
		err = msgs.Errorf("compose.configRejected", strings.TrimSpace(res.Output()))
	}
	if err != nil {
		restore()
		s.db.Audit(ctx, user, "compose.deploy", req.Project, "error", err.Error())
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	// История — у текстовых файлов стека (не .env: там секреты).
	if s.configs != nil {
		for rel, content := range req.Files {
			p := dir + "/" + rel
			var old []byte
			if b := before[p]; b.exists {
				old = b.data
			}
			if string(old) != content {
				_, _ = s.configs.RecordDoc(ctx, p, "docker", user, store.ActionEdit, note, old, []byte(content))
			}
		}
	}
	wait := req.WaitTimeout
	if wait <= 0 || wait > 3600 {
		wait = 300
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindComposeDeploy, TitleKey: "compose.jobTitle", TitleArgs: []any{req.Project},
		Queue: "compose:" + req.Project, Author: user, Steps: 3,
		Params: ComposeDeployParams{Project: req.Project, File: req.File, Engine: engine, Pull: req.Pull, WaitTimeout: wait},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(ctx, user, "compose.deploy", req.Project, "ok", map[string]any{"files": len(req.Files), "job": id})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "engine": engine, "env_edited": envEdited})
}

// envEditedOnHost — .env стека на хосте не тот, что записала прошлая
// выкладка (sha — его sha256 от хаба; пусто — сравнивать не с чем).
func envEditedOnHost(c collect.Collector, dir, sha string) bool {
	if sha == "" {
		return false
	}
	cur, err := c.ReadFile(dir + "/.env")
	if err != nil {
		return false
	}
	sum := sha256.Sum256(cur)
	return hex.EncodeToString(sum[:]) != sha
}

// composeDeployRunner поднимает стек.
type composeDeployRunner struct{ s *Server }

func (d *composeDeployRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ComposeDeployParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	if !site.ValidName(p.Project) || !deploy.ValidPath(p.File) || (p.Engine != "docker" && p.Engine != "podman") {
		return msgs.Errorf("compose.badProject", p.Project)
	}
	c := d.s.scanner.Collector()
	defer d.s.rescanLater()
	run := func(timeout time.Duration, extra ...string) error {
		args := append(composeArgs(c, p.Project, p.File), extra...)
		jc.Logf("$ %s %s", p.Engine, strings.Join(args, " "))
		res, err := c.RunTimeout(ctx, timeout, p.Engine, args...)
		for _, l := range strings.Split(strings.TrimSpace(res.Output()), "\n") {
			if strings.TrimSpace(l) != "" {
				jc.Logf("      %s", l)
			}
		}
		if err != nil {
			return err
		}
		if !res.OK() {
			return msgs.Errorf("compose.commandFailed", strings.Join(extra, " "), res.ExitCode)
		}
		return nil
	}
	wait := time.Duration(p.WaitTimeout) * time.Second
	jc.StepKey(1, 3, "compose.stepPull", p.Project)
	if p.Pull {
		if err := run(20*time.Minute, "pull"); err != nil {
			return err
		}
	}
	jc.StepKey(2, 3, "compose.stepUp", p.Project)
	if p.Engine == "docker" {
		// --wait: ждать, пока контейнеры поднимутся и пройдут healthcheck.
		if err := run(wait+5*time.Minute, "up", "-d", "--remove-orphans", "--wait", "--wait-timeout", strconv.Itoa(p.WaitTimeout)); err != nil {
			return err
		}
	} else {
		if err := run(10*time.Minute, "up", "-d", "--remove-orphans"); err != nil {
			return err
		}
		if err := waitPodmanStack(ctx, jc, c, p.Project, wait); err != nil {
			return err
		}
	}
	jc.StepKey(3, 3, "compose.stepStatus", p.Project)
	_ = run(time.Minute, "ps")
	jc.Log("compose.done", p.Project)
	return nil
}

// waitPodmanStack — у podman compose нет --wait: ждём, пока все
// контейнеры проекта работают (и здоровы, если у них есть healthcheck).
func waitPodmanStack(ctx context.Context, jc *jobs.Context, c collect.Collector, project string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		res, err := c.Run(ctx, "podman", "ps", "-a", "--filter", "label=io.podman.compose.project="+project, "--format", "json")
		if err == nil && res.OK() {
			var list []struct {
				Names  []string `json:"Names"`
				State  string   `json:"State"`
				Status string   `json:"Status"`
			}
			if json.Unmarshal([]byte(res.Stdout), &list) == nil && len(list) > 0 {
				ready := true
				for _, ct := range list {
					if ct.State != "running" || strings.Contains(ct.Status, "unhealthy") || strings.Contains(ct.Status, "starting") {
						ready = false
					}
				}
				if ready {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("compose.waitTimeout", project, int(wait.Seconds()))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
