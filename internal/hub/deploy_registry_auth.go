package hub

import (
	"context"
	"strings"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Ключ registry конвейера («логин:токен» из «Доступа») — не только для
// слежения за тегами: им же сухой прогон проверяет закрытый образ (с хаба),
// а выкладка скачивает его на хосте (docker --config с временным файлом,
// см. api.composeDeployRequest.RegistryAuth). Хосту не нужен свой
// docker login.

// pipelineRegistry — ключ и registry, к которому он относится.
type pipelineRegistry struct {
	Host  string `json:"host"`
	User  string `json:"user"`
	Token string `json:"token"`
}

func (r pipelineRegistry) cred() string { return r.User + ":" + r.Token }

// registryFor — ключ конвейера для образов images. Registry — из
// registry: описания, а без него — единственный registry, из которого стек
// берёт образы: ключ одного registry не уходит в другой.
func (s *Server) registryFor(pl store.Pipeline, spec deploy.Spec, images []string) (pipelineRegistry, bool) {
	if len(pl.RegistryCred) == 0 {
		return pipelineRegistry{}, false
	}
	raw, err := secretbox.Decrypt(s.hub.key, pl.RegistryCred)
	if err != nil {
		return pipelineRegistry{}, false
	}
	user, token, ok := strings.Cut(string(raw), ":")
	if !ok || user == "" || token == "" {
		return pipelineRegistry{}, false
	}
	host := ""
	if spec.Registry != "" {
		host = deploy.RegistryHost(spec.Registry)
	} else {
		// Публичные образы Docker Hub рядом с закрытым из своего registry —
		// обычное дело: ключ — к единственному registry кроме Docker Hub,
		// а если все образы с Docker Hub — к нему.
		hosts := map[string]bool{}
		for _, img := range images {
			if h := deploy.RegistryHost(img); h != "docker.io" {
				hosts[h] = true
			}
		}
		switch len(hosts) {
		case 0:
			if len(images) == 0 {
				return pipelineRegistry{}, false
			}
			host = "docker.io"
		case 1:
			for h := range hosts {
				host = h
			}
		default:
			return pipelineRegistry{}, false
		}
	}
	return pipelineRegistry{Host: host, User: user, Token: token}, true
}

// recheckImages — образы, которые хост проверить не смог (закрытый registry
// без входа на хосте), проверяются с хаба ключом конвейера.
func recheckImages(ctx context.Context, res *composeCheck, reg pipelineRegistry) {
	for i := range res.Images {
		img := &res.Images[i]
		if img.State == "registry" || img.State == "local" || img.State == "missing" || img.ArchMismatch {
			continue
		}
		if deploy.RegistryHost(img.Image) != reg.Host {
			continue
		}
		found, err := deploy.ManifestExists(ctx, img.Image, reg.cred())
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
