package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// API раздела «Выкладки» (все — администратору).

func pipelineIDParam(r *http.Request, name string) (int64, error) {
	var id int64
	_, err := fmt.Sscan(chi.URLParam(r, name), &id)
	return id, err
}

type pipelineJSON struct {
	store.Pipeline
	Last *store.Deployment `json:"last,omitempty"`
	// Action — действие из описания (compose удаляется заданием с хостов).
	Action string `json:"action,omitempty"`
}

// handlePipelines — GET /hub/pipelines: конвейеры с последней выкладкой.
func (s *Server) handlePipelines(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListPipelines(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	out := make([]pipelineJSON, 0, len(list))
	for _, p := range list {
		if !s.pipelineInScope(r.Context(), p) {
			continue
		}
		row := pipelineJSON{Pipeline: p}
		if spec, err := deploy.ParseSpec(p.Content); err == nil {
			// Как удалять: compose — заданием с хостов, прочие — с хаба.
			row.Action = spec.Action
		}
		row.Content = ""
		if ds, err := s.db.Deployments(r.Context(), p.ID, 1); err == nil && len(ds) > 0 {
			row.Last = &ds[0]
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipelines": out})
}

// handlePipelineTemplate — GET /hub/pipelines/template.
func (s *Server) handlePipelineTemplate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"content": deploy.TemplateFor(string(msgs.LangFromRequest(r)))})
}

func validPipelineName(n string) bool {
	n = strings.TrimSpace(n)
	return n != "" && utf8.RuneCountInString(n) <= 80 && !strings.ContainsAny(n, "\n\r\t")
}

// handlePipelineCreate — POST /hub/pipelines {name, content}.
func (s *Server) handlePipelineCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !validPipelineName(req.Name) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "hub.manifestName"))
		return
	}
	if _, err := deploy.ParseSpec(req.Content); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	secret, err := secretbox.Encrypt(s.hub.key, []byte(randomToken(32)))
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	user := auth.Username(r.Context())
	id, err := s.db.CreatePipeline(r.Context(), store.Pipeline{Name: strings.TrimSpace(req.Name), Content: req.Content,
		HookID: randomHex(16), HookSecret: secret, Author: user})
	s.db.Audit(r.Context(), user, "pipeline.create", req.Name, auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func auditOutcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func (s *Server) pipelineFromReq(w http.ResponseWriter, r *http.Request) (store.Pipeline, bool) {
	id, err := pipelineIDParam(r, "id")
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return store.Pipeline{}, false
	}
	p, err := s.db.PipelineByID(r.Context(), id)
	if err == nil && !s.pipelineInScope(r.Context(), p) {
		err = store.ErrNotFound // токену с пределами чужой конвейер не виден
	}
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return store.Pipeline{}, false
	}
	return p, true
}

// handlePipelineGet — GET /hub/pipelines/{id}.
func (s *Server) handlePipelineGet(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.pipelineFromReq(w, r); ok {
		writeJSON(w, http.StatusOK, p)
	}
}

// handlePipelineUpdate — PUT /hub/pipelines/{id} {content, note}.
func (s *Server) handlePipelineUpdate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		Content string `json:"content"`
		Note    string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if _, err := deploy.ParseSpec(req.Content); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	err := s.db.UpdatePipelineContent(r.Context(), p.ID, req.Content, user, req.Note)
	s.db.Audit(r.Context(), user, "pipeline.update", p.Name, auditOutcome(err), req.Note)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePipelineDelete — DELETE /hub/pipelines/{id}.
func (s *Server) handlePipelineDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	// Стек compose живёт на хостах — его убирает задание удаления, а не
	// строка в базе хаба (иначе на хостах остался бы «сирота»).
	if spec, err := deploy.ParseSpec(p.Content); err == nil && spec.Action == deploy.ActionCompose {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("deploy.useRemove"))
		return
	}
	err := s.db.DeletePipeline(r.Context(), p.ID)
	s.db.Audit(r.Context(), auth.Username(r.Context()), "pipeline.delete", p.Name, auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePipelineEnabled — POST /hub/pipelines/{id}/enabled {enabled}.
func (s *Server) handlePipelineEnabled(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	err := s.db.SetPipelineEnabled(r.Context(), p.ID, req.Enabled)
	s.db.Audit(r.Context(), auth.Username(r.Context()), "pipeline.enabled", p.Name, auditOutcome(err), fmt.Sprint(req.Enabled))
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePipelineVersions — GET /hub/pipelines/{id}/versions.
func (s *Server) handlePipelineVersions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	list, err := s.db.PipelineVersions(r.Context(), p.ID)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list})
}

// handlePipelineVersion — GET /hub/pipelines/versions/{version}.
func (s *Server) handlePipelineVersion(w http.ResponseWriter, r *http.Request) {
	id, err := pipelineIDParam(r, "version")
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	v, err := s.db.PipelineVersion(r.Context(), id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handlePipelineCredentials — POST /hub/pipelines/{id}/credentials:
// доступ к репозиторию (токен или ключ) и к registry (логин:токен).
// Пустая строка — не менять, clear_* — убрать.
func (s *Server) handlePipelineCredentials(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		GitToken      string `json:"git_token"`
		SSHKey        string `json:"ssh_key"`
		Registry      string `json:"registry"`
		ClearGit      bool   `json:"clear_git"`
		ClearRegistry bool   `json:"clear_registry"`
		// Env — .env compose-стека (action: compose); пусто — не менять.
		Env      string `json:"env"`
		ClearEnv bool   `json:"clear_env"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var gitEnc, regEnc []byte
	switch {
	case req.ClearGit:
		gitEnc = []byte{}
	case req.GitToken != "" || req.SSHKey != "":
		raw, _ := json.Marshal(deploy.Cred{Token: strings.TrimSpace(req.GitToken), SSHKey: strings.TrimSpace(req.SSHKey)})
		enc, err := secretbox.Encrypt(s.hub.key, raw)
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		gitEnc = enc
	}
	switch {
	case req.ClearRegistry:
		regEnc = []byte{}
	case req.Registry != "":
		if !strings.Contains(req.Registry, ":") {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "deploy.registryCredFormat"))
			return
		}
		enc, err := secretbox.Encrypt(s.hub.key, []byte(strings.TrimSpace(req.Registry)))
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		regEnc = enc
	}
	err := s.db.SetPipelineSecrets(r.Context(), p.ID, nil, gitEnc, regEnc)
	if err == nil && (req.ClearEnv || req.Env != "") {
		// Каждая смена .env — версия: откат выкладки может вернуть и её.
		var env *string
		note := msgs.Tc(r.Context(), "deploy.envVersionCleared")
		if !req.ClearEnv {
			env, note = &req.Env, msgs.Tc(r.Context(), "deploy.envVersionEdited")
		}
		err = s.setPipelineEnv(r.Context(), p, auth.Username(r.Context()), note, env)
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "pipeline.credentials", p.Name, auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePipelineHookSecret — POST /hub/pipelines/{id}/hook-secret {rotate}:
// секрет подписи вебхука (показ — в аудит), по rotate — новый.
func (s *Server) handlePipelineHookSecret(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		Rotate bool `json:"rotate"`
	}
	_ = decodeJSON(r, &req)
	user := auth.Username(r.Context())
	if req.Rotate {
		enc, err := secretbox.Encrypt(s.hub.key, []byte(randomToken(32)))
		if err == nil {
			err = s.db.SetPipelineSecrets(r.Context(), p.ID, enc, nil, nil)
		}
		s.db.Audit(r.Context(), user, "pipeline.hook.rotate", p.Name, auditOutcome(err), "")
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		p, _ = s.db.PipelineByID(r.Context(), p.ID)
	}
	secret, err := secretbox.Decrypt(s.hub.key, p.HookSecret)
	s.db.Audit(r.Context(), user, "pipeline.hook.reveal", p.Name, auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hook_id": p.HookID, "secret": string(secret)})
}

// handlePipelineDeploy — POST /hub/pipelines/{id}/deploy {ref, tag}:
// выложить сейчас (пусто — ветка из описания).
func (s *Server) handlePipelineDeploy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		Ref string `json:"ref"`
		Tag string `json:"tag"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if (req.Ref != "" && !deploy.ValidRef(req.Ref)) || (req.Tag != "" && !deploy.ValidRef(req.Tag)) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "deploy.specBad", "ref", req.Ref+req.Tag))
		return
	}
	user := auth.Username(r.Context())
	d, err := s.startDeployment(r.Context(), p, store.Deployment{Ref: req.Ref, Tag: req.Tag, Trigger: "manual", Author: user}, false)
	s.db.Audit(r.Context(), user, "pipeline.deploy", p.Name, auditOutcome(err), map[string]any{"ref": req.Ref, "tag": req.Tag, "job_id": d.JobID})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployment_id": d.ID, "job_id": d.JobID})
}

// handlePipelineRollback — POST /hub/pipelines/{id}/rollback {deployment_id}:
// выложить заново коммит и тег прошлой удачной выкладки.
func (s *Server) handlePipelineRollback(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		DeploymentID int64 `json:"deployment_id"`
		// WithEnv — вернуть и .env, с которым шла та выкладка.
		WithEnv bool `json:"with_env"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	prev, err := s.db.DeploymentByID(r.Context(), req.DeploymentID)
	if err != nil || prev.PipelineID != p.ID || prev.Status != store.DeploySucceeded || !deploy.ValidSHA(prev.Commit) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "deploy.rollbackBad"))
		return
	}
	user := auth.Username(r.Context())
	envRestored := false
	if req.WithEnv && prev.EnvVersion > 0 && prev.EnvVersion != s.db.LatestEnvVersion(r.Context(), p.ID) {
		v, err := s.db.EnvVersionByID(r.Context(), prev.EnvVersion)
		if err == nil {
			err = s.restoreEnvVersion(r.Context(), p, v, user, msgs.Tc(r.Context(), "deploy.envVersionRollback", prev.ID))
		}
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		envRestored = true
		if p, err = s.db.PipelineByID(r.Context(), p.ID); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
	}
	d, err := s.startDeployment(r.Context(), p, store.Deployment{Ref: prev.Ref, Commit: prev.Commit, Tag: prev.Tag, Trigger: "rollback", Author: user}, false)
	s.db.Audit(r.Context(), user, "pipeline.rollback", p.Name, auditOutcome(err), map[string]any{"to": prev.ID, "commit": prev.Commit, "env_restored": envRestored})
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployment_id": d.ID, "job_id": d.JobID})
}

// handlePipelineDeployments — GET /hub/pipelines/{id}/deployments.
func (s *Server) handlePipelineDeployments(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	list, err := s.db.Deployments(r.Context(), p.ID, 100)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": list})
}
