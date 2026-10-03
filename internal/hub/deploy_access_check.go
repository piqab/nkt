package hub

import (
	"net/http"
	"strings"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
)

// AccessCheck — проверка доступа конвейера: репозиторий (git ls-remote с
// ключами конвейера) и registry, если он задан в описании.
type AccessCheck struct {
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	RepoOK    bool   `json:"repo_ok"`
	RefFound  bool   `json:"ref_found"`
	RepoError string `json:"repo_error,omitempty"`
	// Registry — образ из registry: (пусто — не задан, не проверялся).
	Registry      string `json:"registry,omitempty"`
	RegistryOK    bool   `json:"registry_ok,omitempty"`
	RegistryTags  int    `json:"registry_tags,omitempty"`
	RegistryError string `json:"registry_error,omitempty"`
}

// handlePipelineAccessCheck — POST /hub/pipelines/{id}/access/check:
// доступ с сохранёнными ключами конвейера (окно «Доступ» вызывает его при
// открытии и после записи ключей).
func (s *Server) handlePipelineAccessCheck(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.pipelineFromReq(w, r)
	if !ok {
		return
	}
	spec, err := deploy.ParseSpec(pl.Content)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	res := AccessCheck{Repo: spec.Repo, Ref: spec.Ref}
	g := deploy.Git{Dir: s.pipelineDir(pl.ID), Cred: s.pipelineCred(pl)}
	refs, err := g.Remote(ctx, spec.Repo)
	if err != nil {
		res.RepoError = quietSSH(msgs.Localize(msgs.FromContext(ctx), err))
	} else {
		res.RepoOK = true
		_, res.RefFound = refs["refs/heads/"+spec.Ref]
		if !res.RefFound {
			_, res.RefFound = refs["refs/tags/"+spec.Ref]
		}
	}
	if spec.Registry != "" {
		res.Registry = spec.Registry
		cred := ""
		if len(pl.RegistryCred) > 0 {
			if raw, err := secretbox.Decrypt(s.hub.key, pl.RegistryCred); err == nil {
				cred = string(raw)
			}
		}
		tags, err := deploy.ListTags(ctx, spec.Registry, cred)
		if err != nil {
			res.RegistryError = strings.TrimSpace(msgs.Localize(msgs.FromContext(ctx), err))
		} else {
			res.RegistryOK, res.RegistryTags = true, len(tags)
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// quietSSH — без строк ssh о добавлении ключа хоста в known_hosts: к
// причине отказа они не относятся.
func quietSSH(text string) string {
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.Contains(l, "Permanently added") && strings.Contains(l, "known hosts") {
			if i := strings.Index(l, "Warning:"); i > 0 {
				keep = append(keep, strings.TrimSpace(l[:i]))
			}
			continue
		}
		keep = append(keep, l)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}
