// nkt-edge — входной сервис для вебхуков выкладок хаба nkt. Ставится на
// VPS с белым адресом: принимает по HTTPS (сертификат Let's Encrypt)
// только POST /hooks/{id} и передаёт их хабу по туннелю, который хаб сам
// держит к edge. У edge нет ни базы, ни секретов конвейеров, ни доступа к
// хостам: подпись вебхука проверяет хаб.
//
// Настройка — переменные окружения (файл /etc/nkt-edge/edge.env):
//
//	EDGE_DOMAIN        имя для сертификата (hooks.example.com)
//	EDGE_EMAIL         e-mail для Let's Encrypt
//	EDGE_TOKEN         общий секрет с хабом (не короче 32 знаков)
//	EDGE_DATA_DIR      каталог сертификатов (/var/lib/nkt-edge)
//	EDGE_HTTPS_ADDR    :443
//	EDGE_HTTP_ADDR     :80 (выпуск сертификата; пусто — не слушать)
//	EDGE_TUNNEL_ADDR   :8444 (сюда подключается хаб)
//	EDGE_RATE          запросов в минуту с одного адреса (60)
//	EDGE_GITHUB_ONLY   true — принимать вебхуки только с адресов GitHub
//	EDGE_SELF_SIGNED   true — без Let's Encrypt (проверка, внутренняя сеть)
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/acme/autocert"

	"github.com/piqab/nkt/internal/edge"
)

var version = "dev"

const maxBody = 1 << 20

var hookPath = regexp.MustCompile(`^/hooks/[A-Za-z0-9_-]{1,64}$`)

type server struct {
	token string
	rate  int

	mu      sync.Mutex
	session *yamux.Session
	since   time.Time

	limiter *limiter
	github  *githubNets
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
	token := os.Getenv("EDGE_TOKEN")
	if len(token) < 32 {
		log.Fatal("EDGE_TOKEN must be at least 32 characters")
	}
	dataDir := env("EDGE_DATA_DIR", "/var/lib/nkt-edge")
	rate, _ := strconv.Atoi(env("EDGE_RATE", "60"))
	if rate <= 0 {
		rate = 60
	}
	s := &server{token: token, rate: rate, limiter: newLimiter(rate)}
	if env("EDGE_GITHUB_ONLY", "false") == "true" {
		s.github = &githubNets{}
		go s.github.refreshLoop()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Туннель для хаба.
	cert, fp, err := edge.TunnelCert(filepath.Join(dataDir, "tunnel"))
	if err != nil {
		log.Fatalf("tunnel certificate: %v", err)
	}
	log.Printf("nkt-edge %s, tunnel certificate fingerprint %s", version, fp)
	tl, err := tls.Listen("tcp", env("EDGE_TUNNEL_ADDR", ":8444"), &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	if err != nil {
		log.Fatalf("tunnel listener: %v", err)
	}
	go s.acceptHubs(tl)

	// Вебхуки из интернета.
	mux := http.NewServeMux()
	mux.HandleFunc("/hooks/", s.handleHook)
	mux.HandleFunc("/healthz", s.handleHealth)
	httpsSrv := &http.Server{
		Addr: env("EDGE_HTTPS_ADDR", ":443"), Handler: mux,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}
	var httpSrv *http.Server
	if env("EDGE_SELF_SIGNED", "false") == "true" {
		httpsSrv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	} else {
		domain := os.Getenv("EDGE_DOMAIN")
		if domain == "" {
			log.Fatal("EDGE_DOMAIN is required (or EDGE_SELF_SIGNED=true)")
		}
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(domain),
			Cache:      autocert.DirCache(filepath.Join(dataDir, "acme")),
			Email:      os.Getenv("EDGE_EMAIL"),
		}
		httpsSrv.TLSConfig = m.TLSConfig()
		httpsSrv.TLSConfig.MinVersion = tls.VersionTLS12
		if addr := env("EDGE_HTTP_ADDR", ":80"); addr != "" {
			// Порт 80 — только для выпуска сертификата (HTTP-01).
			httpSrv = &http.Server{Addr: addr, Handler: m.HTTPHandler(http.NotFoundHandler()), ReadHeaderTimeout: 10 * time.Second}
			go func() {
				if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Printf("http: %v", err)
				}
			}()
		}
	}
	go func() {
		if err := httpsSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("https: %v", err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpsSrv.Shutdown(shutdown)
	if httpSrv != nil {
		_ = httpSrv.Shutdown(shutdown)
	}
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	connected := s.session != nil
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "hub": connected, "version": version})
}

// handleHook — фильтры и передача хабу. Всё, что не POST на
// /hooks/{id}, — 404; сверх частоты — 429; хаб не подключён — 503.
func (s *server) handleHook(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	status := http.StatusOK
	defer func() { log.Printf("%s %s %s %d", ip, r.Method, r.URL.Path, status) }()
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
	s.mu.Lock()
	sess := s.session
	s.mu.Unlock()
	if sess == nil {
		status = http.StatusServiceUnavailable
		http.Error(w, "hub not connected", status)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", "hub"
			pr.Out.Host = "hub"
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
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
	status = rec.status
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
