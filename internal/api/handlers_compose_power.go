package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/site"
)

// «Остановить» и «Запустить» выкладку (хаб, конвейер): стек на хосте
// останавливается без удаления (compose stop — контейнеры, тома и .env
// остаются) и поднимается той же версией, что выложена: файлы не
// пишутся, недостающие образы скачиваются с ключом конвейера, up — с
// --pull never.

// KindComposeStop — задание «остановить стек».
const KindComposeStop = "compose.stop"

// ComposeStopParams — вход задания остановки.
type ComposeStopParams struct {
	Project string `json:"project"`
	File    string `json:"file"`
	Engine  string `json:"engine"`
}

type composePowerRequest struct {
	Project string `json:"project"`
	// Action — start или stop.
	Action string `json:"action"`
	// File — основной compose-файл стека (относительно каталога стека);
	// пусто — найти в каталоге.
	File          string                `json:"file,omitempty"`
	RegistryAuths []composeRegistryAuth `json:"registry_auths,omitempty"`
	WaitTimeout   int                   `json:"wait_timeout,omitempty"`
}

// handleComposePower — POST /compose/stacks/power {project, action, file,
// registry_auths}: задание хоста; в ответе — контейнеры стека (хаб
// помечает ими цели Traffic Fly остановленной выкладки).
func (s *Server) handleComposePower(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var req composePowerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !site.ValidName(req.Project) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("compose.badProject", req.Project))
		return
	}
	if req.Action != "start" && req.Action != "stop" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("compose.badPowerAction", req.Action))
		return
	}
	ctx := r.Context()
	c := s.scanner.Collector()
	dir := composeStackDir(req.Project)
	file := req.File
	if file == "" || !deploy.ValidPath(file) || !c.Exists(dir+"/"+file) {
		file = composeFileIn(c, dir)
	}
	if file == "" {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("compose.powerNoStack", req.Project, dir))
		return
	}
	engine := composeEngine(ctx, c)
	if engine == "" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("compose.noEngine"))
		return
	}
	var containers []string
	if res, err := c.RunTimeout(ctx, time.Minute, engine, append(composeArgs(c, req.Project, file), "ps", "-a", "--format", "{{.Name}}")...); err == nil && res.OK() {
		for _, l := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				containers = append(containers, l)
			}
		}
	}
	user := auth.Username(ctx)
	var spec jobs.Spec
	if req.Action == "stop" {
		spec = jobs.Spec{Kind: KindComposeStop, TitleKey: "compose.stopTitle", TitleArgs: []any{req.Project},
			Queue: "compose:" + req.Project, Author: user, Steps: 1,
			Params: ComposeStopParams{Project: req.Project, File: file, Engine: engine}}
	} else {
		wait := req.WaitTimeout
		if wait <= 0 || wait > 3600 {
			wait = 300
		}
		params := ComposeDeployParams{Project: req.Project, File: file, Engine: engine, WaitTimeout: wait, StartOnly: true}
		if len(req.RegistryAuths) > 0 {
			authDir, caHosts, err := s.writePullAuth(req.RegistryAuths)
			if err != nil {
				writeErr(w, r, http.StatusBadRequest, err)
				return
			}
			params.AuthDir, params.CAHosts = authDir, caHosts
		}
		spec = jobs.Spec{Kind: KindComposeDeploy, TitleKey: "compose.startTitle", TitleArgs: []any{req.Project},
			Queue: "compose:" + req.Project, Author: user, Steps: 3, Params: params}
	}
	id, err := s.jobs.Start(ctx, spec)
	if err != nil {
		if p, ok := spec.Params.(ComposeDeployParams); ok && p.AuthDir != "" {
			_ = os.RemoveAll(p.AuthDir)
		}
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(ctx, user, "compose."+req.Action, req.Project, "ok", map[string]any{"job": id})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "containers": containers})
}

// composeStopRunner — compose stop: контейнеры остаются, их можно
// поднять «Запустить».
type composeStopRunner struct{ s *Server }

func (d *composeStopRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ComposeStopParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	if !site.ValidName(p.Project) || !deploy.ValidPath(p.File) || (p.Engine != "docker" && p.Engine != "podman") {
		return msgs.Errorf("compose.badProject", p.Project)
	}
	c := d.s.scanner.Collector()
	defer d.s.rescanLater()
	jc.StepKey(1, 1, "compose.stepStop", p.Project)
	args := append(composeArgs(c, p.Project, p.File), "stop")
	jc.Logf("$ %s %s", p.Engine, strings.Join(args, " "))
	res, err := c.RunTimeout(ctx, 10*time.Minute, p.Engine, args...)
	for _, l := range strings.Split(strings.TrimSpace(res.Output()), "\n") {
		if strings.TrimSpace(l) != "" {
			jc.Logf("      %s", l)
		}
	}
	if err != nil {
		return err
	}
	if !res.OK() {
		return msgs.Errorf("compose.commandFailed", "stop", res.ExitCode)
	}
	return nil
}
