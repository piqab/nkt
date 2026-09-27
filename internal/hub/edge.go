package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hashicorp/yamux"
	"github.com/pkg/sftp"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/edge"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
)

// nkt-edge со стороны хаба: хаб сам подключается к edge на VPS (TLS с
// закреплённым отпечатком, токен, yamux) и держит соединение; по нему
// приходят только вебхуки выкладок — через туннель хаб обслуживает один
// маршрут, POST /hooks/{hook}. Настройки — в KV хаба, токен зашифрован.

const edgeSettingsKey = "hub.edge"

// EdgeSettings — настройки edge.
type EdgeSettings struct {
	Enabled bool `json:"enabled"`
	// Address — куда подключаться (хост:8444).
	Address string `json:"address"`
	// Domain — имя с сертификатом Let's Encrypt (адрес вебхуков).
	Domain   string `json:"domain"`
	TokenEnc []byte `json:"token_enc,omitempty"`
	// CertPEM — сертификат туннеля edge: хаб доверяет ровно ему.
	CertPEM     string `json:"cert_pem,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// HostID — хост хаба, на который edge поставлен (0 — вручную).
	HostID int64 `json:"host_id,omitempty"`
}

type edgeClient struct {
	mu        sync.Mutex
	connected bool
	since     time.Time
	lastErr   string
	reload    chan struct{}
	cancel    context.CancelFunc
}

func (s *Server) edgeSettings(ctx context.Context) EdgeSettings {
	var st EdgeSettings
	if raw, ok, err := s.db.KVGet(ctx, edgeSettingsKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

func (s *Server) saveEdgeSettings(ctx context.Context, st EdgeSettings) error {
	b, _ := json.Marshal(st)
	return s.db.KVSet(ctx, edgeSettingsKey, string(b))
}

// kickEdge — переподключиться с новыми настройками.
func (s *Server) kickEdge() {
	if s.edge == nil {
		return
	}
	s.edge.mu.Lock()
	if s.edge.cancel != nil {
		s.edge.cancel()
	}
	s.edge.mu.Unlock()
	select {
	case s.edge.reload <- struct{}{}:
	default:
	}
}

// StartEdge держит соединение с edge, пока он включён.
func (s *Server) StartEdge(ctx context.Context) {
	s.edge = &edgeClient{reload: make(chan struct{}, 1)}
	go func() {
		backoff := 5 * time.Second
		for ctx.Err() == nil {
			st := s.edgeSettings(ctx)
			if !st.Enabled || st.Address == "" {
				s.setEdgeStatus(false, "")
				select {
				case <-ctx.Done():
				case <-s.edge.reload:
				case <-time.After(time.Minute):
				}
				continue
			}
			err := s.runEdge(ctx, st)
			s.setEdgeStatus(false, errText(err))
			if err == nil {
				backoff = 5 * time.Second
			} else if backoff < time.Minute {
				backoff *= 2
			}
			select {
			case <-ctx.Done():
			case <-s.edge.reload:
				backoff = 5 * time.Second
			case <-time.After(backoff):
			}
		}
	}()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) setEdgeStatus(connected bool, lastErr string) {
	s.edge.mu.Lock()
	defer s.edge.mu.Unlock()
	if connected && !s.edge.connected {
		s.edge.since = time.Now()
	}
	s.edge.connected = connected
	if lastErr != "" || connected {
		s.edge.lastErr = lastErr
	}
}

// runEdge — одно подключение: до разрыва или смены настроек.
func (s *Server) runEdge(ctx context.Context, st EdgeSettings) error {
	token, err := secretbox.Decrypt(s.hub.key, st.TokenEnc)
	if err != nil {
		return err
	}
	if st.CertPEM == "" {
		return msgs.Errorf("edge.noCert")
	}
	conn, _, err := edge.Dial(st.Address, string(token), st.CertPEM, 15*time.Second)
	if err != nil {
		return err
	}
	sess, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		conn.Close()
		return err
	}
	cctx, cancel := context.WithCancel(ctx)
	s.edge.mu.Lock()
	s.edge.cancel = cancel
	s.edge.mu.Unlock()
	defer cancel()
	go func() {
		<-cctx.Done()
		sess.Close()
	}()
	s.setEdgeStatus(true, "")
	srv := &http.Server{Handler: s.edgeHandler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second}
	err = srv.Serve(sess)
	if cctx.Err() != nil || errors.Is(err, net.ErrClosed) || sess.IsClosed() {
		return nil
	}
	return err
}

// edgeHandler — единственный маршрут туннеля.
func (s *Server) edgeHandler() http.Handler {
	r := chi.NewRouter()
	r.Post("/hooks/{hook}", func(w http.ResponseWriter, r *http.Request) {
		from := r.Header.Get("X-Forwarded-For")
		if from == "" {
			from = "edge"
		}
		s.serveHook(w, r, from+" (edge)")
	})
	return r
}

type edgeStatusJSON struct {
	Configured  bool   `json:"configured"`
	Enabled     bool   `json:"enabled"`
	Address     string `json:"address,omitempty"`
	Domain      string `json:"domain,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	HostID      int64  `json:"host_id,omitempty"`
	Connected   bool   `json:"connected"`
	Since       string `json:"since,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

// handleEdgeStatus — GET /hub/edge.
func (s *Server) handleEdgeStatus(w http.ResponseWriter, r *http.Request) {
	st := s.edgeSettings(r.Context())
	out := edgeStatusJSON{Configured: st.Address != "" && len(st.TokenEnc) > 0 && st.CertPEM != "", Enabled: st.Enabled, Address: st.Address, Domain: st.Domain,
		Fingerprint: st.Fingerprint, HostID: st.HostID}
	if s.edge != nil {
		s.edge.mu.Lock()
		out.Connected, out.LastError = s.edge.connected, s.edge.lastErr
		if s.edge.connected {
			out.Since = s.edge.since.UTC().Format(time.RFC3339)
		}
		s.edge.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, out)
}

var (
	edgeDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	edgeEmailRe  = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,190}\.[A-Za-z]{2,63}$`)
)

// handleEdgeUpdate — PUT /hub/edge {enabled, address, domain, token,
// reset_fingerprint}: настройка вручную (edge поставлен не из хаба).
func (s *Server) handleEdgeUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool   `json:"enabled"`
		Address string `json:"address"`
		Domain  string `json:"domain"`
		Token   string `json:"token"`
		CertPEM string `json:"cert_pem"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	st := s.edgeSettings(r.Context())
	if req.Address != "" {
		if _, _, err := net.SplitHostPort(req.Address); err != nil {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.badAddress", req.Address))
			return
		}
		st.Address = req.Address
	}
	if req.Domain != "" {
		if !edgeDomainRe.MatchString(strings.ToLower(req.Domain)) {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.badDomain", req.Domain))
			return
		}
		st.Domain = strings.ToLower(req.Domain)
	}
	if req.Token != "" {
		if len(req.Token) < 32 {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.shortToken"))
			return
		}
		enc, err := secretbox.Encrypt(s.hub.key, []byte(req.Token))
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		st.TokenEnc = enc
	}
	if strings.TrimSpace(req.CertPEM) != "" {
		fp, err := edge.CertInfo(strings.TrimSpace(req.CertPEM))
		if err != nil {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.badCert"))
			return
		}
		st.CertPEM, st.Fingerprint = strings.TrimSpace(req.CertPEM)+"\n", fp
	}
	st.Enabled = req.Enabled
	err := s.saveEdgeSettings(r.Context(), st)
	s.db.Audit(r.Context(), auth.Username(r.Context()), "edge.update", st.Address, auditOutcome(err), map[string]any{"enabled": st.Enabled, "domain": st.Domain})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.kickEdge()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleEdgeDelete — DELETE /hub/edge: забыть edge (на VPS он остаётся).
func (s *Server) handleEdgeDelete(w http.ResponseWriter, r *http.Request) {
	err := s.db.KVSet(r.Context(), edgeSettingsKey, "{}")
	s.db.Audit(r.Context(), auth.Username(r.Context()), "edge.delete", "", auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.kickEdge()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// KindEdgeInstall — задание «поставить nkt-edge на хост».
const KindEdgeInstall = "edge.install"

// EdgeInstallParams — вход задания.
type EdgeInstallParams struct {
	HostID     int64  `json:"host_id"`
	Domain     string `json:"domain"`
	Email      string `json:"email"`
	GitHubOnly bool   `json:"github_only"`
}

// handleEdgeInstall — POST /hub/edge/install {host_id, domain, email, github_only}.
func (s *Server) handleEdgeInstall(w http.ResponseWriter, r *http.Request) {
	var req EdgeInstallParams
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !edgeDomainRe.MatchString(req.Domain) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.badDomain", req.Domain))
		return
	}
	if req.Email != "" && !edgeEmailRe.MatchString(req.Email) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.badEmail", req.Email))
		return
	}
	host, err := s.db.HostByID(r.Context(), req.HostID)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindEdgeInstall, TitleKey: "edge.jobTitle", TitleArgs: []any{host.Name, req.Domain},
		Queue: fmt.Sprintf("host:%d", host.ID), Author: user, Steps: 5, Params: req,
	})
	s.db.Audit(r.Context(), user, "edge.install", host.Name, auditOutcome(err), req.Domain)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// EdgeInstallRunner ставит nkt-edge на хост по SSH.
type EdgeInstallRunner struct{ s *Server }

// NewEdgeInstallRunner строит исполнителя.
func NewEdgeInstallRunner(s *Server) *EdgeInstallRunner { return &EdgeInstallRunner{s: s} }

// Resumable — да: установка повторяема целиком.
func (r *EdgeInstallRunner) Resumable() bool { return true }

// EdgeTunnelPort — порт туннеля на edge.
const EdgeTunnelPort = "8444"

// EdgeUnit — systemd-юнит nkt-edge: отдельный пользователь без прав,
// кроме привязки к портам 80 и 443, всё остальное закрыто.
const EdgeUnit = `[Unit]
Description=nkt-edge — webhooks for the nkt hub
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/nkt-edge
EnvironmentFile=/etc/nkt-edge/edge.env
DynamicUser=yes
StateDirectory=nkt-edge
Environment=EDGE_DATA_DIR=/var/lib/nkt-edge
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallFilter=@system-service
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`

// Run: бинарник под архитектуру хоста, файлы, служба, отпечаток, связь.
func (r *EdgeInstallRunner) Run(ctx context.Context, jc *jobs.Context) error {
	s := r.s
	var p EdgeInstallParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	host, err := s.db.HostByID(ctx, p.HostID)
	if err != nil {
		return err
	}
	report := func(key string, args ...any) { jc.Log(key, args...) }
	jc.StepKey(1, 5, "edge.stepConnect", host.Name)
	link, err := s.hub.dialHost(ctx, host)
	if err != nil {
		return err
	}
	defer link.Close()
	client := link.client
	goos, goarch, err := detectTarget(client)
	if err != nil {
		return err
	}
	jc.StepKey(2, 5, "edge.stepBinary", goos, goarch)
	bin, err := s.hub.ensureProgram(ctx, "nkt-edge", goos, goarch, report, report)
	if err != nil {
		return err
	}

	jc.StepKey(3, 5, "edge.stepFiles")
	st := s.edgeSettings(ctx)
	token := ""
	if len(st.TokenEnc) > 0 && st.HostID == host.ID {
		if raw, err := secretbox.Decrypt(s.hub.key, st.TokenEnc); err == nil {
			token = string(raw)
		}
	}
	if token == "" {
		token = randomToken(32)
	}
	envFile := fmt.Sprintf("EDGE_DOMAIN=%s\nEDGE_EMAIL=%s\nEDGE_TOKEN=%s\nEDGE_TUNNEL_ADDR=:%s\nEDGE_GITHUB_ONLY=%t\n", p.Domain, p.Email, token, EdgeTunnelPort, p.GitHubOnly)
	sc, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sc.Close()
	stage := "/tmp/nkt-edge-install-" + randomHex(6)
	if err := sc.MkdirAll(stage); err != nil {
		return err
	}
	_ = sc.Chmod(stage, 0o700)
	defer func() { _, _ = runRemote(client, "rm -rf "+stage) }()
	if err := uploadFile(sc, bin, stage+"/nkt-edge", 0o755, nil); err != nil {
		return err
	}
	if err := uploadBytes(sc, []byte(envFile), stage+"/edge.env", 0o600); err != nil {
		return err
	}
	if err := uploadBytes(sc, []byte(EdgeUnit), stage+"/nkt-edge.service", 0o644); err != nil {
		return err
	}
	for _, f := range []struct {
		src, dst string
		mode     uint32
	}{
		{stage + "/nkt-edge", "/usr/local/bin/nkt-edge", 0o755},
		{stage + "/edge.env", "/etc/nkt-edge/edge.env", 0o600},
		{stage + "/nkt-edge.service", "/etc/systemd/system/nkt-edge.service", 0o644},
	} {
		if err := installRemoteFile(client, host.SSHUser, f.src, f.dst, os.FileMode(f.mode)); err != nil {
			return err
		}
	}

	jc.StepKey(4, 5, "edge.stepService")
	sudo := sudoPrefix(host.SSHUser)
	script := sudo + "systemctl daemon-reload && " + sudo + "systemctl enable nkt-edge && " + sudo + "systemctl restart nkt-edge"
	if out, err := runRemote(client, script); err != nil {
		return msgs.Errorf("edge.serviceFailed", strings.TrimSpace(out))
	}
	// Файрвол хоста: открыть 80, 443 и порт туннеля, если ufw включён.
	_, _ = runRemote(client, "if command -v ufw >/dev/null && "+sudo+"ufw status | grep -q 'Status: active'; then "+sudo+"ufw allow 80/tcp; "+sudo+"ufw allow 443/tcp; "+sudo+"ufw allow "+EdgeTunnelPort+"/tcp; fi")
	// Сертификат туннеля — прямо с хоста по SSH: хаб доверяет ровно ему.
	var fp, certPEM string
	for i := 0; i < 15 && fp == ""; i++ {
		out, err := runRemote(client, sudo+"cat /var/lib/nkt-edge/tunnel/tunnel.crt")
		if err == nil {
			if f, err := edge.CertInfo(out); err == nil {
				fp, certPEM = f, out
			}
		}
		if fp == "" && !sleepCtx(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
	if fp == "" {
		return msgs.Errorf("edge.noFingerprint")
	}
	jc.Log("edge.fingerprint", fp)

	jc.StepKey(5, 5, "edge.stepConnectHub")
	enc, err := secretbox.Encrypt(s.hub.key, []byte(token))
	if err != nil {
		return err
	}
	st = EdgeSettings{Enabled: true, Address: net.JoinHostPort(host.Addr, EdgeTunnelPort), Domain: p.Domain, TokenEnc: enc, CertPEM: certPEM, Fingerprint: fp, HostID: host.ID}
	if err := s.saveEdgeSettings(ctx, st); err != nil {
		return err
	}
	s.kickEdge()
	for i := 0; i < 20; i++ {
		if s.edge != nil {
			s.edge.mu.Lock()
			ok, last := s.edge.connected, s.edge.lastErr
			s.edge.mu.Unlock()
			if ok {
				jc.Log("edge.connected", p.Domain)
				return nil
			}
			if i == 19 && last != "" {
				return msgs.Errorf("edge.notConnected", last)
			}
		}
		if !sleepCtx(ctx, time.Second) {
			return ctx.Err()
		}
	}
	return msgs.Errorf("edge.notConnected", "timeout")
}
