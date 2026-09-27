package api

import (
	"bufio"
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

// handleK8sPortForwardProxy — /k8s/pf/{token}/*: прокси на проброс.
// Без сессии: доступ — по токену (см. комментарий в начале файла).
func (s *Server) handleK8sPortForwardProxy(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	sess := s.pf.get(token)
	if sess == nil {
		writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "k8s.pfNotFound"))
		return
	}
	marker := "/k8s/pf/" + token
	i := strings.Index(r.URL.Path, marker)
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	rest := r.URL.Path[i+len(marker):]
	if rest == "" {
		// Без завершающей косой относительные ссылки страницы уйдут мимо.
		http.Redirect(w, r, r.URL.Path+"/", http.StatusFound)
		return
	}
	// Общий заголовок nkt (same-origin) — заменяется: токен в адресе не
	// должен уходить наружу даже в пределах сайта приложения.
	w.Header().Set("Referrer-Policy", "no-referrer")
	r2 := r.Clone(r.Context())
	r2.URL.Path = rest
	r2.URL.RawPath = ""
	sess.proxy.ServeHTTP(w, r2)
}

// pfProxy — обратный прокси на локальный порт проброса: без cookie и
// заголовков входа nkt, ответ — в песочнице CSP и без Set-Cookie.
func pfProxy(local, port int) *httputil.ReverseProxy {
	target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(local))
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = "127.0.0.1:" + strconv.Itoa(port)
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups allow-downloads allow-modals")
			resp.Header.Del("Referrer-Policy")
			resp.Header.Del("Set-Cookie")
			return nil
		},
	}
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
