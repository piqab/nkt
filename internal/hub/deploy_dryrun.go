package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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
	EnvEdited   bool     `json:"env_edited"`
	ConfigOK    bool     `json:"config_ok"`
	ConfigError string   `json:"config_error"`
	Services    []string `json:"services"`
	Images      []struct {
		Image        string   `json:"image"`
		State        string   `json:"state"`
		Detail       string   `json:"detail"`
		Arches       []string `json:"arches"`
		ArchMismatch bool     `json:"arch_mismatch"`
	} `json:"images"`
	PortsBusy []struct {
		Addr   string `json:"addr"`
		Holder string `json:"holder"`
	} `json:"ports_busy"`
	HostArch       string   `json:"host_arch"`
	UnsetVars      []string `json:"unset_vars"`
	NoHealthcheck  []string `json:"no_healthcheck"`
	MemAvailableMB int      `json:"mem_available_mb"`
	DiskFreeMB     int      `json:"disk_free_mb"`
	Simulated      bool     `json:"simulated"`
	SiteChecked    bool     `json:"site_checked"`
	SiteFound      bool     `json:"site_found"`
	SitePorts      []int    `json:"site_ports"`
	SiteImage      string   `json:"site_image"`
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
	return r.checkCompose(ctx, jc, pl, spec, src, vars, dryChecksOn(p.Skip))
}

// checkCompose — сухой прогон стека на всех его хостах; ошибка — если
// хоть на одном выкладка не прошла бы.
func (r *DeployRunner) checkCompose(ctx context.Context, jc *jobs.Context, pl store.Pipeline, spec deploy.Spec, src string, vars deploy.Vars, on dryOn) error {
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
	if err := bindComposePorts(jc, files, main, c); err != nil {
		return err
	}
	problems := 0
	sitePort := 0
	if skipped := on.skippedNames(lang); skipped != "" {
		jc.Log("deploy.drySkipped", skipped)
	}
	if others := s.pipelinesWithProject(ctx, pl.ID, c.Project); len(others) > 0 && on("stack") {
		jc.Log("deploy.dryProjectShared", c.Project, strings.Join(others, ", "))
	}
	var missingEnv []string
	if on("config") {
		missingEnv = missingEnvKeys(c.AllEnvKeys(), env)
		if len(missingEnv) > 0 {
			problems++
			jc.Log("deploy.dryEnvKeysMissing", strings.Join(missingEnv, ", "))
		} else if keys := c.AllEnvKeys(); len(keys) > 0 {
			jc.Log("deploy.dryEnvKeysOK", strings.Join(keys, ", "))
		}
	}
	for _, t := range targets {
		var res composeCheck
		body := composeBody(c, main, files, env, pl.EnvSHA, "")
		if c.Site.Managed() && on("site_host") {
			body["site_service"], body["site_port"] = c.Site.Service, c.Site.Port
		}
		if skip := on.hostSkip(); len(skip) > 0 {
			body["skip"] = skip
		}
		code, err := s.composeHostPost(ctx, jc, user, t, "/api/compose/stacks/check", body, &res)
		switch {
		case code == http.StatusNotFound || code == http.StatusMethodNotAllowed:
			jc.Log("deploy.dryOldHost", t.Name)
			continue
		case err != nil:
			problems++
			jc.Log("deploy.dryHostError", t.Name, msgs.Localize(lang, err))
			continue
		}
		problems += logComposeCheck(jc, t.Name, c.Project, res, missingEnv, on)
		if res.StackExists && pl.LastCommit == "" && on("stack") {
			// Стек с таким именем на хосте есть, а этот конвейер его не
			// выкладывал: файлы перезапишутся.
			jc.Log("deploy.dryStackForeign", c.Project, t.Name)
		}
		if on("version") {
			problems += s.dryHostVersion(ctx, jc, t)
		}
		if c.Site.Managed() && res.SiteChecked && on("site_host") {
			n, port := logSitePort(jc, c, res)
			problems += n
			sitePort = port
		}
	}
	jc.StepKey(3, 3, "deploy.stepDryResult")
	switch {
	case c.Site.Managed():
		problems += s.dryRunSite(ctx, jc, user, pl, c, targets[0], sitePort, on)
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
func logComposeCheck(jc *jobs.Context, host, project string, res composeCheck, envKeysMissing []string, on dryOn) int {
	if res.Engine == "" || !res.Compose {
		if !on("engine") {
			return 0 // без движка остальное и не проверить
		}
		if res.Engine == "" {
			jc.Log("deploy.dryNoEngine", host)
		} else {
			jc.Log("deploy.dryNoCompose", host, res.Version, res.ComposeError)
		}
		return 1
	}
	if res.Simulated {
		jc.Log("deploy.drySimulated", host, res.Engine)
	} else {
		jc.Log("deploy.dryEngine", host, res.Version, res.ComposeVersion)
	}
	problems := 0
	if res.DaemonDown && on("engine") {
		problems++
		jc.Log("deploy.dryDaemonDown", res.Engine, res.DaemonError)
	}
	counts := map[string]int{}
	var touched []string
	for _, f := range res.Files {
		counts[f.State]++
		if f.State != "same" && len(touched) < 10 {
			touched = append(touched, f.Path)
		}
	}
	if on("stack") {
		if res.StackExists {
			jc.Log("deploy.dryStackExists", project, counts["new"], counts["changed"], counts["same"], strings.Join(touched, ", "))
		} else {
			jc.Log("deploy.dryStackNew", project, len(res.Files))
		}
		if res.Env != "none" && res.Env != "" {
			jc.Log("deploy.dryEnv." + res.Env)
		}
		if res.EnvEdited {
			jc.Log("deploy.dryEnvEdited")
		}
	}
	if on("config") {
		if unset := withoutNames(res.UnsetVars, envKeysMissing); len(unset) > 0 {
			problems++
			jc.Log("deploy.dryUnsetVars", strings.Join(unset, ", "))
		}
		if !res.ConfigOK {
			jc.Log("deploy.dryConfigBad", res.ConfigError)
			return problems + 1
		}
		jc.Log("deploy.dryConfigOK", strings.Join(res.Services, ", "))
	} else if !res.ConfigOK {
		return problems // compose файл не принял — дальше проверять нечего
	}
	// Ресурсы: мало — проблема (не скачать образы, не поднять базу),
	// впритык — предупреждение.
	if (res.MemAvailableMB > 0 || res.DiskFreeMB > 0) && on("resources") {
		jc.Log("deploy.dryResources", sizeText(res.MemAvailableMB), sizeText(res.DiskFreeMB))
		switch {
		case res.DiskFreeMB > 0 && res.DiskFreeMB < 2048:
			problems++
			jc.Log("deploy.dryDiskLow", res.DiskFreeMB)
		case res.MemAvailableMB > 0 && res.MemAvailableMB < 256:
			problems++
			jc.Log("deploy.dryMemLow", res.MemAvailableMB)
		case res.MemAvailableMB > 0 && res.MemAvailableMB < 1024:
			jc.Log("deploy.dryMemTight", res.MemAvailableMB)
		}
	}
	if len(res.NoHealthcheck) > 0 && on("health") {
		jc.Log("deploy.dryNoHealthcheck", strings.Join(res.NoHealthcheck, ", "))
	}
	for _, pb := range res.PortsBusy {
		if !on("ports") {
			break
		}
		problems++
		jc.Log("deploy.dryPortBusy", pb.Addr, pb.Holder)
	}
	for _, img := range res.Images {
		if !on("images") {
			break // старый хост проверил и так — не показываем
		}
		if img.ArchMismatch {
			problems++
			jc.Log("deploy.dryImageArch", img.Image, strings.Join(img.Arches, ", "), res.HostArch)
			continue
		}
		if tag := imageTag(img.Image); tag == "" || tag == "latest" {
			jc.Log("deploy.dryImageUnpinned", img.Image)
		}
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
		PipelineID int64    `json:"pipeline_id"`
		Content    string   `json:"content"`
		Ref        string   `json:"ref"`
		Tag        string   `json:"tag"`
		Skip       []string `json:"skip"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	name := msgs.Tc(ctx, "deploy.dryNewPipeline")
	tok, byToken := tokenFromContext(ctx)
	if byToken && tok.Scoped() && (req.PipelineID == 0 || strings.TrimSpace(req.Content) != "") {
		// Произвольное описание может смотреть на любые хосты.
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("auth.tokenDryContent"))
		return
	}
	if req.PipelineID > 0 {
		pl, err := s.db.PipelineByID(ctx, req.PipelineID)
		if err == nil && !s.pipelineInScope(ctx, pl) {
			err = store.ErrNotFound
		}
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
	for _, k := range req.Skip {
		if !slices.Contains(DryChecks, k) {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("deploy.specBad", "skip", k))
			return
		}
	}
	if req.PipelineID > 0 && !byToken {
		// Выбор галочек — у конвейера: следующий прогон откроется с ним
		// (прогон токеном выбор администраторов не трогает).
		raw, _ := json.Marshal(req.Skip)
		if len(req.Skip) == 0 {
			raw = nil
		}
		_ = s.db.SetPipelineDrySkip(ctx, req.PipelineID, string(raw))
	}
	queue := "deploy:dryrun"
	if req.PipelineID > 0 {
		// Та же очередь, что у выкладок конвейера: checkout в тот же каталог.
		queue = fmt.Sprintf("deploy:%d", req.PipelineID)
	}
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindDeploy, TitleKey: "deploy.dryTitle", TitleArgs: []any{name},
		Queue: queue, Author: user, Steps: 3,
		Params: DeployParams{DryRun: true, PipelineID: req.PipelineID, Content: req.Content, Ref: req.Ref, Tag: req.Tag, Skip: req.Skip},
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

// logSitePort — сервис и порт сайта против того, что объявляют стек и
// образ; число проблем и порт, который получит сайт (auto — из образа).
func logSitePort(jc *jobs.Context, c *deploy.ComposeSpec, res composeCheck) (int, int) {
	sp := c.Site
	ports := make([]string, len(res.SitePorts))
	for i, p := range res.SitePorts {
		ports[i] = strconv.Itoa(p)
	}
	switch {
	case !res.SiteFound:
		jc.Log("deploy.drySiteNoService", sp.Service, c.Project, strings.Join(res.Services, ", "))
		return 1, 0
	case sp.Port == 0 && len(res.SitePorts) == 1:
		jc.Log("deploy.sitePortAuto", res.SitePorts[0], res.SiteImage)
		return 0, res.SitePorts[0]
	case sp.Port == 0:
		jc.Log("deploy.drySitePortAutoMany", res.SiteImage, strings.Join(ports, ", "))
		return 1, 0
	case len(res.SitePorts) == 0:
		jc.Log("deploy.drySitePortUnknown", res.SiteImage)
	case !slices.Contains(res.SitePorts, sp.Port) && len(res.SitePorts) == 1:
		jc.Log("deploy.drySitePortBadOne", sp.Port, res.SiteImage, res.SitePorts[0], res.SitePorts[0])
		return 1, sp.Port
	case !slices.Contains(res.SitePorts, sp.Port):
		jc.Log("deploy.drySitePortBad", sp.Port, sp.Service, res.SiteImage, strings.Join(ports, ", "))
		return 1, sp.Port
	default:
		jc.Log("deploy.drySitePortOK", sp.Port, sp.Service)
	}
	return 0, sp.Port
}

// imageTag — тег ссылки на образ (пусто — не указан, то есть latest;
// закреплённый дайджестом — «@»).
func imageTag(ref string) string {
	if strings.Contains(ref, "@") {
		return "@"
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[i+1:]
	}
	return ""
}

// pipelinesWithProject — другие конвейеры compose с тем же стеком.
func (s *Server) pipelinesWithProject(ctx context.Context, self int64, project string) []string {
	list, err := s.db.ListPipelines(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range list {
		if p.ID == self {
			continue
		}
		if spec, err := deploy.ParseSpec(p.Content); err == nil && spec.Compose != nil && spec.Compose.Project == project {
			out = append(out, p.Name)
		}
	}
	return out
}

// dryHostVersion — nkt на хосте старее хаба: части проверок там нет
// (проблема — прогон по такому хосту неполный).
func (s *Server) dryHostVersion(ctx context.Context, jc *jobs.Context, t targetHost) int {
	if t.ID == localHostID {
		return 0
	}
	h, err := s.db.HostByID(ctx, t.ID)
	hub := s.hub.version
	if err != nil || h.NktVersion == "" || hub == "" || hub == "dev" || h.NktVersion == "dev" {
		return 0
	}
	if deploy.CompareVersions(strings.TrimPrefix(h.NktVersion, "v"), strings.TrimPrefix(hub, "v")) < 0 {
		jc.Log("deploy.dryHostOlder", t.Name, h.NktVersion, hub)
		return 1
	}
	return 0
}

// sizeText — «512 МБ» / «3.4 ГБ» / «—» (не узнать).
func sizeText(mb int) string {
	switch {
	case mb <= 0:
		return "—"
	case mb < 1024:
		return strconv.Itoa(mb) + " MB"
	default:
		return strconv.FormatFloat(float64(mb)/1024, 'f', 1, 64) + " GB"
	}
}

// withoutNames — list без имён из skip (уже названы другой проблемой).
func withoutNames(list, skip []string) []string {
	var out []string
	for _, n := range list {
		if !slices.Contains(skip, n) {
			out = append(out, n)
		}
	}
	return out
}

// dryOn — включена ли проверка сухого прогона (галочка).
type dryOn func(key string) bool

// DryChecks — проверки сухого прогона, которые можно снять галочкой (в
// окне сухого прогона; выбор хранится у конвейера).
var DryChecks = []string{"engine", "config", "images", "ports", "resources", "health", "stack", "site_dns", "site_outside", "site_host", "site_cert", "version"}

// dryHostChecks — проверки, которые делает хост (снятые — не тратит на
// них время и запросы к registry).
var dryHostChecks = []string{"images", "ports", "resources", "health"}

func dryChecksOn(skip []string) dryOn {
	off := map[string]bool{}
	for _, k := range skip {
		off[k] = true
	}
	return func(key string) bool { return !off[key] }
}

func (on dryOn) hostSkip() []string {
	var out []string
	for _, k := range dryHostChecks {
		if !on(k) {
			out = append(out, k)
		}
	}
	return out
}

// skippedNames — названия снятых проверок для журнала.
func (on dryOn) skippedNames(lang msgs.Lang) string {
	var names []string
	for _, k := range DryChecks {
		if !on(k) {
			names = append(names, msgs.T(lang, "deploy.dryCheck."+k))
		}
	}
	return strings.Join(names, ", ")
}
