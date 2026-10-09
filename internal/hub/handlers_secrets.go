package hub

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
)

// «Секреты» конвейера: ключи репозиториев (основной и другие — submodules,
// по началу адреса), ключи registry (адрес, логин, токен, CA) и .env (тот
// же handlePipelineCredentials). Значения наружу не отдаются — только вид
// ключа и последние четыре знака токена.

// secretsRepo — ключ репозитория в списке. Prefix пуст — основной.
type secretsRepo struct {
	Prefix string `json:"prefix"`
	Kind   string `json:"kind"` // token | ssh
	Hint   string `json:"hint,omitempty"`
}

// secretsRegistry — ключ registry в списке.
type secretsRegistry struct {
	Host string `json:"host"`
	User string `json:"user"`
	Hint string `json:"hint,omitempty"`
	CA   bool   `json:"ca"`
}

func credKind(c deploy.Cred) (string, string) {
	if c.SSHKey != "" {
		return "ssh", ""
	}
	return "token", deploy.TokenHint(c.Token)
}

func tokenTail(t string) string {
	if len(t) < 8 {
		return ""
	}
	return t[len(t)-4:]
}

// handlePipelineSecrets — GET /hub/pipelines/{id}/secrets.
func (s *Server) handlePipelineSecrets(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	repos := []secretsRepo{}
	if main := s.pipelineCred(p); main.Token != "" || main.SSHKey != "" {
		kind, hint := credKind(main)
		repos = append(repos, secretsRepo{Kind: kind, Hint: hint})
	}
	for _, c := range s.pipelineRepoCreds(p) {
		kind, hint := credKind(c.Cred)
		repos = append(repos, secretsRepo{Prefix: c.Prefix, Kind: kind, Hint: hint})
	}
	regs := []secretsRegistry{}
	for _, k := range s.pipelineRegistries(p) {
		regs = append(regs, secretsRegistry{Host: k.Host, User: k.User, Hint: tokenTail(k.Token), CA: k.CA != ""})
	}
	repo := ""
	if spec, err := deploy.ParseSpec(p.Content); err == nil {
		repo = spec.Repo
	}
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "repos": repos, "registries": regs, "has_env": p.HasEnv})
}

// prefixRe — префикс ключа репозитория: «хост[/путь]» после нормализации.
var prefixRe = regexp.MustCompile(`^[a-z0-9.-]+(/[A-Za-z0-9._~/-]*)?$`)

// handlePipelineSecretRepo — PUT/DELETE /hub/pipelines/{id}/secrets/repo:
// ключ основного репозитория (prefix пуст) или по началу адреса.
func (s *Server) handlePipelineSecretRepo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		Prefix string `json:"prefix"`
		Token  string `json:"token"`
		SSHKey string `json:"ssh_key"`
	}
	if r.Method == http.MethodDelete {
		req.Prefix = r.URL.Query().Get("prefix")
	} else if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	prefix := ""
	if strings.TrimSpace(req.Prefix) != "" {
		prefix = deploy.NormalizePrefix(req.Prefix)
		if !prefixRe.MatchString(strings.TrimSuffix(prefix, "/")) {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "deploy.secretBadPrefix", req.Prefix))
			return
		}
	}
	cred := deploy.Cred{Token: strings.TrimSpace(req.Token), SSHKey: strings.TrimSpace(req.SSHKey)}
	del := r.Method == http.MethodDelete
	if !del && cred.Token == "" && cred.SSHKey == "" {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "deploy.secretEmpty"))
		return
	}
	var err error
	if prefix == "" {
		var enc []byte = []byte{}
		if !del {
			raw, _ := json.Marshal(cred)
			if enc, err = secretbox.Encrypt(s.hub.key, raw); err != nil {
				writeErr(w, r, http.StatusInternalServerError, err)
				return
			}
		}
		err = s.db.SetPipelineSecrets(r.Context(), p.ID, nil, enc, nil)
	} else {
		list := s.pipelineRepoCreds(p)
		out := list[:0]
		for _, c := range list {
			if deploy.NormalizePrefix(c.Prefix) != prefix {
				out = append(out, c)
			}
		}
		if !del {
			out = append(out, deploy.RepoCred{Prefix: prefix, Cred: cred})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
		var enc []byte
		if len(out) > 0 {
			raw, _ := json.Marshal(out)
			if enc, err = secretbox.Encrypt(s.hub.key, raw); err != nil {
				writeErr(w, r, http.StatusInternalServerError, err)
				return
			}
		}
		err = s.db.SetPipelineRepoCreds(r.Context(), p.ID, enc)
	}
	action := "pipeline.secret_repo"
	if del {
		action = "pipeline.secret_repo_delete"
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), action, p.Name, auditOutcome(err), map[string]any{"prefix": prefix})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePipelineSecretRegistry — PUT/DELETE /hub/pipelines/{id}/secrets/registry:
// ключ registry по адресу. При правке пустой токен — прежний; ca —
// заменить (пусто вместе с clear_ca — убрать).
func (s *Server) handlePipelineSecretRegistry(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	var req struct {
		Host    string `json:"host"`
		OldHost string `json:"old_host"`
		User    string `json:"user"`
		Token   string `json:"token"`
		CA      string `json:"ca"`
		ClearCA bool   `json:"clear_ca"`
	}
	del := r.Method == http.MethodDelete
	if del {
		req.Host = r.URL.Query().Get("host")
		req.OldHost = req.Host
	} else if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	host := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(req.Host, "https://"), "http://")))
	host = strings.TrimSuffix(host, "/")
	if !del && !registryHostRe.MatchString(host) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "deploy.secretBadRegistry", req.Host))
		return
	}
	user, token, ca := strings.TrimSpace(req.User), strings.TrimSpace(req.Token), strings.TrimSpace(req.CA)
	if !del && (user == "" || strings.ContainsAny(user, ": \t\r\n") || strings.ContainsAny(token, "\r\n")) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "deploy.registryCredFormat"))
		return
	}
	if ca != "" {
		if _, err := (deploy.RegistryAccess{CA: ca}).Client(); err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
	}
	list := s.pipelineRegistries(p)
	var prev *registryKey
	out := []registryKey{}
	for i := range list {
		if list[i].Host == req.OldHost || (!del && list[i].Host == host) {
			if prev == nil {
				k := list[i]
				prev = &k
			}
			continue
		}
		out = append(out, list[i])
	}
	if !del {
		k := registryKey{Host: host, User: user, Token: token, CA: ca}
		if prev != nil {
			if k.Token == "" {
				k.Token = prev.Token
			}
			if k.CA == "" && !req.ClearCA {
				k.CA = prev.CA
			}
		}
		if k.Token == "" {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "deploy.secretEmpty"))
			return
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	var enc []byte
	var err error
	if len(out) > 0 {
		raw, _ := json.Marshal(out)
		if enc, err = secretbox.Encrypt(s.hub.key, raw); err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
	}
	err = s.db.SetPipelineRegistries(r.Context(), p.ID, enc)
	action := "pipeline.secret_registry"
	if del {
		action = "pipeline.secret_registry_delete"
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), action, p.Name, auditOutcome(err), map[string]any{"host": host})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePipelineSecretsCheck — POST /hub/pipelines/{id}/secrets/check:
// вход в каждый registry его ключом (основной репозиторий проверяет
// access/check).
func (s *Server) handlePipelineSecretsCheck(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	type result struct {
		Host  string `json:"host"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	out := []result{}
	for _, k := range s.pipelineRegistries(p) {
		if k.Host == "" {
			continue
		}
		res := result{Host: k.Host, OK: true}
		if err := deploy.RegistryLogin(r.Context(), k.Host, k.access()); err != nil {
			res.OK, res.Error = false, strings.TrimSpace(msgs.Localize(msgs.FromContext(r.Context()), err))
		}
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"registries": out})
}
