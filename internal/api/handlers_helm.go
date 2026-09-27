package api

import (
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
)

// Helm на control plane (см. internal/k8s/helm.go): чтение — сразу,
// изменения — фоновыми заданиями хоста со стандартным окном журнала.

// handleHelm — GET /k8s/helm: установлен ли helm, релизы, репозитории.
func (s *Server) handleHelm(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.k8sManager().Helm(r.Context()))
}

// handleHelmHistory — GET /k8s/helm/history?namespace=&release=.
func (s *Server) handleHelmHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.k8sManager().HelmHistory(r.Context(), q.Get("namespace"), q.Get("release"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": list})
}

// handleHelmValues — GET /k8s/helm/values?namespace=&release=: значения
// релиза (в них бывают пароли — администратору, с аудитом) и источник
// чарта для формы обновления.
func (s *Server) handleHelmValues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	m := s.k8sManager()
	rel, err := m.FindRelease(r.Context(), q.Get("namespace"), q.Get("release"))
	var values string
	if err == nil {
		switch q.Get("view") {
		case "all":
			values, err = m.HelmAllValues(r.Context(), rel.Namespace, rel.Name)
		case "defaults":
			values, err = m.HelmChartDefaults(r.Context(), rel.Namespace, rel.Name)
		default:
			values, err = m.HelmValues(r.Context(), rel.Namespace, rel.Name)
		}
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "k8s.helm.values", q.Get("namespace")+"/"+q.Get("release"), auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": values, "source": m.Source(rel)})
}

// handleHelmSetup — POST /k8s/helm/setup: установить helm заданием.
func (s *Server) handleHelmSetup(w http.ResponseWriter, r *http.Request) {
	s.startCmdJob(w, r, "k8s.helmSetupJob", []any{k8s.HelmVersion}, "k8s:helm:setup",
		cmdjob.Params{Commands: []cmdjob.Command{{Script: k8s.HelmSetupScript, StepKey: "k8s.helmStep.setup"}}}, "k8s.helm.setup", k8s.HelmVersion)
}

// helmInstallJob — задание установки или обновления релиза.
func (s *Server) helmInstallJob(w http.ResponseWriter, r *http.Request, m *k8s.Manager, req k8s.HelmInstallRequest, titleKey, audit string) {
	if err := req.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	valuesFile, err := m.WriteValues(req.Namespace, req.Release, req.Values)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	argvs, err := m.HelmInstallCommands(r.Context(), req, valuesFile)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m.SaveSource(req)
	cmds := make([]cmdjob.Command, 0, len(argvs))
	for i, argv := range argvs {
		c := cmdjob.Command{Argv: argv}
		switch {
		case i == len(argvs)-1:
			c.StepKey, c.StepArgs = "k8s.helmStep.install", []any{req.Release}
		case i == 0:
			c.StepKey, c.StepArgs = "k8s.helmStep.repoAdd", []any{req.RepoName}
		default:
			c.StepKey, c.StepArgs = "k8s.helmStep.repoUpdate", []any{req.RepoName}
		}
		cmds = append(cmds, c)
	}
	target := req.Namespace + "/" + req.Release
	s.startCmdJob(w, r, titleKey, []any{target, req.ChartRef()}, "k8s:helm:"+target,
		cmdjob.Params{Commands: cmds, Refresh: true}, audit, target+" "+req.ChartRef())
}

// handleHelmInstall — POST /k8s/helm/install {repo_name, repo_url, chart,
// version, release, namespace, values}.
func (s *Server) handleHelmInstall(w http.ResponseWriter, r *http.Request) {
	var req k8s.HelmInstallRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m := s.k8sManager()
	if !m.Helm(r.Context()).Installed {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "k8s.helmMissing"))
		return
	}
	s.helmInstallJob(w, r, m, req, "k8s.helmInstallJob", "k8s.helm.install")
}

// handleHelmUpgrade — POST /k8s/helm/upgrade: то же, но релиз и namespace
// — из helm list.
func (s *Server) handleHelmUpgrade(w http.ResponseWriter, r *http.Request) {
	var req k8s.HelmInstallRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m := s.k8sManager()
	rel, err := m.FindRelease(r.Context(), req.Namespace, req.Release)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req.Namespace, req.Release = rel.Namespace, rel.Name
	s.helmInstallJob(w, r, m, req, "k8s.helmUpgradeJob", "k8s.helm.upgrade")
}

type helmReleaseRequest struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Revision  int    `json:"revision"`
}

// handleHelmRollback — POST /k8s/helm/rollback {namespace, release, revision}.
func (s *Server) handleHelmRollback(w http.ResponseWriter, r *http.Request) {
	var req helmReleaseRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m := s.k8sManager()
	rel, err := m.FindRelease(r.Context(), req.Namespace, req.Release)
	var argv []string
	if err == nil {
		argv, err = m.HelmRollbackArgs(r.Context(), rel, req.Revision)
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	target := rel.Namespace + "/" + rel.Name
	s.startCmdJob(w, r, "k8s.helmRollbackJob", []any{target}, "k8s:helm:"+target,
		cmdjob.Params{Commands: []cmdjob.Command{{Argv: argv, StepKey: "k8s.helmStep.rollback", StepArgs: []any{rel.Name}}}, Refresh: true}, "k8s.helm.rollback", target)
}

// handleHelmUninstall — POST /k8s/helm/uninstall {namespace, release}.
func (s *Server) handleHelmUninstall(w http.ResponseWriter, r *http.Request) {
	var req helmReleaseRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m := s.k8sManager()
	rel, err := m.FindRelease(r.Context(), req.Namespace, req.Release)
	var argv []string
	if err == nil {
		argv, err = m.HelmUninstallArgs(r.Context(), rel)
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	target := rel.Namespace + "/" + rel.Name
	s.startCmdJob(w, r, "k8s.helmUninstallJob", []any{target}, "k8s:helm:"+target,
		cmdjob.Params{Commands: []cmdjob.Command{{Argv: argv, StepKey: "k8s.helmStep.uninstall", StepArgs: []any{rel.Name}}}, Refresh: true}, "k8s.helm.uninstall", target)
}
