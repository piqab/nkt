package deploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Слежение за registry: список тегов образа по протоколу OCI Distribution
// (GET /v2/<образ>/tags/list), с получением токена по вызову
// WWW-Authenticate: Bearer (GHCR, Docker Hub, GitLab, Harbor) или
// базовой авторизацией. Доступ — «логин:токен» или анонимно.

// DefaultRegistryTags — теги-версии, если шаблон не задан.
const DefaultRegistryTags = `^v?\d+(\.\d+){1,2}$`

var registryClient = &http.Client{Timeout: 20 * time.Second}

// RegistryAccess — вход в registry: «логин:токен» (пусто — анонимно) и
// свой CA в PEM (пусто — системные корни), если у registry сертификат
// своего центра (Harbor в своей сети).
type RegistryAccess struct {
	Cred string
	CA   string
}

// Client — HTTP-клиент с доверием к CA registry (вдобавок к системным).
func (a RegistryAccess) Client() (*http.Client, error) {
	if strings.TrimSpace(a.CA) == "" {
		return registryClient, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM([]byte(a.CA)) {
		return nil, msgs.Errorf("deploy.registryBadCA")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	return &http.Client{Timeout: registryClient.Timeout, Transport: tr}, nil
}

// registryBase — адрес registry и имя образа: docker.io и короткие имена —
// Docker Hub (library/ для одиночных).
func registryBase(image string) (string, string) {
	first, rest, ok := strings.Cut(image, "/")
	if !ok {
		return "https://registry-1.docker.io", "library/" + image
	}
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		if first == "docker.io" {
			if !strings.Contains(rest, "/") {
				rest = "library/" + rest
			}
			return "https://registry-1.docker.io", rest
		}
		return "https://" + first, rest
	}
	return "https://registry-1.docker.io", image
}

var bearerParamRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// ListTags — теги образа (до 10 страниц).
func ListTags(ctx context.Context, image string, acc RegistryAccess) ([]string, error) {
	client, err := acc.Client()
	if err != nil {
		return nil, err
	}
	cred := acc.Cred
	base, repo := registryBase(image)
	next := base + "/v2/" + repo + "/tags/list?n=1000"
	var tags []string
	auth := ""
	for page := 0; page < 10 && next != ""; page++ {
		resp, err := registryGet(ctx, client, next, auth)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && auth == "" {
			challenge := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			auth, err = registryAuth(ctx, client, challenge, cred)
			if err != nil {
				return nil, err
			}
			resp, err = registryGet(ctx, client, next, auth)
			if err != nil {
				return nil, err
			}
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, msgs.Errorf("deploy.registryHTTP", image, resp.StatusCode)
		}
		var body struct {
			Tags []string `json:"tags"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, msgs.Errorf("deploy.registryHTTP", image, err.Error())
		}
		tags = append(tags, body.Tags...)
		next = ""
		if m := regexp.MustCompile(`<([^>]+)>;\s*rel="next"`).FindStringSubmatch(link); m != nil {
			if u, err := url.Parse(m[1]); err == nil {
				next = strings.TrimSuffix(base, "/") + u.RequestURI()
			}
		}
	}
	return tags, nil
}

func registryGet(ctx context.Context, client *http.Client, u, auth string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return client.Do(req)
}

// registryAuth — заголовок Authorization по вызову сервера.
func registryAuth(ctx context.Context, client *http.Client, challenge, cred string) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	user, pass, hasCred := strings.Cut(cred, ":")
	switch strings.ToLower(scheme) {
	case "basic":
		if !hasCred {
			return "", msgs.Errorf("deploy.registryAuth")
		}
		req, _ := http.NewRequest(http.MethodGet, "https://x", nil)
		req.SetBasicAuth(user, pass)
		return req.Header.Get("Authorization"), nil
	case "bearer":
		p := map[string]string{}
		for _, m := range bearerParamRe.FindAllStringSubmatch(params, -1) {
			p[m[1]] = m[2]
		}
		realm, err := url.Parse(p["realm"])
		if err != nil || realm.Scheme != "https" {
			return "", msgs.Errorf("deploy.registryAuth")
		}
		q := realm.Query()
		if p["service"] != "" {
			q.Set("service", p["service"])
		}
		if p["scope"] != "" {
			q.Set("scope", p["scope"])
		}
		realm.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
		if err != nil {
			return "", err
		}
		if hasCred {
			req.SetBasicAuth(user, pass)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", msgs.Errorf("deploy.registryHTTP", realm.Host, resp.StatusCode)
		}
		var tok struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
			return "", err
		}
		t := tok.Token
		if t == "" {
			t = tok.AccessToken
		}
		if t == "" {
			return "", msgs.Errorf("deploy.registryAuth")
		}
		return "Bearer " + t, nil
	}
	return "", msgs.Errorf("deploy.registryAuth")
}

// NewestTag — самый новый тег, подходящий под шаблон (сравнение по
// числам в теге: v1.10.0 новее v1.9.3).
func NewestTag(tags []string, pattern string) string {
	if pattern == "" {
		pattern = DefaultRegistryTags
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	var ok []string
	for _, t := range tags {
		if re.MatchString(t) && ValidRef(t) {
			ok = append(ok, t)
		}
	}
	if len(ok) == 0 {
		return ""
	}
	sort.SliceStable(ok, func(i, j int) bool { return CompareVersions(ok[i], ok[j]) < 0 })
	return ok[len(ok)-1]
}

var numRe = regexp.MustCompile(`\d+`)

// CompareVersions — сравнение по числам в строке, затем по самой строке.
func CompareVersions(a, b string) int {
	na, nb := numRe.FindAllString(a, -1), numRe.FindAllString(b, -1)
	for i := 0; i < len(na) && i < len(nb); i++ {
		x, _ := strconv.Atoi(na[i])
		y, _ := strconv.Atoi(nb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	if len(na) != len(nb) {
		if len(na) < len(nb) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// RegistryHost — хост registry образа («ghcr.io», «docker.io» для Docker
// Hub и коротких имён): к какому registry относится ключ конвейера.
func RegistryHost(image string) string {
	first, _, ok := strings.Cut(image, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") && first != "docker.io" {
		return first
	}
	return "docker.io"
}

// manifestAccept — индексы и манифесты, OCI и Docker: HEAD отвечает на
// любой из них.
const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

// ManifestExists — есть ли образ (тег или дайджест) в registry: HEAD
// манифеста, без скачивания слоёв; вход — как у ListTags.
func ManifestExists(ctx context.Context, image string, acc RegistryAccess) (bool, error) {
	client, err := acc.Client()
	if err != nil {
		return false, err
	}
	cred := acc.Cred
	name, ref := image, "latest"
	if at := strings.LastIndex(name, "@"); at > 0 {
		name, ref = name[:at], name[at+1:]
	} else if colon := strings.LastIndex(name, ":"); colon > strings.LastIndex(name, "/") {
		name, ref = name[:colon], name[colon+1:]
	}
	base, repo := registryBase(name)
	u := base + "/v2/" + repo + "/manifests/" + ref
	head := func(auth string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", manifestAccept)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return client.Do(req)
	}
	resp, err := head("")
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		auth, err := registryAuth(ctx, client, resp.Header.Get("WWW-Authenticate"), cred)
		if err != nil {
			return false, err
		}
		if resp, err = head(auth); err != nil {
			return false, err
		}
		resp.Body.Close()
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, msgs.Errorf("deploy.registryHTTP", image, resp.StatusCode)
}

// RegistryLogin — вход в registry host этим доступом: GET /v2/, по вызову
// — токен (Bearer) или базовая авторизация; nil — вход есть.
func RegistryLogin(ctx context.Context, host string, acc RegistryAccess) error {
	client, err := acc.Client()
	if err != nil {
		return err
	}
	base := "https://" + host
	if host == "docker.io" {
		base = "https://registry-1.docker.io"
	}
	resp, err := registryGet(ctx, client, base+"/v2/", "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil // registry без входа
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return msgs.Errorf("deploy.registryHTTP", host, resp.StatusCode)
	}
	auth, err := registryAuth(ctx, client, resp.Header.Get("WWW-Authenticate"), acc.Cred)
	if err != nil {
		return err
	}
	resp, err = registryGet(ctx, client, base+"/v2/", auth)
	if err != nil {
		return err
	}
	resp.Body.Close()
	// Docker Hub на /v2/ с токеном без scope отвечает 401 — токен выдан,
	// значит логин и пароль приняты.
	if resp.StatusCode == http.StatusOK || (resp.StatusCode == http.StatusUnauthorized && strings.HasPrefix(auth, "Bearer ")) {
		return nil
	}
	return msgs.Errorf("deploy.registryHTTP", host, resp.StatusCode)
}
