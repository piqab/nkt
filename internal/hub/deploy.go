package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Выкладки хаба (internal/deploy): задание берёт репозиторий на нужном
// коммите и выкладывает его манифестом в кластеры, Helm-релизом или
// сценарием хаба. Запускают его кнопка, вебхук, опрос репозитория и
// слежение за registry (deploy_triggers.go); одна выкладка конвейера за
// раз — очередь задания по конвейеру.

// KindDeploy — задание выкладки.
const KindDeploy = "deploy.run"

// DeployParams — вход задания: выкладка и описание конвейера на момент
// запуска (правка описания потом не меняет уже начатую выкладку).
type DeployParams struct {
	DeploymentID int64  `json:"deployment_id"`
	Content      string `json:"content"`
	// DryRun — сухой прогон: без записи выкладки и без изменений на
	// хостах; конвейер (для доступа и .env) — PipelineID (0 — новый,
	// ещё не сохранённый), ветка и тег — Ref и Tag.
	DryRun     bool   `json:"dry_run,omitempty"`
	PipelineID int64  `json:"pipeline_id,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Tag        string `json:"tag,omitempty"`
	// Skip — проверки сухого прогона, с которых сняли галочки.
	Skip []string `json:"skip,omitempty"`
}

// DeployRunner — исполнитель выкладки.
type DeployRunner struct{ s *Server }

// NewDeployRunner строит исполнителя.
func NewDeployRunner(s *Server) *DeployRunner { return &DeployRunner{s: s} }

// Resumable — нет: после перезапуска хаба выкладку честно помечаем
// прерванной, а не продолжаем вслепую с середины apply.
func (r *DeployRunner) Resumable() bool { return false }

func (s *Server) pipelineDir(id int64) string {
	return filepath.Join(s.hub.cfg.DataDir, "deploy", strconv.FormatInt(id, 10))
}

// pipelineCred — расшифрованный доступ к репозиторию.
func (s *Server) pipelineCred(p store.Pipeline) deploy.Cred {
	var c deploy.Cred
	if len(p.GitCred) == 0 {
		return c
	}
	if raw, err := secretbox.Decrypt(s.hub.key, p.GitCred); err == nil {
		_ = json.Unmarshal(raw, &c)
	}
	return c
}

// Run выполняет выкладку.
func (r *DeployRunner) Run(ctx context.Context, jc *jobs.Context) (err error) {
	s := r.s
	var p DeployParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	if p.DryRun {
		return r.dryRun(ctx, jc, p)
	}
	d, err := s.db.DeploymentByID(ctx, p.DeploymentID)
	if err != nil {
		return err
	}
	pl, err := s.db.PipelineByID(ctx, d.PipelineID)
	if err != nil {
		return err
	}
	commit := d.Commit
	var autoRetry bool
	defer func() {
		status, text := store.DeploySucceeded, ""
		if err != nil {
			status, text = store.DeployFailed, msgs.Localize(jc.Lang(), err)
			// Опрос и registry сравнивают с последним удачным: без этого
			// упавший коммит выкладывался бы заново каждый интервал.
			_ = s.db.SetPipelineFailed(context.WithoutCancel(ctx), pl.ID, commit, d.Tag)
			if autoRetry && commit != "" {
				jc.Log("deploy.failedRemembered", deploy.ShortSHA(commit)+" "+d.Tag)
			}
		}
		_ = s.db.UpdateDeployment(context.WithoutCancel(ctx), d.ID, status, commit, text)
		s.emitDeploy(context.WithoutCancel(ctx), pl, d, jc.Job.ID, commit, err)
	}()
	_ = s.db.UpdateDeployment(ctx, d.ID, store.DeployRunning, "", "")

	spec, err := deploy.ParseSpec(p.Content)
	if err != nil {
		return err
	}
	autoRetry = spec.Poll != "" || spec.Registry != ""
	g := deploy.Git{Dir: s.pipelineDir(pl.ID), Cred: s.pipelineCred(pl)}

	// Коммит: заданный (откат), иначе вершина тега или ветки.
	ref := d.Ref
	if ref == "" {
		ref = spec.Ref
	}
	if commit == "" {
		if ref, commit, err = resolveRef(ctx, g, spec.Repo, ref, d.Tag); err != nil {
			return err
		}
	}
	vars := deploy.Vars{Tag: d.Tag, Commit: commit, Ref: ref}
	if vars.Tag == "" {
		vars.Tag = deploy.ShortSHA(commit)
	}
	jc.StepKey(1, 3, "deploy.stepCheckout", ref, deploy.ShortSHA(commit))
	src := filepath.Join(s.pipelineDir(pl.ID), "src")
	if err := g.Checkout(ctx, spec.Repo, ref, commit, src); err != nil {
		return err
	}

	switch spec.Action {
	case deploy.ActionManifest:
		err = r.applyManifests(ctx, jc, pl, spec, src, vars)
	case deploy.ActionHelm:
		err = r.installHelm(ctx, jc, spec, src, vars)
	case deploy.ActionScript:
		err = r.runScript(ctx, jc, pl, spec, src, vars)
	case deploy.ActionCompose:
		err = r.deployCompose(ctx, jc, pl, spec, src, vars)
	}
	if err != nil {
		return err
	}
	_ = s.db.SetPipelineDeployed(ctx, pl.ID, commit, d.Tag)
	jc.Log("deploy.done", deploy.ShortSHA(commit)+" "+d.Tag)
	return nil
}

// resolveRef — коммит ветки или тега: заданный тег, иначе ветка ref, а
// если такой ветки нет — тег с этим именем (ссылка на файл в теге).
func resolveRef(ctx context.Context, g deploy.Git, repo, ref, tag string) (string, string, error) {
	refs, err := g.Remote(ctx, repo)
	if err != nil {
		return ref, "", err
	}
	switch {
	case tag != "" && refs["refs/tags/"+tag] != "":
		return tag, refs["refs/tags/"+tag], nil
	case refs["refs/heads/"+ref] != "":
		return ref, refs["refs/heads/"+ref], nil
	case refs["refs/tags/"+ref] != "":
		return ref, refs["refs/tags/"+ref], nil
	}
	return ref, "", msgs.Errorf("deploy.refNotFound", ref)
}

// pipelineClusters — кластеры по именам и по группе хоста кластера.
func (s *Server) pipelineClusters(ctx context.Context, spec deploy.Spec) ([]store.Cluster, error) {
	all, err := s.db.ListClusters(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Cluster
	for _, c := range all {
		if c.Status != store.ClusterReady {
			continue
		}
		match := slices.Contains(spec.Clusters, c.Name)
		if !match && spec.Group != "" {
			if h, err := s.db.HostByID(ctx, c.HostID); err == nil && h.Group == spec.Group {
				match = true
			}
		}
		if match {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, msgs.Errorf("deploy.noClusters")
	}
	return out, nil
}

func (r *DeployRunner) applyManifests(ctx context.Context, jc *jobs.Context, pl store.Pipeline, spec deploy.Spec, src string, vars deploy.Vars) error {
	s := r.s
	var parts []string
	for _, f := range spec.Manifests {
		text, err := deploy.ReadFile(src, f)
		if err != nil {
			return err
		}
		parts = append(parts, strings.TrimSpace(vars.Substitute(text)))
	}
	content := strings.Join(parts, "\n---\n") + "\n"
	if _, err := k8s.ParseManifest(content); err != nil {
		return err
	}
	clusters, err := s.pipelineClusters(ctx, spec)
	if err != nil {
		return err
	}
	var results []ManifestResult
	failed := 0
	for i, cl := range clusters {
		jc.StepKey(2+i, 2+len(clusters), "deploy.stepApply", cl.Name)
		res := ManifestResult{ClusterID: cl.ID, Cluster: cl.Name}
		hosts, _ := s.db.ClusterHosts(ctx, cl.ID)
		if len(hosts) == 0 {
			res.Error = msgs.T(jc.Lang(), "hub.clusterNoNodes")
		} else {
			var out struct {
				Output string `json:"output"`
			}
			body := map[string]string{"content": content, "note": msgs.T(jc.Lang(), "hub.manifestNote", "pipeline "+pl.Name, deploy.ShortSHA(vars.Commit))}
			if _, err := s.hub.HostAPI(ctx, hosts[0].ID, "POST", "/api/k8s/apply", body, &out); err != nil {
				res.Error = msgs.Localize(jc.Lang(), err)
			} else {
				res.Output = out.Output
			}
		}
		if res.Error != "" {
			failed++
			jc.Log("deploy.clusterFailed", cl.Name, res.Error)
		} else {
			for _, l := range strings.Split(strings.TrimSpace(res.Output), "\n") {
				jc.Logf("      %s", l)
			}
		}
		results = append(results, res)
	}
	raw, _ := json.Marshal(results)
	_, _, _ = s.db.SaveManifestVersion(ctx, store.Manifest{Name: "pipeline: " + pl.Name, Content: content,
		Note: deploy.ShortSHA(vars.Commit) + " " + vars.Ref, Author: jc.Job.Author}, string(raw))
	if failed > 0 {
		return msgs.Errorf("deploy.failedClusters", failed, len(clusters))
	}
	return nil
}

func (r *DeployRunner) installHelm(ctx context.Context, jc *jobs.Context, spec deploy.Spec, src string, vars deploy.Vars) error {
	s := r.s
	h := spec.Helm
	values := ""
	if h.Values != "" {
		text, err := deploy.ReadFile(src, h.Values)
		if err != nil {
			return err
		}
		values = vars.Substitute(text)
	}
	if h.TagKey != "" {
		var err error
		if values, err = SetYAMLKey(values, h.TagKey, vars.Tag); err != nil {
			return err
		}
	}
	rel := k8s.HelmInstallRequest{RepoName: h.RepoName, RepoURL: h.RepoURL, Chart: h.Chart, Version: h.Version,
		Release: h.Release, Namespace: h.Namespace, Values: values}
	if err := rel.Validate(); err != nil {
		return err
	}
	clusters, err := s.pipelineClusters(ctx, spec)
	if err != nil {
		return err
	}
	runner := &HelmMultiRunner{m: s.hub}
	waiter := &GroupApplyRunner{m: s.hub}
	failed := 0
	for i, cl := range clusters {
		jc.StepKey(2+i, 2+len(clusters), "deploy.stepHelm", cl.Name)
		if err := runner.installOn(ctx, jc, waiter, cl.ID, rel); err != nil {
			failed++
			jc.Log("deploy.clusterFailed", cl.Name, msgs.Localize(jc.Lang(), err))
		}
	}
	if failed > 0 {
		return msgs.Errorf("deploy.failedClusters", failed, len(clusters))
	}
	return nil
}

// SetYAMLKey ставит значение по пути a.b.c (карты создаются по дороге).
func SetYAMLKey(text, path, value string) (string, error) {
	doc := map[string]any{}
	if strings.TrimSpace(text) != "" {
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			return "", msgs.Errorf("k8s.manifestYAML", err.Error())
		}
		if doc == nil {
			doc = map[string]any{}
		}
	}
	keys := strings.Split(path, ".")
	cur := doc
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[keys[len(keys)-1]] = value
	b, err := yaml.Marshal(doc)
	return string(b), err
}

func (r *DeployRunner) runScript(ctx context.Context, jc *jobs.Context, pl store.Pipeline, spec deploy.Spec, src string, vars deploy.Vars) error {
	s := r.s
	content, err := deploy.ReadFile(src, spec.Script)
	if err != nil {
		return err
	}
	values := map[string]string{"param:TAG": vars.Tag, "param:COMMIT": vars.Commit, "param:REF": vars.Ref}
	parsed, issues := script.Parse(content, values)
	if len(issues) == 0 {
		issues = script.Check(parsed, s.refsNowCtx(ctx))
	}
	if len(issues) > 0 {
		return msgs.Errorf("hub.scriptParseFailed", issues[0].Line, msgs.Localize(jc.Lang(), issues[0].Err))
	}
	jc.StepKey(2, 3, "deploy.stepScript", spec.Script)
	sr := s.ScriptRunner()
	ticket := sr.keep(values)
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindScriptRun, Title: msgs.T(jc.Lang(), "hub.scriptRunTitle", "pipeline "+pl.Name),
		Queue: "script:pipeline:" + strconv.FormatInt(pl.ID, 10), Author: jc.Job.Author, Steps: len(parsed.Steps),
		Params: ScriptRunParams{Name: "pipeline " + pl.Name, Content: content, Ticket: ticket},
	})
	if err != nil {
		sr.forget(ticket)
		return err
	}
	jc.Logf("      job #%d", id)
	for {
		job, err := s.db.JobByID(ctx, id)
		if err == nil && job.Done() {
			if job.Status != store.JobSucceeded {
				return msgs.Errorf("deploy.scriptFailed", job.Error)
			}
			return nil
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
}

// randomToken — случайные байты в base64url.
func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startDeployment заводит выкладку и её задание. skipIfBusy — для опроса и
// registry: пока идёт выкладка, новую не ставить (придёт следующий опрос).
func (s *Server) startDeployment(ctx context.Context, pl store.Pipeline, d store.Deployment, skipIfBusy bool) (store.Deployment, error) {
	if skipIfBusy {
		if busy, _ := s.db.ActiveDeployment(ctx, pl.ID); busy {
			return d, msgs.Errorf("deploy.busy")
		}
	}
	d.PipelineID = pl.ID
	d.EnvVersion = s.ensureEnvVersion(ctx, pl)
	id, err := s.db.CreateDeployment(ctx, d)
	if err != nil {
		return d, err
	}
	d.ID = id
	what := d.Tag
	if what == "" {
		what = d.Ref
	}
	if what == "" && d.Commit != "" {
		what = deploy.ShortSHA(d.Commit)
	}
	jobID, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindDeploy, TitleKey: "deploy.jobTitle", TitleArgs: []any{pl.Name, what},
		Queue: fmt.Sprintf("deploy:%d", pl.ID), Author: d.Author, Steps: 3,
		Params: DeployParams{DeploymentID: id, Content: pl.Content},
	})
	if err != nil {
		_ = s.db.UpdateDeployment(ctx, id, store.DeployFailed, "", err.Error())
		return d, err
	}
	_ = s.db.SetDeploymentJob(ctx, id, jobID)
	d.JobID = jobID
	return d, nil
}
