package hub

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/edge"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
)

// Установка nkt-edge на VPS: проверка имени и портов, программа, файлы,
// сертификат certbot (standalone), служба, связь с хабом и проверка
// https://имя/healthz — всё одним заданием.

// EdgeUnit — systemd-юнит nkt-edge: системный пользователь nkt-edge (ему
// нужно читать копию сертификата в /etc/nkt-edge/tls), из прав — только
// привязка к 443, всё остальное закрыто.
const EdgeUnit = `[Unit]
Description=nkt-edge — webhooks for the nkt hub
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/nkt-edge
EnvironmentFile=/etc/nkt-edge/edge.env
User=nkt-edge
Group=nkt-edge
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

// EdgeCertbotHook — deploy-hook certbot: после выпуска и каждого продления
// сертификата домена вебхуков кладёт копию для службы nkt-edge (она
// перечитывает файлы сама), а за nginx — перезагружает nginx.
const EdgeCertbotHook = `#!/bin/sh
# certbot deploy-hook для nkt-edge: свежий сертификат домена вебхуков.
# Без прокси — копия в /etc/nkt-edge/tls для службы nkt-edge (она
# перечитывает файлы сама); за nginx — перезагрузка nginx.
set -e
env=/etc/nkt-edge/edge.env
domain=$(sed -n 's/^EDGE_DOMAIN=//p' "$env")
[ -n "$domain" ] && [ "$RENEWED_LINEAGE" = "/etc/letsencrypt/live/$domain" ] || exit 0
if grep -q '^EDGE_PROXY_ADDR=' "$env"; then
    if command -v nginx >/dev/null && nginx -t -q; then
        systemctl reload nginx
    fi
    exit 0
fi
install -d -m 0750 -g nkt-edge /etc/nkt-edge/tls
install -m 0640 -g nkt-edge "$RENEWED_LINEAGE/fullchain.pem" /etc/nkt-edge/tls/fullchain.pem
install -m 0640 -g nkt-edge "$RENEWED_LINEAGE/privkey.pem" /etc/nkt-edge/tls/privkey.pem
`

const (
	edgeHookPath = "/etc/nkt-edge/certbot-deploy.sh"
	edgeSteps    = 7
)

// Run: бинарник под архитектуру хоста, файлы, сертификат, служба,
// отпечаток туннеля, связь, проверка снаружи.
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
	jc.StepKey(1, edgeSteps, "edge.stepConnect", host.Name)
	link, err := s.hub.dialHost(ctx, host)
	if err != nil {
		return err
	}
	defer link.Close()
	client := link.client
	sudo := sudoPrefix(host.SSHUser)
	goos, goarch, err := detectTarget(client)
	if err != nil {
		return err
	}
	if err := checkAptHost(client); err != nil {
		return err
	}
	dns := s.checkEdgeDNS(ctx, client, host.Addr, p.Domain)
	if !dns.Match {
		if len(dns.DomainIPs) == 0 {
			return msgs.Errorf("edge.dnsMissing", p.Domain)
		}
		return msgs.Errorf("edge.dnsMismatch", p.Domain, strings.Join(dns.DomainIPs, ", "), strings.Join(dns.HostIPs, ", "))
	}
	jc.Log("edge.dnsOK", p.Domain, strings.Join(dns.DomainIPs, ", "))
	ports, err := edgePorts(client, sudo, p.ProxyPort)
	if err != nil {
		return err
	}

	jc.StepKey(2, edgeSteps, "edge.stepBinary", goos, goarch)
	bin, err := s.hub.ensureProgram(ctx, "nkt-edge", goos, goarch, report, report)
	if err != nil {
		return err
	}

	jc.StepKey(3, edgeSteps, "edge.stepFiles")
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
	envFile := fmt.Sprintf("EDGE_DOMAIN=%s\nEDGE_TOKEN=%s\nEDGE_TUNNEL_ADDR=:%s\nEDGE_GITHUB_ONLY=%t\n", p.Domain, token, EdgeTunnelPort, p.GitHubOnly)
	if p.ProxyPort > 0 {
		envFile += fmt.Sprintf("EDGE_PROXY_ADDR=127.0.0.1:%d\n", p.ProxyPort)
	}
	if out, err := runRemote(client, "id -u nkt-edge >/dev/null 2>&1 || "+sudo+"useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin nkt-edge"); err != nil {
		return msgs.Errorf("edge.userFailed", strings.TrimSpace(out))
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
	files := []struct {
		name, dst string
		data      []byte
		mode      uint32
	}{
		{"edge.env", "/etc/nkt-edge/edge.env", []byte(envFile), 0o600},
		{"nkt-edge.service", "/etc/systemd/system/nkt-edge.service", []byte(EdgeUnit), 0o644},
		{"certbot-deploy.sh", edgeHookPath, []byte(EdgeCertbotHook), 0o755},
	}
	for _, f := range files {
		if err := uploadBytes(sc, f.data, stage+"/"+f.name, 0o600); err != nil {
			return err
		}
	}
	if err := installRemoteFile(client, host.SSHUser, stage+"/nkt-edge", "/usr/local/bin/nkt-edge", 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if err := installRemoteFile(client, host.SSHUser, stage+"/"+f.name, f.dst, os.FileMode(f.mode)); err != nil {
			return err
		}
	}
	if out, err := runRemote(client, sudo+"chgrp nkt-edge /etc/nkt-edge && "+sudo+"chmod 0750 /etc/nkt-edge && "+sudo+"install -d -m 0750 -g nkt-edge /etc/nkt-edge/tls"); err != nil {
		return msgs.Errorf("edge.userFailed", strings.TrimSpace(out))
	}

	jc.StepKey(4, edgeSteps, "edge.stepCert", p.Domain)
	if err := edgeIssueCert(client, sudo, p, ports, jc); err != nil {
		return err
	}

	jc.StepKey(5, edgeSteps, "edge.stepService")
	if p.ProxyPort > 0 {
		// Свой веб-сервер на VPS хаб не правит — только подсказка.
		jc.Log("edge.proxySnippet", p.Domain, p.ProxyPort)
	}
	script := sudo + "systemctl daemon-reload && " + sudo + "systemctl enable nkt-edge && " + sudo + "systemctl restart nkt-edge"
	if out, err := runRemote(client, script); err != nil {
		return msgs.Errorf("edge.serviceFailed", strings.TrimSpace(out))
	}
	// restart проходит, даже если процесс падает через долю секунды, —
	// служба должна прожить несколько секунд тем же процессом.
	if err := edgeServiceStable(ctx, client, sudo); err != nil {
		return err
	}
	// Файрвол хоста, если ufw включён: порт туннеля, а без прокси — ещё
	// 443 и 80 (80 нужен certbot при продлении).
	allow := sudo + "ufw allow " + EdgeTunnelPort + "/tcp; "
	if p.ProxyPort == 0 {
		allow = sudo + "ufw allow 80/tcp; " + sudo + "ufw allow 443/tcp; " + allow
	}
	_, _ = runRemote(client, "if command -v ufw >/dev/null && "+sudo+"ufw status | grep -q 'Status: active'; then "+allow+"fi")

	jc.StepKey(6, edgeSteps, "edge.stepConnectHub")
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
	enc, err := secretbox.Encrypt(s.hub.key, []byte(token))
	if err != nil {
		return err
	}
	st = EdgeSettings{Enabled: true, Address: net.JoinHostPort(host.Addr, EdgeTunnelPort), Domain: p.Domain, TokenEnc: enc, CertPEM: certPEM, Fingerprint: fp, HostID: host.ID}
	if err := s.saveEdgeSettings(ctx, st); err != nil {
		return err
	}
	s.kickEdge()
	connected := false
	var last error
	for i := 0; i < 20 && !connected; i++ {
		if s.edge != nil {
			s.edge.mu.Lock()
			connected, last = s.edge.connected, s.edge.lastErr
			s.edge.mu.Unlock()
		}
		if !connected && !sleepCtx(ctx, time.Second) {
			return ctx.Err()
		}
	}
	if !connected {
		if last == nil {
			return msgs.Errorf("edge.notConnected", "timeout")
		}
		return msgs.Errorf("edge.notConnected", last)
	}
	jc.Log("edge.connected", p.Domain)

	jc.StepKey(7, edgeSteps, "edge.stepVerify", p.Domain)
	return edgeVerify(ctx, client, p.Domain, jc)
}

// edgePortsState — кто держит порты, которые нужны edge и certbot.
type edgePortsState struct {
	// Unit80 — служба systemd на порту 80: certbot standalone
	// останавливает её на время выпуска и продления.
	Unit80 string
}

var (
	ssProcRe   = regexp.MustCompile(`\("([^"]+)",pid=(\d+)`)
	unitNameRe = regexp.MustCompile(`^[A-Za-z0-9@._-]{1,120}\.service$`)
)

type portHolder struct {
	name, pid string
	// stray — nkt-edge, но не служба nkt-edge.service.
	stray bool
}

// portHolders — процессы, слушающие порт (кроме самого nkt-edge).
func portHolders(client *ssh.Client, sudo string, port int) []portHolder {
	out, err := runRemote(client, sudo+"ss -ltnpH 'sport = :"+strconv.Itoa(port)+"'")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	var hs []portHolder
	for _, m := range ssProcRe.FindAllStringSubmatch(out, -1) {
		if slices.ContainsFunc(hs, func(h portHolder) bool { return h.name == m[1] }) {
			continue
		}
		if m[1] == "nkt-edge" {
			// Своя служба — её перезапустит установка; посторонний
			// экземпляр (запущен вручную, остался от прежней установки)
			// занимал бы порты, и служба падала бы по кругу.
			unit, _ := runRemote(client, "ps -o unit= -p "+m[2])
			if strings.TrimSpace(unit) == "nkt-edge.service" {
				continue
			}
			hs = append(hs, portHolder{name: "nkt-edge", pid: m[2], stray: true})
			continue
		}
		hs = append(hs, portHolder{name: m[1], pid: m[2]})
	}
	if len(hs) == 0 && !strings.Contains(out, "nkt-edge") {
		hs = append(hs, portHolder{name: "?"})
	}
	return hs
}

func holderNames(hs []portHolder) string {
	names := make([]string, len(hs))
	for i, h := range hs {
		names[i] = h.name
	}
	return strings.Join(names, ", ")
}

// edgePorts — до установки: 8444 и (без прокси) 443 свободны, порт для
// прокси свободен; кто держит 80 — служба, которую certbot остановит.
func edgePorts(client *ssh.Client, sudo string, proxyPort int) (edgePortsState, error) {
	var st edgePortsState
	for _, port := range []int{80, 443, 8444, proxyPort} {
		if port == 0 {
			continue
		}
		for _, h := range portHolders(client, sudo, port) {
			if h.stray {
				return st, msgs.Errorf("edge.strayEdge", h.pid, port)
			}
		}
	}
	tunnel, _ := strconv.Atoi(EdgeTunnelPort)
	if hs := portHolders(client, sudo, tunnel); len(hs) > 0 {
		return st, msgs.Errorf("edge.portBusy", tunnel, holderNames(hs))
	}
	if proxyPort > 0 {
		if hs := portHolders(client, sudo, proxyPort); len(hs) > 0 {
			return st, msgs.Errorf("edge.portBusy", proxyPort, holderNames(hs))
		}
	} else if hs := portHolders(client, sudo, 443); len(hs) > 0 {
		return st, msgs.Errorf("edge.port443Busy", holderNames(hs))
	}
	if hs := portHolders(client, sudo, 80); len(hs) > 0 {
		h := hs[0]
		unit := ""
		if h.pid != "" {
			out, _ := runRemote(client, "ps -o unit= -p "+h.pid)
			unit = strings.TrimSpace(out)
		}
		if !unitNameRe.MatchString(unit) {
			return st, msgs.Errorf("edge.port80NotUnit", holderNames(hs))
		}
		st.Unit80 = unit
	}
	return st, nil
}

// edgeIssueCert — сертификат certbot standalone: certbot ставится, если
// его нет; служба на 80 останавливается на время выпуска (и продлений —
// хуки certbot запоминает); копию для nkt-edge кладёт deploy-hook.
func edgeIssueCert(client *ssh.Client, sudo string, p EdgeInstallParams, ports edgePortsState, jc *jobs.Context) error {
	if _, err := runRemote(client, "command -v certbot"); err != nil {
		jc.Log("edge.certbotInstall")
		if out, err := runRemote(client, sudo+"env DEBIAN_FRONTEND=noninteractive apt-get install -y -q certbot"); err != nil {
			return msgs.Errorf("edge.certbotInstallFailed", tailLines(out, 8))
		}
	}
	// Прежний edge (старые версии слушали 80) — не должен мешать certbot.
	_, _ = runRemote(client, sudo+"systemctl stop nkt-edge 2>/dev/null")
	args := []string{"certbot", "certonly", "--standalone", "--non-interactive", "--agree-tos",
		"--preferred-challenges", "http", "--keep-until-expiring",
		"--cert-name", p.Domain, "-d", p.Domain, "--deploy-hook", edgeHookPath}
	if p.Email != "" {
		args = append(args, "--email", p.Email)
	} else {
		args = append(args, "--register-unsafely-without-email")
	}
	if ports.Unit80 != "" {
		jc.Log("edge.certStopsUnit", ports.Unit80)
		args = append(args, "--pre-hook", "'systemctl stop "+ports.Unit80+"'", "--post-hook", "'systemctl start "+ports.Unit80+"'")
	}
	out, err := runRemote(client, sudo+strings.Join(args, " "))
	if err != nil {
		return msgs.Errorf("edge.certFailed", p.Domain, tailLines(out, 12))
	}
	// Уже действующий сертификат certbot не трогает, и hook не
	// срабатывает — копия кладётся явно.
	if out, err := runRemote(client, sudo+"env RENEWED_LINEAGE=/etc/letsencrypt/live/"+p.Domain+" "+edgeHookPath); err != nil {
		return msgs.Errorf("edge.certFailed", p.Domain, tailLines(out, 8))
	}
	if out, err := runRemote(client, sudo+"openssl x509 -enddate -noout -in /etc/letsencrypt/live/"+p.Domain+"/fullchain.pem"); err == nil {
		jc.Log("edge.certIssued", p.Domain, strings.TrimPrefix(strings.TrimSpace(out), "notAfter="))
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

// edgeVerify — https://имя/healthz с проверкой сертификата: сначала с
// хаба (как придёт GitHub), если хаб не выходит в интернет — с самого
// VPS через loopback (сертификат и служба, без проверки снаружи).
func edgeVerify(ctx context.Context, client *ssh.Client, domain string, jc *jobs.Context) error {
	url := "https://" + domain + "/healthz"
	c := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.Do(req)
	if err == nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			jc.Log("edge.verifiedOutside", url)
			return nil
		}
		return msgs.Errorf("edge.verifyFailed", url, fmt.Sprintf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	outside := err.Error()
	out, err := runRemote(client, "curl -sS --max-time 15 --resolve "+domain+":443:127.0.0.1 "+url)
	if err != nil {
		return msgs.Errorf("edge.verifyFailed", url, outside+"; "+strings.TrimSpace(out))
	}
	jc.Log("edge.verifiedLocal", url, outside)
	return nil
}

// tailLines — последние n строк вывода команды.
func tailLines(out string, n int) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// edgeDNSReport — куда указывает имя вебхуков и адреса VPS.
type edgeDNSReport struct {
	Domain    string   `json:"domain"`
	DomainIPs []string `json:"domain_ips"`
	HostIPs   []string `json:"host_ips"`
	Match     bool     `json:"match"`
}

// checkEdgeDNS — имя резолвится (с хаба, при неудаче — на VPS) и хотя бы
// один его адрес — адрес VPS: тот, по которому хаб ходит по SSH, или
// адрес на интерфейсе VPS.
func (s *Server) checkEdgeDNS(ctx context.Context, client *ssh.Client, hostAddr, domain string) edgeDNSReport {
	rep := edgeDNSReport{Domain: domain}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if addrs, err := net.DefaultResolver.LookupIPAddr(lctx, domain); err == nil {
		for _, a := range addrs {
			rep.DomainIPs = appendIP(rep.DomainIPs, a.IP.String())
		}
	} else if client != nil {
		out, _ := runRemote(client, "getent ahosts "+domain)
		for _, f := range strings.Fields(out) {
			rep.DomainIPs = appendIP(rep.DomainIPs, f)
		}
	}
	if ip := net.ParseIP(hostAddr); ip != nil {
		rep.HostIPs = appendIP(rep.HostIPs, ip.String())
	} else if addrs, err := net.DefaultResolver.LookupIPAddr(lctx, hostAddr); err == nil {
		for _, a := range addrs {
			rep.HostIPs = appendIP(rep.HostIPs, a.IP.String())
		}
	}
	if client != nil {
		out, _ := runRemote(client, "ip -o addr show scope global")
		for _, m := range regexp.MustCompile(`inet6? ([0-9a-fA-F:.]+)/`).FindAllStringSubmatch(out, -1) {
			rep.HostIPs = appendIP(rep.HostIPs, m[1])
		}
	}
	rep.Match = ipsOverlap(rep.DomainIPs, rep.HostIPs)
	return rep
}

// appendIP — добавить адрес (нормализованный), без повторов и не-адресов.
func appendIP(list []string, raw string) []string {
	ip := net.ParseIP(raw)
	if ip == nil {
		return list
	}
	v := ip.String()
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func ipsOverlap(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

// handleEdgeCheck — POST /hub/edge/check {host_id, domain}: для формы
// установки — указывает ли имя на этот хост.
func (s *Server) handleEdgeCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HostID int64  `json:"host_id"`
		Domain string `json:"domain"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !edgeDomainRe.MatchString(req.Domain) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "edge.badDomain", req.Domain))
		return
	}
	host, err := s.db.HostByID(r.Context(), req.HostID)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	var client *ssh.Client
	if link, err := s.hub.dialHost(r.Context(), host); err == nil {
		defer link.Close()
		client = link.client
	}
	writeJSON(w, http.StatusOK, s.checkEdgeDNS(r.Context(), client, host.Addr, req.Domain))
}

// Run удаления: служба, файлы, данные, сертификат certbot
// (если его выпускал edge — в его конфиге продления наш hook), системный
// пользователь, правило ufw для порта туннеля; затем хаб забывает edge.
// Правила 80 и 443 не трогаются — их может использовать другой сервер.
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
	domain := s.edgeSettings(ctx).Domain
	if domain != "" && edgeDomainRe.MatchString(domain) {
		renewal := "/etc/letsencrypt/renewal/" + domain + ".conf"
		if out, _ := runRemote(link.client, sudo+"grep -l nkt-edge "+renewal+" 2>/dev/null"); strings.TrimSpace(out) != "" {
			_, _ = runRemote(link.client, sudo+"certbot delete --non-interactive --cert-name "+domain)
			jc.Log("edge.certDeleted", domain)
		}
	}
	script := sudo + "systemctl disable --now nkt-edge 2>/dev/null; " +
		sudo + "rm -f /usr/local/bin/nkt-edge /etc/systemd/system/nkt-edge.service && " +
		sudo + "rm -rf /etc/nkt-edge /var/lib/nkt-edge /var/lib/private/nkt-edge && " +
		sudo + "systemctl daemon-reload && " + sudo + "systemctl reset-failed nkt-edge 2>/dev/null; " +
		"if id -u nkt-edge >/dev/null 2>&1; then " + sudo + "userdel nkt-edge; fi; " +
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
