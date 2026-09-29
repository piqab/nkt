package hub

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Сухой прогон выкладки compose-стека: репозиторий, подстановки, файлы
// стека — как у выкладки; на каждом хосте — POST /api/compose/stacks/check
// (движок, compose config на копии стека, что изменится, есть ли образы).
// Ни записи выкладки, ни изменений на хостах.

// composeCheck — ответ хоста на сухой прогон.
type composeCheck struct {
	composeEngine
	StackExists bool `json:"stack_exists"`
	Files       []struct {
		Path  string `json:"path"`
		State string `json:"state"`
	} `json:"files"`
	Env         string   `json:"env"`
	ConfigOK    bool     `json:"config_ok"`
	ConfigError string   `json:"config_error"`
	Services    []string `json:"services"`
	Images      []struct {
		Image  string `json:"image"`
		State  string `json:"state"`
		Detail string `json:"detail"`
	} `json:"images"`
	Simulated bool `json:"simulated"`
}

// dryRun — задание сухого прогона.
func (r *DeployRunner) dryRun(ctx context.Context, jc *jobs.Context, p DeployParams) error {
	s := r.s
	spec, err := deploy.ParseSpec(p.Content)
	if err != nil {
		return err
	}
	if spec.Action != deploy.ActionCompose {
		return msgs.Errorf("deploy.dryOnlyCompose")
	}
	pl := store.Pipeline{Author: jc.Job.Author}
	dir := filepath.Join(s.hub.cfg.DataDir, "deploy", "dryrun")
	if p.PipelineID > 0 {
		if pl, err = s.db.PipelineByID(ctx, p.PipelineID); err != nil {
			return err
		}
		dir = s.pipelineDir(pl.ID)
	}
	g := deploy.Git{Dir: dir, Cred: s.pipelineCred(pl)}
	ref := p.Ref
	if ref == "" {
		ref = spec.Ref
	}
	ref, commit, err := resolveRef(ctx, g, spec.Repo, ref, p.Tag)
	if err != nil {
		return err
	}
	vars := deploy.Vars{Tag: p.Tag, Commit: commit, Ref: ref}
	if vars.Tag == "" {
		vars.Tag = deploy.ShortSHA(commit)
	}
	jc.StepKey(1, 3, "deploy.stepCheckout", ref, deploy.ShortSHA(commit))
	src := filepath.Join(dir, "src")
	if err := g.Checkout(ctx, spec.Repo, ref, commit, src); err != nil {
		return err
	}
	return r.checkCompose(ctx, jc, pl, spec, src, vars)
}

// checkCompose — сухой прогон стека на всех его хостах; ошибка — если
// хоть на одном выкладка не прошла бы.
func (r *DeployRunner) checkCompose(ctx context.Context, jc *jobs.Context, pl store.Pipeline, spec deploy.Spec, src string, vars deploy.Vars) error {
	s := r.s
	c := spec.Compose
	lang := jc.Lang()
	files, main, err := collectComposeFiles(src, c, vars)
	if err != nil {
		return err
	}
	env, err := s.pipelineEnv(pl)
	if err != nil {
		return err
	}
	targets, err := s.resolveHosts(ctx, c.Hosts, c.Group)
	if err != nil {
		return err
	}
	user := s.actingUser(ctx, jc.Job.Author, pl.Author)
	jc.StepKey(2, 3, "deploy.stepDryHosts", len(targets))
	jc.Log("deploy.dryFiles", len(files), c.Project, len(targets), vars.Tag)
	problems := 0
	var services []string
	for _, t := range targets {
		var res composeCheck
		body := composeBody(c, main, files, env, "")
		code, err := s.hostCall(ctx, user, t.ID, "POST", "/api/compose/stacks/check", body, &res)
		switch {
		case code == http.StatusNotFound || code == http.StatusMethodNotAllowed:
			jc.Log("deploy.dryOldHost", t.Name)
			continue
		case err != nil:
			problems++
			jc.Log("deploy.dryHostError", t.Name, msgs.Localize(lang, err))
			continue
		}
		problems += logComposeCheck(jc, t.Name, c.Project, res)
		if res.ConfigOK && !res.Simulated {
			services = res.Services
		}
	}
	jc.StepKey(3, 3, "deploy.stepDryResult")
	switch {
	case c.Site.Managed():
		s.dryRunSite(ctx, jc, user, pl, c, targets[0], services)
	case c.Site.CheckDomain() != "":
		d := c.Site.CheckDomain()
		chk := httpsCheck(ctx, d)
		if chk.OK {
			jc.Log("deploy.siteOK", d, chk.Status, chk.CertDaysLeft)
		} else {
			jc.Log("deploy.siteFailed", d, chk.Error)
		}
	}
	if problems > 0 {
		return msgs.Errorf("deploy.dryFailed", problems)
	}
	jc.Log("deploy.dryOK", len(targets))
	return nil
}

// logComposeCheck пишет итог хоста в журнал; возвращает число проблем.
func logComposeCheck(jc *jobs.Context, host, project string, res composeCheck) int {
	if res.Engine == "" {
		jc.Log("deploy.dryNoEngine", host)
		return 1
	}
	if !res.Compose {
		jc.Log("deploy.dryNoCompose", host, res.Version, res.ComposeError)
		return 1
	}
	if res.Simulated {
		jc.Log("deploy.drySimulated", host, res.Engine)
	} else {
		jc.Log("deploy.dryEngine", host, res.Version, res.ComposeVersion)
	}
	counts := map[string]int{}
	var touched []string
	for _, f := range res.Files {
		counts[f.State]++
		if f.State != "same" && len(touched) < 10 {
			touched = append(touched, f.Path)
		}
	}
	if res.StackExists {
		jc.Log("deploy.dryStackExists", project, counts["new"], counts["changed"], counts["same"], strings.Join(touched, ", "))
	} else {
		jc.Log("deploy.dryStackNew", project, len(res.Files))
	}
	if res.Env != "none" && res.Env != "" {
		jc.Log("deploy.dryEnv." + res.Env)
	}
	problems := 0
	if !res.ConfigOK {
		jc.Log("deploy.dryConfigBad", res.ConfigError)
		return 1
	}
	jc.Log("deploy.dryConfigOK", strings.Join(res.Services, ", "))
	for _, img := range res.Images {
		switch img.State {
		case "registry":
			jc.Log("deploy.dryImageRegistry", img.Image)
		case "local":
			if img.Detail != "" {
				jc.Log("deploy.dryImageLocalOnly", img.Image, img.Detail)
			} else {
				jc.Log("deploy.dryImageLocal", img.Image)
			}
		case "missing":
			problems++
			jc.Log("deploy.dryImageMissing", img.Image, img.Detail)
		default:
			jc.Log("deploy.dryImageUnknown", img.Image, img.Detail)
		}
	}
	return problems
}

// handlePipelineDryRun — POST /hub/pipelines/dryrun: сухой прогон
// описания (сохранённого конвейера или текста из редактора).
func (s *Server) handlePipelineDryRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PipelineID int64  `json:"pipeline_id"`
		Content    string `json:"content"`
		Ref        string `json:"ref"`
		Tag        string `json:"tag"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	name := msgs.Tc(ctx, "deploy.dryNewPipeline")
	if req.PipelineID > 0 {
		pl, err := s.db.PipelineByID(ctx, req.PipelineID)
		if err != nil {
			writeErr(w, r, http.StatusNotFound, err)
			return
		}
		name = pl.Name
		if strings.TrimSpace(req.Content) == "" {
			req.Content = pl.Content
		}
	}
	spec, err := deploy.ParseSpec(req.Content)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if spec.Action != deploy.ActionCompose {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("deploy.dryOnlyCompose"))
		return
	}
	if (req.Ref != "" && !deploy.ValidRef(req.Ref)) || (req.Tag != "" && !deploy.ValidRef(req.Tag)) {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "deploy.specBad", "ref", req.Ref+req.Tag))
		return
	}
	queue := "deploy:dryrun"
	if req.PipelineID > 0 {
		// Та же очередь, что у выкладок конвейера: checkout в тот же каталог.
		queue = fmt.Sprintf("deploy:%d", req.PipelineID)
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindDeploy, TitleKey: "deploy.dryTitle", TitleArgs: []any{name},
		Queue: queue, Author: user, Steps: 3,
		Params: DeployParams{DryRun: true, PipelineID: req.PipelineID, Content: req.Content, Ref: req.Ref, Tag: req.Tag},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(ctx, user, "pipeline.dryrun", name, "ok", map[string]any{"job_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// handleDeployGit — GET /hub/deploy/git: есть ли на хабе git (без него
// выкладки и сухой прогон не работают) и можно ли поставить его отсюда
// (apt-get на машине хаба, встроенный API машины хаба).
func (s *Server) handleDeployGit(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"installed": false}
	if p, err := exec.LookPath("git"); err == nil {
		out["installed"] = true
		if v, err := exec.CommandContext(r.Context(), p, "--version").Output(); err == nil {
			out["version"] = strings.TrimSpace(string(v))
		}
	}
	_, aptErr := exec.LookPath("apt-get")
	out["installable"] = aptErr == nil && s.local != nil && s.hub.cfg.Mode != config.ModeFixtures
	writeJSON(w, http.StatusOK, out)
}
