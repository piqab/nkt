package hub

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Ключи registry конвейера — списком «адрес registry → логин, токен, CA»
// («Секреты» → «Реестры»): каждый образ стека проверяется в сухом прогоне
// (с хаба) и скачивается при выкладке (на хосте, временным конфигом
// Docker или authfile Podman) ключом своего registry; публичные — без
// ключа. Тем же ключом хаб следит за тегами (registry:). Свой CA registry
// хаб использует при проверке, хост кладёт его в certs.d движка.

// registryKey — ключ одного registry. Host пуст только у прежнего
// одиночного ключа (до списка): он применяется к registry из registry:
// или к единственному registry стека кроме Docker Hub.
type registryKey struct {
	Host  string `json:"host"`
	User  string `json:"user"`
	Token string `json:"token"`
	CA    string `json:"ca,omitempty"`
}

func (r registryKey) access() deploy.RegistryAccess {
	return deploy.RegistryAccess{Cred: r.User + ":" + r.Token, CA: r.CA}
}

// pipelineRegistries — ключи registry конвейера (расшифрованные).
func (s *Server) pipelineRegistries(pl store.Pipeline) []registryKey {
	var out []registryKey
	if len(pl.RegistriesEnc) > 0 {
		if raw, err := secretbox.Decrypt(s.hub.key, pl.RegistriesEnc); err == nil {
			_ = json.Unmarshal(raw, &out)
		}
		return out
	}
	if len(pl.RegistryCred) > 0 {
		if raw, err := secretbox.Decrypt(s.hub.key, pl.RegistryCred); err == nil {
			if user, token, ok := strings.Cut(string(raw), ":"); ok && user != "" && token != "" {
				out = append(out, registryKey{User: user, Token: token})
			}
		}
	}
	return out
}

// pipelineRepoCreds — ключи других репозиториев (submodules).
func (s *Server) pipelineRepoCreds(pl store.Pipeline) []deploy.RepoCred {
	var out []deploy.RepoCred
	if len(pl.RepoCredsEnc) > 0 {
		if raw, err := secretbox.Decrypt(s.hub.key, pl.RepoCredsEnc); err == nil {
			_ = json.Unmarshal(raw, &out)
		}
	}
	return out
}

// pipelineGit — git конвейера: ключ основного репозитория и ключи других
// (submodules).
func (s *Server) pipelineGit(pl store.Pipeline, dir string) deploy.Git {
	if dir == "" {
		dir = s.pipelineDir(pl.ID)
	}
	return deploy.Git{Dir: dir, Cred: s.pipelineCred(pl), Extra: s.pipelineRepoCreds(pl)}
}

// registryKeyFor — ключ registry host среди ключей конвейера; прежний
// одиночный ключ без адреса — если правило старого вида его сюда относит
// (unbound — к какому registry он относится, "" — ни к какому).
func registryKeyFor(keys []registryKey, host, unbound string) (registryKey, bool) {
	for _, k := range keys {
		if k.Host == host {
			return k, true
		}
	}
	for _, k := range keys {
		if k.Host == "" && unbound == host {
			k.Host = host
			return k, true
		}
	}
	return registryKey{}, false
}

// unboundRegistry — к какому registry относится прежний одиночный ключ:
// из registry: описания, без него — единственный registry стека кроме
// Docker Hub (все с Docker Hub — к нему).
func unboundRegistry(spec deploy.Spec, images []string) string {
	if spec.Registry != "" {
		return deploy.RegistryHost(spec.Registry)
	}
	hosts := map[string]bool{}
	for _, img := range images {
		if h := deploy.RegistryHost(img); h != "docker.io" {
			hosts[h] = true
		}
	}
	switch len(hosts) {
	case 0:
		if len(images) > 0 {
			return "docker.io"
		}
	case 1:
		for h := range hosts {
			return h
		}
	}
	return ""
}

// registriesFor — ключи для образов стека: по registry каждого образа.
func (s *Server) registriesFor(pl store.Pipeline, spec deploy.Spec, images []string) map[string]registryKey {
	keys := s.pipelineRegistries(pl)
	if len(keys) == 0 {
		return nil
	}
	unbound := unboundRegistry(spec, images)
	out := map[string]registryKey{}
	for _, img := range images {
		h := deploy.RegistryHost(img)
		if k, ok := registryKeyFor(keys, h, unbound); ok {
			out[h] = k
		}
	}
	return out
}

// registryList — ключи списком по адресу (для хоста).
func registryList(m map[string]registryKey) []registryKey {
	out := make([]registryKey, 0, len(m))
	for _, k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// unboundKeyLost — прежний одиночный ключ есть, а отнести его некуда.
func (s *Server) unboundKeyLost(pl store.Pipeline, spec deploy.Spec, images []string) bool {
	for _, k := range s.pipelineRegistries(pl) {
		if k.Host == "" && unboundRegistry(spec, images) == "" {
			return true
		}
	}
	return false
}

// unusedRegistryKeys — адреса ключей, которые не подходят ни одному образу
// стека и не нужны registry: (опечатка в адресе ключа или в имени образа).
func unusedRegistryKeys(keys []registryKey, spec deploy.Spec, images []string) []string {
	used := map[string]bool{}
	for _, img := range images {
		used[deploy.RegistryHost(img)] = true
	}
	if spec.Registry != "" {
		used[deploy.RegistryHost(spec.Registry)] = true
	}
	var out []string
	for _, k := range keys {
		if k.Host != "" && !used[k.Host] {
			out = append(out, k.Host)
		}
	}
	return out
}

// imageRegistries — registry образов стека (для подсказки).
func imageRegistries(images []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, img := range images {
		if h := deploy.RegistryHost(img); !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// recheckImages — образы, которые хост проверить не смог (закрытый
// registry без входа на хосте), проверяются с хаба ключом их registry.
func recheckImages(ctx context.Context, res *composeCheck, keys map[string]registryKey) {
	for i := range res.Images {
		img := &res.Images[i]
		if img.State == "registry" || img.State == "local" || img.State == "missing" || img.ArchMismatch {
			continue
		}
		k, ok := keys[deploy.RegistryHost(img.Image)]
		if !ok {
			continue
		}
		found, err := deploy.ManifestExists(ctx, img.Image, k.access())
		switch {
		case err != nil:
			img.Detail = err.Error()
		case found:
			img.State = "registry-key"
		default:
			img.State, img.Detail = "missing", "key"
		}
	}
}

// orDash — «—» вместо пустой строки в журнале.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
