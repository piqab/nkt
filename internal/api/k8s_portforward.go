package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
)

// Проброс порта пода или сервиса в браузер: kubectl port-forward на
// 127.0.0.1 хоста и обратный прокси nkt на него по адресу
// …/k8s/pf/<токен>/. Страница приложения отдаётся в песочнице CSP (без
// allow-same-origin): у неё непрозрачный origin, и сессией nkt она
// пользоваться не может — cookie SameSite=Lax её запросам не достаётся.
// Потому и доступ к пробросу — по токену в пути (случайные 128 бит), а не
// по сессии: его создаёт администратор, живёт он, пока им пользуются
// (30 минут простоя — и проброс закрывается), Referer наружу не уходит.

const pfIdle = 30 * time.Minute

type pfSession struct {
	Token     string    `json:"token"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Port      int       `json:"port"`
	User      string    `json:"user"`
	Created   time.Time `json:"created"`
	LastUsed  time.Time `json:"last_used"`
	local     int
	cmd       *exec.Cmd
	proxy     *httputil.ReverseProxy
}

type pfManager struct {
	mu       sync.Mutex
	sessions map[string]*pfSession
	started  bool
}

func (m *pfManager) get(token string) *pfSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[token]
	if s != nil {
		s.LastUsed = time.Now()
	}
	return s
}

func (m *pfManager) add(s *pfSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = map[string]*pfSession{}
	}
	m.sessions[s.Token] = s
	if !m.started {
		m.started = true
		go m.reap()
	}
}

func (m *pfManager) stop(token string) bool {
	m.mu.Lock()
	s := m.sessions[token]
	delete(m.sessions, token)
	m.mu.Unlock()
	if s == nil {
		return false
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return true
}

func (m *pfManager) list() []pfSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]pfSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	return out
}

// reap закрывает пробросы, которыми не пользуются.
func (m *pfManager) reap() {
	for {
		time.Sleep(time.Minute)
		var idle []string
		m.mu.Lock()
		for t, s := range m.sessions {
			if time.Since(s.LastUsed) > pfIdle || (s.cmd.ProcessState != nil) {
				idle = append(idle, t)
			}
		}
		m.mu.Unlock()
		for _, t := range idle {
			m.stop(t)
		}
	}
}

var pfForwardingRe = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) ->`)

// k8sTargetPorts — порты объекта из его спецификации.
func k8sTargetPorts(t k8s.Target) []int {
	var out []int
	add := func(v any) {
		if f, ok := v.(float64); ok && f > 0 {
			out = append(out, int(f))
		}
	}
	spec, _ := t.Item["spec"].(map[string]any)
	switch t.Kind {
	case "services":
		ports, _ := spec["ports"].([]any)
		for _, p := range ports {
			if pm, ok := p.(map[string]any); ok {
				add(pm["port"])
			}
		}
	case "pods":
		cs, _ := spec["containers"].([]any)
		for _, c := range cs {
			cm, _ := c.(map[string]any)
			ports, _ := cm["ports"].([]any)
			for _, p := range ports {
				if pm, ok := p.(map[string]any); ok {
					add(pm["containerPort"])
				}
			}
		}
	}
	return out
}

// handleK8sPortForwardStart — POST /k8s/portforward {kind, namespace, name, port}.
func (s *Server) handleK8sPortForwardStart(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return
	}
	var req struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Port      int    `json:"port"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.Kind != "pods" && req.Kind != "services" {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "k8s.badKind", req.Kind))
		return
	}
	m := s.k8sManager()
	t, err := m.Find(r.Context(), req.Kind, req.Namespace, req.Name)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	port := 0
	for _, p := range k8sTargetPorts(t) {
		if p == req.Port {
			port = p
		}
	}
	if port == 0 {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "k8s.pfBadPort", req.Port))
		return
	}
	prefix := "pod/"
	if t.Kind == "services" {
		prefix = "svc/"
	}
	argv, err := m.KubectlArgv(r.Context(), "port-forward", prefix+t.Name, "-n", t.Namespace, "--address", "127.0.0.1", "0:"+strconv.Itoa(port))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	localCh := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if mm := pfForwardingRe.FindStringSubmatch(sc.Text()); mm != nil {
				n, _ := strconv.Atoi(mm[1])
				localCh <- n
			}
		}
		_ = cmd.Wait()
		close(localCh)
	}()
	var local int
	select {
	case local = <-localCh:
	case <-time.After(20 * time.Second):
	}
	if local == 0 {
		_ = cmd.Process.Kill()
		writeError(w, http.StatusBadGateway, msgs.T(msgs.LangFromRequest(r), "k8s.pfFailed", strings.TrimSpace(stderr.String())))
		return
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	user := auth.Username(r.Context())
	sess := &pfSession{Token: hex.EncodeToString(buf), Kind: t.Kind, Namespace: t.Namespace, Name: t.Name, Port: port, User: user,
		Created: time.Now(), LastUsed: time.Now(), local: local, cmd: cmd}
	sess.proxy = pfProxy(local, port)
	s.pf.add(sess)
	s.db.Audit(r.Context(), user, "k8s.portforward", t.Kind+" "+t.Namespace+"/"+t.Name+":"+strconv.Itoa(port), "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"token": sess.Token, "path": "/k8s/pf/" + sess.Token + "/"})
}

// handleK8sPortForwardList — GET /k8s/portforward: открытые пробросы.
func (s *Server) handleK8sPortForwardList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"forwards": s.pf.list()})
}

// handleK8sPortForwardStop — DELETE /k8s/portforward/{token}.
func (s *Server) handleK8sPortForwardStop(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if !s.pf.stop(token) {
		writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "k8s.pfNotFound"))
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "k8s.portforward.stop", token[:min(8, len(token))], "ok", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleK8sPortForwardProxy — /api/k8s/pf/{token}/*: прокси на проброс.
// Без сессии: доступ — по токену (см. комментарий в начале файла). Если
// запрос пришёл с отдельного адреса пробросов хаба (X-NKT-Forward-Prefix),
// песочница не нужна: у страницы и так свой origin.
func (s *Server) handleK8sPortForwardProxy(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	marker := "/k8s/pf/" + token
	i := strings.Index(r.URL.Path, marker)
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	prefix := r.Header.Get("X-NKT-Forward-Prefix")
	if prefix != "" && !(strings.HasPrefix(prefix, "/f/") && strings.HasSuffix(prefix, "/"+token)) {
		prefix = ""
	}
	s.serveForward(w, r, token, r.URL.Path[i+len(marker):], prefix)
}

// ForwardHandler — отдельный адрес пробросов (NKT_FORWARD_ADDR):
// /f/{token}/… без песочницы — другой порт, другой origin.
func (s *Server) ForwardHandler() http.Handler {
	r := chi.NewRouter()
	h := func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		prefix := "/f/" + token
		s.serveForward(w, r, token, strings.TrimPrefix(r.URL.Path, prefix), prefix)
	}
	r.HandleFunc("/f/{token}", h)
	r.HandleFunc("/f/{token}/*", h)
	return r
}

type pfModeKey struct{}

// pfMode — как отдавать ответ: sandbox — в песочнице по пути /api (без
// cookie приложения); иначе — с адреса пробросов, cookie приложения
// живут под префиксом проброса.
type pfMode struct {
	sandbox bool
	prefix  string
}

func (s *Server) serveForward(w http.ResponseWriter, r *http.Request, token, rest, prefix string) {
	sess := s.pf.get(token)
	if sess == nil {
		writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "k8s.pfNotFound"))
		return
	}
	if rest == "" {
		// Без завершающей косой относительные ссылки страницы уйдут мимо.
		// Интерфейс даёт адреса с косой; перенаправлять сюда не станем —
		// адрес ответа тогда зависел бы от адреса запроса.
		http.Error(w, "add a trailing slash: /", http.StatusNotFound)
		return
	}
	// Токен в адресе не должен уходить наружу даже в пределах сайта
	// приложения — общий заголовок nkt (same-origin) заменяется.
	w.Header().Set("Referrer-Policy", "no-referrer")
	mode := pfMode{sandbox: prefix == "", prefix: prefix}
	r2 := r.Clone(context.WithValue(r.Context(), pfModeKey{}, mode))
	r2.URL.Path = rest
	r2.URL.RawPath = ""
	r2.Header.Del("X-NKT-Forward-Prefix")
	sess.proxy.ServeHTTP(w, r2)
}

// appCookies — cookie браузера без cookie nkt: cookie не различают порты,
// и сессия nkt пришла бы и на адрес пробросов.
func appCookies(h string) string {
	var keep []string
	for _, part := range strings.Split(h, ";") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		if name != "" && !strings.HasPrefix(strings.ToLower(name), "nkt") {
			keep = append(keep, strings.TrimSpace(part))
		}
	}
	return strings.Join(keep, "; ")
}

// pfProxy — обратный прокси на локальный порт проброса.
func pfProxy(local, port int) *httputil.ReverseProxy {
	target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(local))
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			mode, _ := pr.In.Context().Value(pfModeKey{}).(pfMode)
			pr.SetURL(target)
			pr.Out.Host = "127.0.0.1:" + strconv.Itoa(port)
			pr.Out.Header.Del("Authorization")
			if c := appCookies(pr.In.Header.Get("Cookie")); c != "" && !mode.sandbox {
				pr.Out.Header.Set("Cookie", c)
			} else {
				pr.Out.Header.Del("Cookie")
			}
			if mode.prefix != "" {
				pr.Out.Header.Set("X-Forwarded-Prefix", mode.prefix)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			mode, _ := resp.Request.Context().Value(pfModeKey{}).(pfMode)
			resp.Header.Del("Referrer-Policy")
			if mode.sandbox {
				resp.Header.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups allow-downloads allow-modals")
				resp.Header.Del("Set-Cookie")
				return nil
			}
			// Cookie приложения — только под префиксом проброса и не с
			// именами nkt (иначе приложение подменило бы сессию nkt).
			var keep []string
			for _, c := range resp.Header.Values("Set-Cookie") {
				name, _, _ := strings.Cut(c, "=")
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "nkt") {
					continue
				}
				keep = append(keep, withCookiePath(c, mode.prefix+"/"))
			}
			resp.Header.Del("Set-Cookie")
			for _, c := range keep {
				resp.Header.Add("Set-Cookie", c)
			}
			return nil
		},
	}
}

// withCookiePath заменяет (или добавляет) Path у Set-Cookie и убирает Domain.
func withCookiePath(c, path string) string {
	parts := strings.Split(c, ";")
	out := []string{strings.TrimSpace(parts[0])}
	for _, p := range parts[1:] {
		k, _, _ := strings.Cut(strings.TrimSpace(p), "=")
		switch strings.ToLower(k) {
		case "path", "domain":
			continue
		}
		out = append(out, strings.TrimSpace(p))
	}
	return strings.Join(append(out, "Path="+path), "; ")
}

// handleK8sPortForwardPorts — GET /k8s/portforward/ports?kind=&namespace=&name=:
// порты объекта для выбора в окне проброса.
func (s *Server) handleK8sPortForwardPorts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("kind") != "pods" && q.Get("kind") != "services" {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "k8s.badKind", q.Get("kind")))
		return
	}
	t, err := s.k8sManager().Find(r.Context(), q.Get("kind"), q.Get("namespace"), q.Get("name"))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ports := k8sTargetPorts(t)
	if ports == nil {
		ports = []int{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ports": ports})
}
