package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Порты образа (EXPOSE) из registry без скачивания: манифест под
// архитектуру хоста и маленький файл конфигурации образа, слоёв не трогаем.
// Нужно сухому прогону: образ на хосте ещё не скачан, а проверить
// site.port хочется до выкладки. Вход — анонимный (публичные образы Docker
// Hub, ghcr.io, quay.io…); не вышло — порты просто неизвестны.

// imageRef — разобранная ссылка на образ.
type imageRef struct {
	Registry  string // хост registry (registry-1.docker.io для Docker Hub)
	Repo      string // library/nginx
	Reference string // тег или дайджест
}

var (
	registryHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)
	repoRe         = regexp.MustCompile(`^[a-z0-9]+([._/-][a-z0-9]+)*$`)
	tagRe          = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestRe       = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// parseImageRef — ссылка docker: [registry/]repo[:tag][@digest]; без
// registry — Docker Hub, однословное имя — library/.
func parseImageRef(ref string) (imageRef, error) {
	r := imageRef{Registry: "registry-1.docker.io", Reference: "latest"}
	name := ref
	if i := strings.Index(name, "@"); i >= 0 {
		r.Reference, name = name[i+1:], name[:i]
		if !digestRe.MatchString(r.Reference) {
			return r, fmt.Errorf("bad digest in %q", ref)
		}
	} else if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		r.Reference, name = name[i+1:], name[:i]
		if !tagRe.MatchString(r.Reference) {
			return r, fmt.Errorf("bad tag in %q", ref)
		}
	}
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.Registry, name = first, rest
		if r.Registry == "docker.io" || r.Registry == "index.docker.io" {
			r.Registry = "registry-1.docker.io"
		}
	}
	if r.Registry == "registry-1.docker.io" && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	if !registryHostRe.MatchString(r.Registry) || !repoRe.MatchString(name) {
		return r, fmt.Errorf("bad image reference %q", ref)
	}
	r.Repo = name
	return r, nil
}

var bearerParamRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// registryClient — запросы к одному registry с анонимным токеном.
type registryClient struct {
	http  *http.Client
	ref   imageRef
	token string
}

func (rc *registryClient) get(ctx context.Context, path string, accept []string, limit int64) (*http.Response, []byte, error) {
	u := url.URL{Scheme: "https", Host: rc.ref.Registry, Path: path}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, nil, err
		}
		for _, a := range accept {
			req.Header.Add("Accept", a)
		}
		if rc.token != "" {
			req.Header.Set("Authorization", "Bearer "+rc.token)
		}
		resp, err := rc.http.Do(req)
		if err != nil {
			return nil, nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
		resp.Body.Close()
		if err != nil {
			return nil, nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && rc.token == "" && attempt == 0 {
			if err := rc.login(ctx, resp.Header.Get("WWW-Authenticate")); err != nil {
				return nil, nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("registry %s: HTTP %d", rc.ref.Registry, resp.StatusCode)
		}
		return resp, body, nil
	}
	return nil, nil, errors.New("registry: unauthorized")
}

// login — анонимный токен по вызову Bearer (realm, service, scope).
func (rc *registryClient) login(ctx context.Context, challenge string) error {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return errors.New("registry: no bearer challenge")
	}
	params := map[string]string{}
	for _, m := range bearerParamRe.FindAllStringSubmatch(challenge, -1) {
		params[strings.ToLower(m[1])] = m[2]
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Scheme != "https" || realm.Host == "" {
		return errors.New("registry: bad token realm")
	}
	q := realm.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	q.Set("scope", "repository:"+rc.ref.Repo+":pull")
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return err
	}
	resp, err := rc.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry token: HTTP %d", resp.StatusCode)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&tok); err != nil {
		return err
	}
	rc.token = tok.Token
	if rc.token == "" {
		rc.token = tok.AccessToken
	}
	if rc.token == "" {
		return errors.New("registry: empty token")
	}
	return nil
}

// manifestEntry — манифест платформы в списке (index).
type manifestEntry struct {
	Digest   string `json:"digest"`
	Platform struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}

var manifestAccept = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}

// imageExposedPorts — порты EXPOSE образа для архитектуры arch (amd64,
// arm64…; пусто — amd64) по данным registry.
func imageExposedPorts(ctx context.Context, client *http.Client, ref, arch string) ([]int, error) {
	r, err := parseImageRef(ref)
	if err != nil {
		return nil, err
	}
	if arch == "" {
		arch = "amd64"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rc := &registryClient{http: client, ref: r}
	reference := r.Reference
	for depth := 0; depth < 2; depth++ {
		_, body, err := rc.get(ctx, "/v2/"+r.Repo+"/manifests/"+reference, manifestAccept, 4<<20)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Manifests []manifestEntry `json:"manifests"`
			Config    struct {
				Digest string `json:"digest"`
			} `json:"config"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, err
		}
		if len(doc.Manifests) > 0 {
			// Список под платформы — берём манифест архитектуры хоста.
			i := slices.IndexFunc(doc.Manifests, func(m manifestEntry) bool {
				return m.Platform.Architecture == arch && (m.Platform.OS == "" || m.Platform.OS == "linux")
			})
			if i < 0 || !digestRe.MatchString(doc.Manifests[i].Digest) {
				return nil, fmt.Errorf("registry: no %s manifest for %s", arch, ref)
			}
			reference = doc.Manifests[i].Digest
			continue
		}
		if !digestRe.MatchString(doc.Config.Digest) {
			return nil, fmt.Errorf("registry: no config for %s", ref)
		}
		_, cfgBody, err := rc.get(ctx, "/v2/"+r.Repo+"/blobs/"+doc.Config.Digest, nil, 4<<20)
		if err != nil {
			return nil, err
		}
		var cfg struct {
			Config struct {
				ExposedPorts map[string]any `json:"ExposedPorts"`
			} `json:"config"`
		}
		if err := json.Unmarshal(cfgBody, &cfg); err != nil {
			return nil, err
		}
		var ports []int
		for k := range cfg.Config.ExposedPorts {
			if n, err := strconv.Atoi(strings.SplitN(k, "/", 2)[0]); err == nil && !slices.Contains(ports, n) {
				ports = append(ports, n)
			}
		}
		sort.Ints(ports)
		return ports, nil
	}
	return nil, fmt.Errorf("registry: nested index for %s", ref)
}
