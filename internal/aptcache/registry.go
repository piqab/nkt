package aptcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Зеркало registry образов контейнеров (pull-through cache). containerd
// на хосте настраивается брать docker.io, quay.io, registry.k8s.io, ghcr.io
// и gcr.io с http://127.0.0.1:3142 — тот же проброс, что у кэша пакетов,
// — и добавляет к запросам ?ns=<исходный registry>, по которому зеркало
// понимает, куда идти. Слои и манифесты по digest неизменяемы и лежат
// бессрочно; «тег → digest» живёт tagTTL, а при недоступном источнике
// берётся устаревший — узел без интернета продолжит тянуть образы,
// которые хаб уже видел. Авторизация к источнику — анонимный Bearer по
// вызову WWW-Authenticate (Docker Hub, quay.io, ghcr.io так и работают).

const (
	tagTTL   = 5 * time.Minute
	tokenTTL = 4 * time.Minute
)

var digestRe = regexp.MustCompile(`^[a-z0-9]+:[a-f0-9]{32,128}$`)

// registryNSRe — допустимое имя registry в ?ns= (см. serveRegistry).
var registryNSRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)

// registryUpstream — адрес источника по имени registry из ?ns=.
func registryUpstream(ns string) string {
	switch ns {
	case "", "docker.io", "index.docker.io", "registry-1.docker.io":
		return "https://registry-1.docker.io"
	}
	return "https://" + ns
}

type registryToken struct {
	value string
	at    time.Time
}

// registryAuth — токены по вызову источника.
type registryAuth struct {
	mu     sync.Mutex
	tokens map[string]registryToken
}

func (c *Cache) serveRegistry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	p := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/v2"), "/")
	if p == "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		return
	}
	ns := r.URL.Query().Get("ns")
	// ns — имя registry (хост[:порт]): оно входит в путь кэша и в адрес
	// источника, поэтому только символы имени хоста — ни «..», ни «/».
	if ns != "" && !registryNSRe.MatchString(ns) {
		http.Error(w, "nkt registry mirror: bad ns", http.StatusBadRequest)
		return
	}
	upstream := registryUpstream(ns)
	if ns == "" || ns == "index.docker.io" || ns == "registry-1.docker.io" {
		ns = "docker.io"
	}
	var name, kind, ref string
	if i := strings.LastIndex(p, "/manifests/"); i > 0 {
		name, kind, ref = p[:i], "manifests", p[i+len("/manifests/"):]
	} else if i := strings.LastIndex(p, "/blobs/"); i > 0 {
		name, kind, ref = p[:i], "blobs", p[i+len("/blobs/"):]
	} else if strings.HasSuffix(p, "/tags/list") {
		c.registryPass(w, r, upstream+"/v2/"+p)
		return
	} else {
		http.Error(w, "nkt registry mirror: unsupported path", http.StatusNotFound)
		return
	}
	if strings.Contains(name, "..") || ref == "" || strings.ContainsAny(ref, "/ ") {
		http.Error(w, "bad reference", http.StatusBadRequest)
		return
	}
	if ns == "docker.io" && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	switch kind {
	case "blobs":
		if !digestRe.MatchString(ref) {
			http.Error(w, "bad digest", http.StatusBadRequest)
			return
		}
		c.serveBlob(w, r, upstream, name, ref)
	case "manifests":
		c.serveManifest(w, r, upstream, ns, name, ref)
	}
}

func digestRel(kind, digest string) string {
	algo, hex, _ := strings.Cut(digest, ":")
	return path.Join("registry", kind, algo, hex)
}

func (c *Cache) serveBlob(w http.ResponseWriter, r *http.Request, upstream, name, digest string) {
	rel := digestRel("blobs", digest)
	file := c.cacheFile(rel)
	if file == "" {
		http.Error(w, "nkt registry mirror: bad path", http.StatusBadRequest)
		return
	}
	if !c.fresh(rel, 0) {
		c.misses.Add(1)
		err := c.fetchWith(rel, func() error {
			resp, err := c.registryDo(context.Background(), http.MethodGet, upstream+"/v2/"+name+"/blobs/"+digest, "")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return &statusError{code: resp.StatusCode}
			}
			return c.store(rel, file, resp.Body, resp.ContentLength, "")
		})
		if err != nil {
			registryError(w, err)
			return
		}
	} else {
		c.hits.Add(1)
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Type", "application/octet-stream")
	c.serveFile(w, r, file)
}

func (c *Cache) serveManifest(w http.ResponseWriter, r *http.Request, upstream, ns, name, ref string) {
	accept := r.Header.Get("Accept")
	digest := ref
	if !digestRe.MatchString(ref) {
		// Тег: сначала digest — по HEAD к источнику (с тем же Accept, что
		// у клиента: список платформ или манифест одной), с коротким
		// сроком; при недоступном источнике — прошлое значение.
		sum := sha256.Sum256([]byte(accept))
		relTag := path.Join("registry", "tags", ns, name, ref+"."+hex.EncodeToString(sum[:4]))
		fileTag := c.cacheFile(relTag)
		if fileTag == "" {
			http.Error(w, "nkt registry mirror: bad path", http.StatusBadRequest)
			return
		}
		if !c.fresh(relTag, tagTTL) {
			err := c.fetchWith(relTag, func() error {
				resp, err := c.registryDo(context.Background(), http.MethodHead, upstream+"/v2/"+name+"/manifests/"+ref, accept)
				if err != nil {
					return err
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					return &statusError{code: resp.StatusCode}
				}
				d := resp.Header.Get("Docker-Content-Digest")
				if !digestRe.MatchString(d) {
					return errors.New("upstream manifest without digest")
				}
				return c.store(relTag, fileTag, strings.NewReader(d), int64(len(d)), "")
			})
			if err != nil && !c.has(relTag) {
				registryError(w, err)
				return
			}
		}
		raw, err := os.ReadFile(fileTag)
		if err != nil || !digestRe.MatchString(strings.TrimSpace(string(raw))) {
			http.Error(w, "nkt registry mirror: tag mapping unreadable", http.StatusBadGateway)
			return
		}
		digest = strings.TrimSpace(string(raw))
	}
	rel := digestRel("manifests", digest)
	file := c.cacheFile(rel)
	if file == "" {
		http.Error(w, "nkt registry mirror: bad path", http.StatusBadRequest)
		return
	}
	if !c.fresh(rel, 0) {
		c.misses.Add(1)
		err := c.fetchWith(rel, func() error {
			resp, err := c.registryDo(context.Background(), http.MethodGet, upstream+"/v2/"+name+"/manifests/"+digest, accept)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return &statusError{code: resp.StatusCode}
			}
			return c.store(rel, file, resp.Body, resp.ContentLength, resp.Header.Get("Content-Type"))
		})
		if err != nil {
			registryError(w, err)
			return
		}
	} else {
		c.hits.Add(1)
	}
	ct := "application/vnd.docker.distribution.manifest.v2+json"
	if raw, err := os.ReadFile(file + ".ct"); err == nil && strings.TrimSpace(string(raw)) != "" {
		ct = strings.TrimSpace(string(raw))
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Type", ct)
	c.serveFile(w, r, file)
}

// registryPass — без кэша (список тегов).
func (c *Cache) registryPass(w http.ResponseWriter, r *http.Request, u string) {
	resp, err := c.registryDo(r.Context(), r.Method, u, r.Header.Get("Accept"))
	if err != nil {
		registryError(w, err)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Link"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func registryError(w http.ResponseWriter, err error) {
	var se *statusError
	if errors.As(err, &se) {
		http.Error(w, se.Error(), se.code)
		return
	}
	http.Error(w, "nkt registry mirror: "+err.Error(), http.StatusBadGateway)
}

// registryDo — запрос к источнику с анонимным Bearer по вызову
// WWW-Authenticate (токен кэшируется на tokenTTL).
func (c *Cache) registryDo(ctx context.Context, method, u, accept string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	do := func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, u, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		req.Header.Set("User-Agent", "nkt-registry-mirror")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return c.client.Do(req)
	}
	host := ""
	if pu, err := url.Parse(u); err == nil {
		host = pu.Host
	}
	if tok := c.auth.get(host); tok != "" {
		resp, err := do(tok)
		if err != nil || resp.StatusCode != http.StatusUnauthorized {
			return wrapCancel(resp, err, cancel)
		}
		resp.Body.Close()
	}
	resp, err := do("")
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return wrapCancel(resp, nil, cancel)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	resp.Body.Close()
	tok, err := c.auth.fetch(ctx, c.client, host, challenge)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err = do(tok)
	return wrapCancel(resp, err, cancel)
}

// wrapCancel привязывает отмену контекста к закрытию тела ответа.
func wrapCancel(resp *http.Response, err error, cancel context.CancelFunc) (*http.Response, error) {
	if err != nil || resp == nil {
		cancel()
		return resp, err
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}

func (a *registryAuth) get(host string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, ok := a.tokens[host]; ok && time.Since(t.at) < tokenTTL {
		return t.value
	}
	return ""
}

// fetch получает анонимный токен по вызову вида
// Bearer realm="https://auth.docker.io/token",service="…",scope="…".
func (a *registryAuth) fetch(ctx context.Context, client *http.Client, host, challenge string) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", fmt.Errorf("unsupported auth challenge: %q", challenge)
	}
	fields := map[string]string{}
	for _, kv := range strings.Split(params, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if ok {
			fields[k] = strings.Trim(v, `"`)
		}
	}
	realm := fields["realm"]
	if realm == "" {
		return "", errors.New("auth challenge without realm")
	}
	q := url.Values{}
	if s := fields["service"]; s != "" {
		q.Set("service", s)
	}
	if s := fields["scope"]; s != "" {
		q.Set("scope", s)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &statusError{code: resp.StatusCode}
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	tok := body.Token
	if tok == "" {
		tok = body.AccessToken
	}
	if tok == "" {
		return "", errors.New("auth response without token")
	}
	a.mu.Lock()
	if a.tokens == nil {
		a.tokens = map[string]registryToken{}
	}
	a.tokens[host] = registryToken{value: tok, at: time.Now()}
	a.mu.Unlock()
	return tok, nil
}
