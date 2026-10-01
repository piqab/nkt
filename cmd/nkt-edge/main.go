// nkt-edge — входной сервис для вебхуков выкладок хаба nkt. Ставится на
// VPS с белым адресом: принимает по HTTPS (сертификат Let's Encrypt,
// выпущенный certbot)
// только POST /hooks/{id} и передаёт их хабу по туннелю, который хаб сам
// держит к edge. У edge нет ни базы, ни секретов конвейеров, ни доступа к
// хостам: подпись вебхука проверяет хаб.
//
// Настройка — переменные окружения (файл /etc/nkt-edge/edge.env):
//
//	EDGE_DOMAIN        имя вебхуков (hooks.example.com), для журнала
//	EDGE_TOKEN         общий секрет с хабом (не короче 32 знаков)
//	EDGE_DATA_DIR      каталог сертификата туннеля (/var/lib/nkt-edge)
//	EDGE_CERT_FILE     сертификат (/etc/nkt-edge/tls/fullchain.pem) —
//	EDGE_KEY_FILE      и ключ (/etc/nkt-edge/tls/privkey.pem): копия
//	                   сертификата certbot, её обновляет deploy-hook
//	EDGE_HTTPS_ADDR    :443
//	EDGE_TUNNEL_ADDR   :8444 (сюда подключается хаб)
//	EDGE_RATE          запросов в минуту с одного адреса (60)
//	EDGE_GITHUB_ONLY   true — принимать вебхуки только с адресов GitHub
//	EDGE_SELF_SIGNED   true — сертификатом туннеля (проверка, внутренняя сеть)
//	EDGE_PROXY_ADDR    127.0.0.1:8445 — за обратным прокси (nginx, Caddy),
//	                   когда 80 и 443 на VPS уже заняты: вебхуки по HTTP
//	                   только на loopback, TLS и сертификат — у прокси,
//	                   адрес отправителя — из X-Real-IP / X-Forwarded-For
//	EDGE_ROLES         роли через запятую (hooks): hooks — вебхуки
//	                   выкладок, api — подписанные запросы API-токенов
//	                   хаба (/api/hub/…, /api/hosts/…; Bearer, cookie и
//	                   веб-сокеты не проходят — секрет токена на VPS не
//	                   попадает)
//	EDGE_API_RATE      запросов API в минуту с одного адреса (120)
//	                   роль probe — проверки «снаружи» по просьбе хаба
//	                   (DNS, порты, HTTPS); только по туннелю
//	                   роль callbacks — колбэки ботов (Slack):
//	                   POST /callbacks/<платформа>/<вид>, подпись — у хаба
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/piqab/nkt/internal/edge"
)

var version = "dev"

const maxBody = 1 << 20

var hookPath = regexp.MustCompile(`^/hooks/[A-Za-z0-9_-]{1,64}$`)

type server struct {
	token string
	rate  int
	roles map[string]bool
	// behindProxy — вебхуки приходят от обратного прокси на loopback:
	// адрес отправителя берётся из его заголовков.
	behindProxy bool

	mu      sync.Mutex
	session *yamux.Session
	since   time.Time

	limiter    *limiter
	apiLimiter *limiter
	github     *githubNets
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	log.SetFlags(log.LstdFlags | log.LUTC)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	token := os.Getenv("EDGE_TOKEN")
	if len(token) < 32 {
		log.Fatal("EDGE_TOKEN must be at least 32 characters")
	}
	dataDir := env("EDGE_DATA_DIR", "/var/lib/nkt-edge")
	rate, _ := strconv.Atoi(env("EDGE_RATE", "60"))
	if rate <= 0 {
		rate = 60
	}
	apiRate, _ := strconv.Atoi(env("EDGE_API_RATE", "120"))
	if apiRate <= 0 {
		apiRate = 120
	}
	s := &server{token: token, rate: rate, limiter: newLimiter(rate), apiLimiter: newLimiter(apiRate), roles: map[string]bool{}}
	for _, role := range strings.Split(env("EDGE_ROLES", "hooks"), ",") {
		switch role = strings.TrimSpace(role); role {
		case "hooks", "api", "probe", "callbacks":
			s.roles[role] = true
		case "":
		default:
			log.Fatal("EDGE_ROLES: unknown role (hooks, api, probe, callbacks)")
		}
	}
	if len(s.roles) == 0 {
		s.roles["hooks"] = true
	}
	if env("EDGE_GITHUB_ONLY", "false") == "true" {
		s.github = &githubNets{}
		go s.github.refreshLoop()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cert, fp, err := edge.TunnelCert(filepath.Join(dataDir, "tunnel"))
	if err != nil {
		log.Fatalf("tunnel certificate: %v", err)
	}
	log.Printf("nkt-edge %s, tunnel certificate fingerprint %s (for the hub: %s)", version, fp, filepath.Join(dataDir, "tunnel", "tunnel.crt"))

	// Вебхуки из интернета. Порты занимаются до туннеля: если порт занят
	// (скажем, 443 держит nginx), служба выходит сразу с понятной ошибкой,
	// а хаб видит закрытый порт туннеля, а не обрыв посреди рукопожатия.
	mux := http.NewServeMux()
	if s.roles["hooks"] {
		mux.HandleFunc("/hooks/", s.handleHook)
	}
	if s.roles["api"] {
		mux.HandleFunc("/api/", s.handleAPI)
	}
	if s.roles["callbacks"] {
		mux.HandleFunc("/callbacks/", s.handleCallback)
	}
	mux.HandleFunc("/healthz", s.handleHealth)
	hookSrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}
	var serveHooks func() error
	if proxyAddr := os.Getenv("EDGE_PROXY_ADDR"); proxyAddr != "" {
		host, _, err := net.SplitHostPort(proxyAddr)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			// Само значение в журнал не пишется: оно из окружения, и в нём
			// могут быть переводы строк (подделка соседних записей).
			log.Fatal("EDGE_PROXY_ADDR must be a loopback address with a port, like 127.0.0.1:8445")
		}
		ln, err := net.Listen("tcp", proxyAddr)
		if err != nil {
			log.Fatalf("webhooks (behind proxy): %v", err)
		}
		s.behindProxy = true
		// Адрес не пишется: он из окружения (EDGE_PROXY_ADDR), оператор его
		// и так знает, а непроверенная строка в журнале — подделка записей.
		log.Print("webhooks over HTTP on EDGE_PROXY_ADDR (loopback), behind a reverse proxy")
		serveHooks = func() error { return hookSrv.Serve(ln) }
	} else {
		if env("EDGE_SELF_SIGNED", "false") == "true" {
			hookSrv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		} else {
			cf := &certFile{certPath: env("EDGE_CERT_FILE", "/etc/nkt-edge/tls/fullchain.pem"), keyPath: env("EDGE_KEY_FILE", "/etc/nkt-edge/tls/privkey.pem")}
			if err := cf.load(); err != nil {
				log.Fatalf("certificate: %v — issue it with certbot (see site: nkt-edge), or set EDGE_SELF_SIGNED=true", err)
			}
			hookSrv.TLSConfig = &tls.Config{GetCertificate: cf.GetCertificate, MinVersion: tls.VersionTLS12}
		}
		addr := env("EDGE_HTTPS_ADDR", ":443")
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("https: %v — the port is taken by another program; stop it or run nkt-edge behind it with EDGE_PROXY_ADDR", err)
		}
		serveHooks = func() error { return hookSrv.ServeTLS(ln, "", "") }
	}

	// Туннель для хаба.
	tl, err := tls.Listen("tcp", env("EDGE_TUNNEL_ADDR", ":8444"), &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	if err != nil {
		log.Fatalf("tunnel listener: %v", err)
	}
	go s.acceptHubs(tl)
	go func() {
		if err := serveHooks(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("webhooks: %v", err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = hookSrv.Shutdown(shutdown)
	_ = tl.Close()
}

// acceptHubs принимает подключение хаба; новое заменяет прежнее.
func (s *server) acceptHubs(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go func() {
			if err := edge.Accept(conn, s.token); err != nil {
				log.Printf("hub %s rejected: %v", conn.RemoteAddr(), err)
				conn.Close()
				return
			}
			sess, err := yamux.Server(conn, yamux.DefaultConfig())
			if err != nil {
				conn.Close()
				return
			}
			s.mu.Lock()
			old := s.session
			s.session, s.since = sess, time.Now()
			s.mu.Unlock()
			if old != nil {
				old.Close()
			}
			log.Printf("hub connected from %s", conn.RemoteAddr())
			if s.roles["probe"] {
				// Хаб открывает потоки сам — проверки «снаружи».
				go func() { _ = (&http.Server{Handler: probeHandler(), ReadHeaderTimeout: 10 * time.Second}).Serve(sess) }()
			}
			<-sess.CloseChan()
			s.mu.Lock()
			if s.session == sess {
				s.session = nil
			}
			s.mu.Unlock()
			log.Printf("hub disconnected")
		}()
	}
}

// clientIP — адрес отправителя: для ограничения частоты, фильтра GitHub
// и журнала. За обратным прокси (и только если запрос пришёл с loopback)
// — из X-Real-IP или последнего адреса X-Forwarded-For, который дописал
// сам прокси; чужому заголовку от прямого клиента не верим.
func (s *server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !s.behindProxy {
		return host
	}
	if peer := net.ParseIP(host); peer == nil || !peer.IsLoopback() {
		return host
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
			return ip.String()
		}
	}
	return host
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	connected := s.session != nil
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	roles := []string{}
	for _, role := range []string{"hooks", "api", "probe", "callbacks"} {
		if s.roles[role] {
			roles = append(roles, role)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "hub": connected, "version": version, "roles": roles})
}

// handleHook — фильтры и передача хабу. Всё, что не POST на
// /hooks/{id}, — 404; сверх частоты — 429; хаб не подключён — 503.
func (s *server) handleHook(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	status := http.StatusOK
	// Поля — атрибутами slog: значения из запроса журнал экранирует сам
	// (переводы строк не подделают соседние записи).
	defer func() {
		slog.Info("hook", "ip", noNewlines(ip), "method", noNewlines(r.Method), "path", noNewlines(r.URL.Path), "status", status)
	}()
	if r.Method != http.MethodPost || !hookPath.MatchString(r.URL.Path) {
		status = http.StatusNotFound
		http.NotFound(w, r)
		return
	}
	if !s.limiter.allow(ip, time.Now()) {
		status = http.StatusTooManyRequests
		http.Error(w, "too many requests", status)
		return
	}
	if s.github != nil && !s.github.contains(net.ParseIP(ip)) {
		status = http.StatusForbidden
		http.Error(w, "forbidden", status)
		return
	}
	status = s.forward(w, r, ip)
}

// handleAPI — роль api: подписанные запросы API-токенов хаба. Без
// подписи, с Bearer или cookie, веб-сокеты и пути вне API токенов — отказ
// здесь же: секрет токена на VPS не попадает, а хаб проверит всё ещё раз.
func (s *server) handleAPI(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	status := http.StatusOK
	defer func() {
		slog.Info("api", "ip", noNewlines(ip), "method", noNewlines(r.Method), "path", noNewlines(r.URL.Path), "status", status)
	}()
	if !edge.APIPath(r.URL.Path) || r.Header.Get("Upgrade") != "" {
		status = http.StatusNotFound
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "" || r.Header.Get("X-NKT-API-Signature") == "" || r.Header.Get("X-NKT-API-Key") == "" {
		status = http.StatusUnauthorized
		http.Error(w, `{"error":"only signed API token requests (X-NKT-API-*) pass through nkt-edge"}`, status)
		return
	}
	if !s.apiLimiter.allow(ip, time.Now()) {
		status = http.StatusTooManyRequests
		http.Error(w, "too many requests", status)
		return
	}
	status = s.forward(w, r, ip)
}

// forward — запрос хабу по туннелю; код ответа — для журнала.
func (s *server) forward(w http.ResponseWriter, r *http.Request, ip string) int {
	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		http.Error(w, "hub not connected", http.StatusServiceUnavailable)
		return http.StatusServiceUnavailable
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", "hub"
			pr.Out.Host = "hub"
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("X-Real-IP")
			pr.Out.Header.Set("X-Forwarded-For", ip)
		},
		Transport: &http.Transport{
			DialContext:           func(context.Context, string, string) (net.Conn, error) { return sess.Open() },
			ResponseHeaderTimeout: 60 * time.Second,
			DisableKeepAlives:     true,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "hub unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(rec, r)
	return rec.status
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// noNewlines — значение из запроса для журнала: без переводов строк.
// slog и так берёт такие значения в кавычки; это — вторая линия.
func noNewlines(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, "\n", ""), "\r", "")
}

// probeHandler — роль probe: POST /probe от хаба по туннелю.
func probeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(edge.ProbePath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req edge.ProbeRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || len(req.Checks) == 0 || len(req.Checks) > edge.ProbeMaxChecks {
			http.Error(w, `{"error":"bad probe request"}`, http.StatusBadRequest)
			return
		}
		res := edge.RunProbe(r.Context(), req)
		slog.Info("probe", "checks", len(req.Checks))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})
	return mux
}

var callbackPath = regexp.MustCompile(`^/callbacks/[a-z]{2,20}/[a-z]{2,20}$`)

// handleCallback — роль callbacks: колбэки ботов (Slack). Подпись
// платформы проверяет хаб; здесь — только путь, метод, тело и частота.
func (s *server) handleCallback(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	status := http.StatusOK
	defer func() {
		slog.Info("callback", "ip", noNewlines(ip), "method", noNewlines(r.Method), "path", noNewlines(r.URL.Path), "status", status)
	}()
	if r.Method != http.MethodPost || !callbackPath.MatchString(r.URL.Path) {
		status = http.StatusNotFound
		http.NotFound(w, r)
		return
	}
	if !s.limiter.allow(ip, time.Now()) {
		status = http.StatusTooManyRequests
		http.Error(w, "too many requests", status)
		return
	}
	status = s.forward(w, r, ip)
}
