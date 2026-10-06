package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Kubernetes на хосте (см. internal/k8s): состояние и узлы, установка
// роли заданием, токен и kubeconfig для хаба, поды для вкладки.

func (s *Server) k8sManager() *k8s.Manager {
	var run control.PrivilegedRunner
	if s.cfg.Mode == config.ModeLocal {
		run = RunUnrestricted
	}
	return k8s.New(s.scanner.Collector(), run).WithRunDir(filepath.Join(s.cfg.DataDir, ".run"))
}

func (s *Server) handleK8sStatus(w http.ResponseWriter, r *http.Request) {
	m := s.k8sManager()
	st := m.Status(r.Context())
	resp := map[string]any{"status": st}
	if st.Installed && st.Role == k8s.RoleServer && st.Active {
		nodes, err := m.Nodes(r.Context())
		if err != nil {
			resp["nodes_error"] = msgs.Localize(msgs.LangFromRequest(r), err)
		} else {
			resp["nodes"] = nodes
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleK8sInstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "k8s.fixturesDisabled"))
		return
	}
	var spec k8s.InstallSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := spec.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.T(msgs.LangFromRequest(r), "api.backgroundJobsAreUnavailable"))
		return
	}
	// Хост сам решает, идти ли через кэш хаба: порт есть — идёт.
	spec.HubCache = s.hubCacheURL()
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: k8s.KindInstall, TitleKey: "k8s.installJobTitle", TitleArgs: []any{spec.Flavor, spec.Role},
		Queue: "host", Author: user, Steps: len(k8s.Steps(spec)), Params: spec,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "k8s.install", spec.Flavor+"/"+spec.Role, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

func (s *Server) handleK8sJoin(w http.ResponseWriter, r *http.Request) {
	info, err := s.k8sManager().Join(r.Context(), r.URL.Query().Get("server"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleK8sKubeconfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.k8sManager().Kubeconfig(r.Context(), r.URL.Query().Get("server"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write([]byte(cfg))
}

func (s *Server) handleK8sPods(w http.ResponseWriter, r *http.Request) {
	raw, err := s.k8sManager().Pods(r.Context(), r.URL.Query().Get("namespace"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (s *Server) handleK8sUninstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "k8s.fixturesDisabled"))
		return
	}
	user := auth.Username(r.Context())
	// ?job=1 — заданием: удаление идёт минутами, а запрос браузера
	// обрывается раньше и прерывал бы его на полпути.
	if wantsJob(r) {
		script := s.k8sManager().UninstallScript(r.Context())
		if script == "" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		s.startCmdJob(w, r, "k8s.uninstallJobTitle", nil, "host",
			cmdjob.Params{Commands: []cmdjob.Command{{Script: script, StepKey: "k8s.uninstallJobTitle"}}, Refresh: true},
			"k8s.uninstall", "")
		return
	}
	err := s.k8sManager().Uninstall(r.Context())
	s.db.Audit(r.Context(), user, "k8s.uninstall", "", auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Пробросы портов хоста в машины (см. control.PortForwardManager).

func (s *Server) portForwards() *control.PortForwardManager {
	var run control.PrivilegedRunner
	if s.cfg.Mode == config.ModeLocal {
		run = RunUnrestricted
	}
	return control.NewPortForwardManager(s.scanner.Collector(), run)
}

func (s *Server) handlePortForwardList(w http.ResponseWriter, r *http.Request) {
	list, err := s.portForwards().List()
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forwards": list})
}

func (s *Server) handlePortForwardSet(w http.ResponseWriter, r *http.Request) {
	var pf control.PortForward
	if err := decodeJSON(r, &pf); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	err := s.portForwards().Set(r.Context(), pf)
	s.db.Audit(r.Context(), user, "portforward.set", pf.Name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handlePortForwardDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user := auth.Username(r.Context())
	err := s.portForwards().Remove(r.Context(), name)
	s.db.Audit(r.Context(), user, "portforward.remove", name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleK8sResources — GET /k8s/resources?kind=deployments&namespace=…:
// объекты вида (в том числе cr:<plural>.<group>) в строках таблицы.
func (s *Server) handleK8sResources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.k8sManager().Resources(r.Context(), q.Get("kind"), q.Get("namespace"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleK8sCRDs — GET /k8s/crds: пользовательские виды кластера.
func (s *Server) handleK8sCRDs(w http.ResponseWriter, r *http.Request) {
	list, err := s.k8sManager().CRDs(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"crds": list})
}

// handleK8sConfigMapData — GET /k8s/configmaps/data?namespace=&name=.
func (s *Server) handleK8sConfigMapData(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data, err := s.k8sManager().Data(r.Context(), "configmaps", q.Get("namespace"), q.Get("name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// handleK8sSecretReveal — POST /k8s/secrets/reveal {namespace, name}:
// значения секрета администратору, с записью в аудит.
func (s *Server) handleK8sSecretReveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	data, err := s.k8sManager().Data(r.Context(), "secrets", req.Namespace, req.Name)
	user := auth.Username(r.Context())
	s.db.Audit(r.Context(), user, "k8s.secret.reveal", req.Namespace+"/"+req.Name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// ------------------------------------------------------- действия с объектами

// k8sObjectText — GET-обработчик текста об объекте (describe, history).
func (s *Server) k8sObjectText(w http.ResponseWriter, r *http.Request, read func(ctx context.Context, kind, ns, name string) (string, error)) {
	q := r.URL.Query()
	text, err := read(r.Context(), q.Get("kind"), q.Get("namespace"), q.Get("name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// handleK8sDescribe — GET /k8s/describe?kind=&namespace=&name=.
func (s *Server) handleK8sDescribe(w http.ResponseWriter, r *http.Request) {
	s.k8sObjectText(w, r, s.k8sManager().Describe)
}

// handleK8sRolloutHistory — GET /k8s/rollout/history?kind=&namespace=&name=.
func (s *Server) handleK8sRolloutHistory(w http.ResponseWriter, r *http.Request) {
	s.k8sObjectText(w, r, s.k8sManager().RolloutHistory)
}

// handleK8sPodContainers — GET /k8s/pods/containers?namespace=&name=.
func (s *Server) handleK8sPodContainers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t, err := s.k8sManager().Find(r.Context(), "pods", q.Get("namespace"), q.Get("name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": t.Containers()})
}

// handleK8sAction — POST /k8s/objects/action {kind, namespace, name,
// action, replicas, revision}: удалить, масштабировать, перезапустить,
// откатить, cordon, запустить CronJob сейчас, приостановить.
func (s *Server) handleK8sAction(w http.ResponseWriter, r *http.Request) {
	var req k8s.ActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	out, err := s.k8sManager().Act(r.Context(), req)
	target := req.Kind + " " + strings.TrimPrefix(req.Namespace+"/"+req.Name, "/")
	detail := map[string]any{}
	if req.Action == "scale" {
		detail["replicas"] = req.Replicas
	}
	if req.Action == "undo" && req.Revision > 0 {
		detail["revision"] = req.Revision
	}
	if err != nil {
		detail["error"] = errText(err)
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "k8s."+req.Action, target, auditResult(err), detail)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

// handleK8sNamespaceCreate — POST /k8s/namespaces {name}.
func (s *Server) handleK8sNamespaceCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	out, err := s.k8sManager().CreateNamespace(r.Context(), req.Name)
	s.db.Audit(r.Context(), auth.Username(r.Context()), "k8s.namespace.create", req.Name, auditResult(err), errText(err))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

// handleK8sDrain — POST /k8s/nodes/drain {name}: вывод узла заданием
// (выселение подов идёт минутами).
func (s *Server) handleK8sDrain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m := s.k8sManager()
	t, err := m.Find(r.Context(), "nodes", "", req.Name)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	argv, err := m.KubectlArgv(r.Context(), k8s.DrainArgs(t.Name)...)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.startCmdJob(w, r, "k8s.drainJobTitle", []any{t.Name}, "k8s:drain:"+t.Name,
		cmdjob.Params{Commands: []cmdjob.Command{{Argv: argv}}}, "k8s.drain", t.Name)
}

// k8sPodSession — под и контейнер из листинга для PTY-сессии.
func (s *Server) k8sPodSession(w http.ResponseWriter, r *http.Request) (*k8s.Manager, k8s.Target, string, bool) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return nil, k8s.Target{}, "", false
	}
	q := r.URL.Query()
	m := s.k8sManager()
	t, err := m.Find(r.Context(), "pods", q.Get("namespace"), q.Get("name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return nil, k8s.Target{}, "", false
	}
	c, ok := t.Container(q.Get("container"))
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "k8s.badContainer", q.Get("container")))
		return nil, k8s.Target{}, "", false
	}
	return m, t, c, true
}

// handleK8sPodLogsWS — GET /k8s/pods/logs/ws?namespace=&name=&container=&tail=&follow=1&previous=1.
func (s *Server) handleK8sPodLogsWS(w http.ResponseWriter, r *http.Request) {
	m, t, c, ok := s.k8sPodSession(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	if tail <= 0 || tail > 10000 {
		tail = 200
	}
	argv, err := m.KubectlArgv(r.Context(), k8s.LogsArgs(t, c, tail, q.Get("follow") == "1", q.Get("previous") == "1")...)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cmd := unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, argv...)
	s.runPTYSession(w, r, cmd, "k8s-logs", t.Namespace+"/"+t.Name+"/"+c, s.cfg.TerminalIdleTimeout)
}

// handleK8sPodExecWS — GET /k8s/pods/exec/ws?namespace=&name=&container=:
// консоль в контейнере пода, те же ворота, что у консоли контейнеров.
func (s *Server) handleK8sPodExecWS(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TerminalEnabled {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.disabled"))
		return
	}
	m, t, c, ok := s.k8sPodSession(w, r)
	if !ok {
		return
	}
	argv, err := m.KubectlArgv(r.Context(), k8s.ExecArgs(t, c)...)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "k8s.exec", t.Namespace+"/"+t.Name+"/"+c, "ok", "")
	cmd := unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, argv...)
	s.runPTYSession(w, r, cmd, "console", "k8s:"+t.Namespace+"/"+t.Name+"/"+c, s.cfg.TerminalIdleTimeout)
}

// ------------------------------------------------------- YAML объектов

// k8sDocs — YAML объектов для истории версий конфигураций (k8s://…).
type k8sDocs struct{ s *Server }

func (d k8sDocs) ValidDocPath(path string) bool {
	_, _, _, ok := k8s.ParseDocPath(path)
	return ok
}

func (d k8sDocs) CurrentDoc(ctx context.Context, path string) (string, error) {
	kind, ns, name, ok := k8s.ParseDocPath(path)
	if !ok {
		return "", msgs.Errorf("k8s.badKind", path)
	}
	doc, err := d.s.k8sManager().YAML(ctx, kind, ns, name)
	return doc.Content, err
}

func (d k8sDocs) RestoreDoc(ctx context.Context, user, path, content string) (string, error) {
	kind, ns, name, ok := k8s.ParseDocPath(path)
	if !ok {
		return "", msgs.Errorf("k8s.badKind", path)
	}
	m := d.s.k8sManager()
	t, err := m.Find(ctx, kind, ns, name)
	if err == nil {
		err = m.CheckIdentity(ctx, t, content)
	}
	if err == nil {
		_, err = m.Apply(ctx, content, t.Namespace)
	}
	d.s.db.Audit(ctx, user, "k8s.yaml.rollback", path, auditResult(err), errText(err))
	if err != nil {
		return "", err
	}
	return d.s.k8sAppliedText(ctx, m, t, content), nil
}

// k8sAppliedText — что записать в историю после apply: YAML из кластера,
// а в fixtures (apply имитируется) — присланный текст.
func (s *Server) k8sAppliedText(ctx context.Context, m *k8s.Manager, t k8s.Target, content string) string {
	if s.cfg.Mode == config.ModeFixtures {
		return content
	}
	if doc, err := m.YAML(ctx, t.Kind, t.Namespace, t.Name); err == nil {
		return doc.Content
	}
	return content
}

// handleK8sYAML — GET /k8s/yaml?kind=&namespace=&name=.
func (s *Server) handleK8sYAML(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	doc, err := s.k8sManager().YAML(r.Context(), q.Get("kind"), q.Get("namespace"), q.Get("name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

type k8sYAMLRequest struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Note      string `json:"note"`
	Expected  string `json:"expected_sha256"`
}

// handleK8sYAMLDiff — POST /k8s/yaml/diff {kind, namespace, name, content}:
// kubectl diff с кластером. Без kind — манифест новых объектов.
func (s *Server) handleK8sYAMLDiff(w http.ResponseWriter, r *http.Request) {
	var req k8sYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	m := s.k8sManager()
	ns := ""
	if req.Kind != "" {
		t, err := m.Find(r.Context(), req.Kind, req.Namespace, req.Name)
		if err == nil {
			err = m.CheckIdentity(r.Context(), t, req.Content)
		}
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		ns = t.Namespace
	}
	diff, err := m.Diff(r.Context(), req.Content, ns)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"diff": diff})
}

// handleK8sYAMLWrite — PUT /k8s/yaml: kubectl apply правки объекта,
// версия — в истории конфигураций (k8s://…).
func (s *Server) handleK8sYAMLWrite(w http.ResponseWriter, r *http.Request) {
	var req k8sYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	m := s.k8sManager()
	cur, err := m.YAML(ctx, req.Kind, req.Namespace, req.Name)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.Expected != "" && req.Expected != cur.SHA256 {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "k8s.yamlStale"))
		return
	}
	t, err := m.Find(ctx, req.Kind, req.Namespace, req.Name)
	if err == nil {
		err = m.CheckIdentity(ctx, t, req.Content)
	}
	var out string
	if err == nil {
		out, err = m.Apply(ctx, req.Content, t.Namespace)
	}
	s.db.Audit(ctx, user, "k8s.yaml.apply", cur.HistoryPath, auditResult(err), map[string]any{"note": req.Note, "error": errText(err)})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var id int64
	if s.configs != nil {
		id, _ = s.configs.RecordDoc(ctx, cur.HistoryPath, "k8s", user, store.ActionEdit, req.Note, []byte(cur.Content), []byte(s.k8sAppliedText(ctx, m, t, req.Content)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": out, "version_id": id})
}

// handleK8sApply — POST /k8s/apply {content, note}: новые объекты из
// манифеста (шаблона). Версии — тем объектам, что нашлись после записи.
func (s *Server) handleK8sApply(w http.ResponseWriter, r *http.Request) {
	var req k8sYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	m := s.k8sManager()
	docs, err := k8s.ParseManifest(req.Content)
	var out string
	if err == nil {
		out, err = m.Apply(ctx, req.Content, "")
	}
	names := make([]string, 0, len(docs))
	for _, d := range docs {
		names = append(names, d.Kind+" "+strings.TrimPrefix(d.Namespace+"/"+d.Name, "/"))
	}
	s.db.Audit(ctx, user, "k8s.apply", strings.Join(names, ", "), auditResult(err), map[string]any{"note": req.Note, "error": errText(err)})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if s.configs != nil {
		for _, d := range docs {
			kind := k8s.KindKey(d.Kind)
			if kind == "" || kind == "secrets" {
				continue
			}
			ns := d.Namespace
			if ns == "" && k8s.Kinds[kind].Namespaced {
				ns = "default"
			}
			if doc, err := m.YAML(ctx, kind, ns, d.Name); err == nil {
				_, _ = s.configs.RecordDoc(ctx, doc.HistoryPath, "k8s", user, store.ActionEdit, req.Note, nil, []byte(doc.Content))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

// handleK8sYAMLBlocks — POST /k8s/yaml/blocks {content}: блоки манифеста
// для блочного режима редактора (только разбор, ничего не меняет).
func (s *Server) handleK8sYAMLBlocks(w http.ResponseWriter, r *http.Request) {
	var req k8sYAMLRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	blocks, err := k8s.ManifestBlocks(req.Content)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks})
}

// handleK8sUpgradeInfo — GET /k8s/upgrade: версия узла и куда можно
// обновиться.
func (s *Server) handleK8sUpgradeInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.k8sManager().Upgrade(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleK8sUpgradeNode — POST /k8s/upgrade/node {version, first}:
// обновление Kubernetes на этом узле заданием.
func (s *Server) handleK8sUpgradeNode(w http.ResponseWriter, r *http.Request) {
	var req k8s.UpgradeSpec
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	st := s.k8sManager().Status(r.Context())
	if !st.Installed {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "k8s.notInstalled"))
		return
	}
	if err := req.Validate(st.Flavor, st.Version); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.HubCache == "" {
		req.HubCache = s.hubCacheURL()
	}
	s.startCmdJob(w, r, "k8s.upgradeJobTitle", []any{req.Version}, "k8s:upgrade",
		cmdjob.Params{Commands: []cmdjob.Command{{Script: k8s.UpgradeScript(st.Flavor, st.Role, req), StepKey: "k8s.upgradeStep", StepArgs: []any{req.Version}}}, Refresh: true},
		"k8s.upgrade", st.Version+" → "+req.Version)
}
