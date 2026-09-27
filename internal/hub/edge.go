package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hashicorp/yamux"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

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
	lastErr   error
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
				s.setEdgeStatus(false, nil)
				select {
				case <-ctx.Done():
				case <-s.edge.reload:
				case <-time.After(time.Minute):
				}
				continue
			}
			err := s.runEdge(ctx, st)
			s.setEdgeStatus(false, err)
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

func (s *Server) setEdgeStatus(connected bool, lastErr error) {
	s.edge.mu.Lock()
	defer s.edge.mu.Unlock()
	if connected && !s.edge.connected {
		s.edge.since = time.Now()
	}
	s.edge.connected = connected
	if lastErr != nil || connected {
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
		if errors.Is(err, io.EOF) {
			// Порт туннеля принял соединение и закрыл его без ответа:
			// обычно служба edge падает и перезапускается по кругу.
			return msgs.Errorf("edge.closedByEdge", st.Address)
		}
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
	s.setEdgeStatus(true, nil)
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
		out.Connected = s.edge.connected
		if s.edge.lastErr != nil {
			out.LastError = msgs.Localize(msgs.LangFromRequest(r), s.edge.lastErr)
		}
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
	// ProxyPort — edge за обратным прокси на VPS (80 и 443 заняты nginx
	// или Caddy): вебхуки по HTTP на 127.0.0.1:ProxyPort; 0 — edge сам
	// на 80 и 443.
	ProxyPort int `json:"proxy_port,omitempty"`
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
	if req.ProxyPort != 0 && (req.ProxyPort < 1024 || req.ProxyPort > 65535 || strconv.Itoa(req.ProxyPort) == EdgeTunnelPort) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.badProxyPort", req.ProxyPort))
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
	if err := edgePortsFree(client, host.SSHUser, p.ProxyPort, jc); err != nil {
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
	if p.ProxyPort > 0 {
		envFile += fmt.Sprintf("EDGE_PROXY_ADDR=127.0.0.1:%d\n", p.ProxyPort)
	}
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
	if err := uploadFile(sc, bin, stage+"/nkt-edge", 0o755, report); err != nil {
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
	// restart проходит, даже если процесс падает через долю секунды, —
	// служба должна прожить несколько секунд тем же процессом.
	if err := edgeServiceStable(ctx, client, sudo); err != nil {
		return err
	}
	// Файрвол хоста: порт туннеля, а без прокси — ещё 80 и 443, если ufw
	// включён (за прокси они уже открыты для него).
	allow := sudo + "ufw allow " + EdgeTunnelPort + "/tcp; "
	if p.ProxyPort == 0 {
		allow = sudo + "ufw allow 80/tcp; " + sudo + "ufw allow 443/tcp; " + allow
	}
	_, _ = runRemote(client, "if command -v ufw >/dev/null && "+sudo+"ufw status | grep -q 'Status: active'; then "+allow+"fi")
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
				if p.ProxyPort > 0 {
					jc.Log("edge.proxySnippet", p.Domain, p.ProxyPort)
				}
				return nil
			}
			if i == 19 && last != nil {
				return msgs.Errorf("edge.notConnected", last)
			}
		}
		if !sleepCtx(ctx, time.Second) {
			return ctx.Err()
		}
	}
	return msgs.Errorf("edge.notConnected", "timeout")
}

var ssProcRe = regexp.MustCompile(`\("([^"]+)",pid=`)

// edgePortsFree — до установки: порты, которые займёт edge, не держит
// другая программа. Иначе служба падала бы по кругу, а хаб видел бы
// только обрыв соединения. 80 — предупреждение: без него сертификат
// выпускается через 443.
func edgePortsFree(client *ssh.Client, user string, proxyPort int, jc *jobs.Context) error {
	sudo := sudoPrefix(user)
	holder := func(port int) string {
		out, err := runRemote(client, sudo+"ss -ltnpH 'sport = :"+strconv.Itoa(port)+"'")
		if err != nil || strings.TrimSpace(out) == "" {
			return ""
		}
		names := []string{}
		for _, m := range ssProcRe.FindAllStringSubmatch(out, -1) {
			if m[1] != "nkt-edge" && !slices.Contains(names, m[1]) {
				names = append(names, m[1])
			}
		}
		if len(names) == 0 && !strings.Contains(out, "nkt-edge") {
			return "?"
		}
		return strings.Join(names, ", ")
	}
	tunnel, _ := strconv.Atoi(EdgeTunnelPort)
	if who := holder(tunnel); who != "" {
		return msgs.Errorf("edge.portBusy", tunnel, who)
	}
	if proxyPort > 0 {
		if who := holder(proxyPort); who != "" {
			return msgs.Errorf("edge.portBusy", proxyPort, who)
		}
		return nil
	}
	if who := holder(443); who != "" {
		return msgs.Errorf("edge.port443Busy", who)
	}
	if who := holder(80); who != "" {
		jc.Log("edge.port80Busy", who)
	}
	return nil
}

// edgeServiceStable — служба nkt-edge активна и не перезапускается:
// тот же MainPID через несколько секунд. Иначе — хвост её журнала.
func edgeServiceStable(ctx context.Context, client *ssh.Client, sudo string) error {
	pid := func() string {
		out, _ := runRemote(client, sudo+"systemctl show -p MainPID --value nkt-edge")
		return strings.TrimSpace(out)
	}
	if !sleepCtx(ctx, time.Second) {
		return ctx.Err()
	}
	first := pid()
	if !sleepCtx(ctx, 4*time.Second) {
		return ctx.Err()
	}
	if first != "" && first != "0" && pid() == first {
		return nil
	}
	out, _ := runRemote(client, sudo+"journalctl -u nkt-edge -n 8 --no-pager -o cat")
	return msgs.Errorf("edge.serviceCrashing", strings.TrimSpace(out))
}

// KindEdgeUninstall — задание «удалить nkt-edge с хоста».
const KindEdgeUninstall = "edge.uninstall"

// EdgeUninstallParams — вход задания: хост, на который edge ставил хаб.
type EdgeUninstallParams struct {
	HostID int64 `json:"host_id"`
}

// handleEdgeUninstall — POST /hub/edge/uninstall: снять edge с VPS, на
// который его поставил хаб, и забыть настройки. Edge, поставленный
// вручную, хаб не трогает — только «Забыть».
func (s *Server) handleEdgeUninstall(w http.ResponseWriter, r *http.Request) {
	st := s.edgeSettings(r.Context())
	if st.HostID == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.uninstallManual"))
		return
	}
	host, err := s.db.HostByID(r.Context(), st.HostID)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindEdgeUninstall, TitleKey: "edge.uninstallTitle", TitleArgs: []any{host.Name},
		Queue: fmt.Sprintf("host:%d", host.ID), Author: user, Steps: 3, Params: EdgeUninstallParams{HostID: host.ID},
	})
	s.db.Audit(r.Context(), user, "edge.uninstall", host.Name, auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// EdgeUninstallRunner снимает nkt-edge с хоста по SSH.
type EdgeUninstallRunner struct{ s *Server }

// NewEdgeUninstallRunner строит исполнителя.
func NewEdgeUninstallRunner(s *Server) *EdgeUninstallRunner { return &EdgeUninstallRunner{s: s} }

// Resumable — да: удаление повторяемо.
func (r *EdgeUninstallRunner) Resumable() bool { return true }

// Run: служба, файлы, данные (DynamicUser хранит их в /var/lib/private),
// правило ufw для порта туннеля; затем хаб забывает edge. Правила 80 и 443
// не трогаются — их может использовать другой сервер на этом VPS.
func (r *EdgeUninstallRunner) Run(ctx context.Context, jc *jobs.Context) error {
	s := r.s
	var p EdgeUninstallParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	host, err := s.db.HostByID(ctx, p.HostID)
	if err != nil {
		return err
	}
	jc.StepKey(1, 3, "edge.stepConnect", host.Name)
	link, err := s.hub.dialHost(ctx, host)
	if err != nil {
		return err
	}
	defer link.Close()
	sudo := sudoPrefix(host.SSHUser)
	jc.StepKey(2, 3, "edge.stepRemove")
	script := sudo + "systemctl disable --now nkt-edge 2>/dev/null; " +
		sudo + "rm -f /usr/local/bin/nkt-edge /etc/systemd/system/nkt-edge.service && " +
		sudo + "rm -rf /etc/nkt-edge /var/lib/nkt-edge /var/lib/private/nkt-edge && " +
		sudo + "systemctl daemon-reload && " + sudo + "systemctl reset-failed nkt-edge 2>/dev/null; " +
		"if command -v ufw >/dev/null && " + sudo + "ufw status | grep -q 'Status: active'; then " + sudo + "ufw delete allow " + EdgeTunnelPort + "/tcp >/dev/null 2>&1; fi; " +
		"test ! -e /usr/local/bin/nkt-edge"
	if out, err := runRemote(link.client, script); err != nil {
		return msgs.Errorf("edge.removeFailed", strings.TrimSpace(out))
	}
	jc.Log("edge.removed")
	jc.StepKey(3, 3, "edge.stepForget")
	if err := s.db.KVSet(ctx, edgeSettingsKey, "{}"); err != nil {
		return err
	}
	s.kickEdge()
	jc.Log("edge.portsKept")
	return nil
}
