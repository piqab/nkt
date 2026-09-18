package control

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Туннель WireGuard между хостами кластера (режим «wireguard» раздела
// «Кластеры»). На каждом хосте — интерфейс wg-quick с адресом в общей
// сети туннеля и своя подсеть libvirt для машин; подсети машин соседей
// приходят через AllowedIPs, и wg-quick сам добавляет к ним маршруты.
// PostUp открывает UDP-порт, пропускает FORWARD через туннель и снимает
// маскарад libvirt для трафика между подсетями машин: узлы видят друг
// друга по настоящим адресам, а не по адресу хоста.

const wgDir = "/etc/wireguard"

// WGPeer — сосед по туннелю.
type WGPeer struct {
	Name       string   `json:"name"`
	PublicKey  string   `json:"public_key"`
	Endpoint   string   `json:"endpoint"`
	AllowedIPs []string `json:"allowed_ips"`
}

// WGMesh — конфигурация туннеля на одном хосте.
type WGMesh struct {
	Name       string   `json:"name"`
	PrivateKey string   `json:"private_key"`
	Address    string   `json:"address"` // адрес в сети туннеля, CIDR
	ListenPort int      `json:"listen_port"`
	VMSubnet   string   `json:"vm_subnet"` // подсеть машин этого хоста
	Peers      []WGPeer `json:"peers"`
}

// WGPeerStatus — что показывает wg show.
type WGPeerStatus struct {
	PublicKey     string `json:"public_key"`
	Endpoint      string `json:"endpoint,omitempty"`
	LastHandshake int64  `json:"last_handshake"`
	RxBytes       int64  `json:"rx_bytes"`
	TxBytes       int64  `json:"tx_bytes"`
}

// WGStatus — состояние туннеля на хосте.
type WGStatus struct {
	Installed bool           `json:"installed"`
	Up        bool           `json:"up"`
	Peers     []WGPeerStatus `json:"peers"`
}

// Имя интерфейса ограничено ядром 15 символами.
var wgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,14}$`)
var wgKeyRe = regexp.MustCompile(`^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw048]=$`)

// Validate проверяет форму: имена, ключи, адреса — всё попадает в файл и
// командную строку.
func (m WGMesh) Validate() error {
	if !wgNameRe.MatchString(m.Name) {
		return msgs.Errorf("control.wgBadName", m.Name)
	}
	if !wgKeyRe.MatchString(m.PrivateKey) {
		return msgs.Errorf("control.wgBadKey")
	}
	if _, _, err := net.ParseCIDR(m.Address); err != nil {
		return msgs.Errorf("control.wgBadAddress", m.Address)
	}
	if _, _, err := net.ParseCIDR(m.VMSubnet); err != nil {
		return msgs.Errorf("control.wgBadAddress", m.VMSubnet)
	}
	if m.ListenPort < 1 || m.ListenPort > 65535 {
		return msgs.Errorf("control.portForwardBadPort", m.ListenPort)
	}
	for _, p := range m.Peers {
		if !wgKeyRe.MatchString(p.PublicKey) {
			return msgs.Errorf("control.wgBadKey")
		}
		host, port, err := net.SplitHostPort(p.Endpoint)
		if err != nil || host == "" || strings.ContainsAny(host, " \t\n'\"`$\\;&|[]") {
			return msgs.Errorf("control.wgBadEndpoint", p.Endpoint)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return msgs.Errorf("control.wgBadEndpoint", p.Endpoint)
		}
		if len(p.AllowedIPs) == 0 {
			return msgs.Errorf("control.wgBadAddress", "")
		}
		for _, a := range p.AllowedIPs {
			if _, _, err := net.ParseCIDR(a); err != nil {
				return msgs.Errorf("control.wgBadAddress", a)
			}
		}
	}
	return nil
}

// Config — файл wg-quick.
func (m WGMesh) Config() string {
	var b strings.Builder
	b.WriteString("# nkt: туннель между хостами кластера; генерируется автоматически.\n[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\nAddress = %s\nListenPort = %d\n", m.PrivateKey, m.Address, m.ListenPort)
	var up, down []string
	rule := func(table, args string) {
		up = append(up, "iptables "+table+"-I "+args)
		down = append(down, "iptables "+table+"-D "+args+" 2>/dev/null || true")
	}
	rule("", fmt.Sprintf("INPUT -p udp --dport %d -j ACCEPT", m.ListenPort))
	rule("", "FORWARD -i %i -j ACCEPT")
	rule("", "FORWARD -o %i -j ACCEPT")
	// Между подсетями машин и сетью туннеля — без маскарада libvirt.
	if _, n, err := net.ParseCIDR(m.Address); err == nil {
		rule("-t nat ", fmt.Sprintf("POSTROUTING -s %s -d %s -j RETURN", m.VMSubnet, n.String()))
	}
	for _, p := range m.Peers {
		for _, a := range p.AllowedIPs {
			if ip, _, err := net.ParseCIDR(a); err == nil && !strings.HasSuffix(a, "/32") && ip.To4() != nil {
				rule("-t nat ", fmt.Sprintf("POSTROUTING -s %s -d %s -j RETURN", m.VMSubnet, a))
			}
		}
	}
	up = append([]string{"sysctl -qw net.ipv4.ip_forward=1"}, up...)
	fmt.Fprintf(&b, "PostUp = %s\nPostDown = %s\n", strings.Join(up, "; "), strings.Join(down, "; "))
	for _, p := range m.Peers {
		fmt.Fprintf(&b, "\n[Peer]\n# %s\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = %s\nPersistentKeepalive = 25\n",
			p.Name, p.PublicKey, p.Endpoint, strings.Join(p.AllowedIPs, ", "))
	}
	return b.String()
}

// WGManager применяет и снимает туннели.
type WGManager struct {
	run PrivilegedRunner
}

func NewWGManager(run PrivilegedRunner) *WGManager { return &WGManager{run: run} }

// Apply ставит wireguard-tools (если нет), пишет конфиг и поднимает
// интерфейс заново — конфиг мог измениться.
func (m *WGManager) Apply(ctx context.Context, mesh WGMesh) error {
	if m.run == nil {
		return msgs.Errorf("control.installationUnavailableMode")
	}
	if err := mesh.Validate(); err != nil {
		return err
	}
	install := "command -v wg >/dev/null 2>&1 || { export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq wireguard-tools; }"
	if out, err := m.run(ctx, "sh", "-c", install); err != nil || out.ExitCode != 0 {
		return msgs.Errorf("control.wgApply", "apt-get", lastErr(out.Output(), err))
	}
	enc := base64.StdEncoding.EncodeToString([]byte(mesh.Config()))
	conf := path.Join(wgDir, mesh.Name+".conf")
	write := fmt.Sprintf("install -d -m 700 %s && printf '%%s' %s | base64 -d > %s.tmp && chmod 600 %s.tmp && mv -f %s.tmp %s",
		wgDir, enc, conf, conf, conf, conf)
	if out, err := m.run(ctx, "sh", "-c", write); err != nil || out.ExitCode != 0 {
		return msgs.Errorf("control.wgApply", conf, lastErr(out.Output(), err))
	}
	up := fmt.Sprintf("systemctl enable wg-quick@%s >/dev/null 2>&1; wg-quick down %s >/dev/null 2>&1; wg-quick up %s", mesh.Name, mesh.Name, mesh.Name)
	if out, err := m.run(ctx, "sh", "-c", up); err != nil || out.ExitCode != 0 {
		return msgs.Errorf("control.wgApply", "wg-quick up", lastErr(out.Output(), err))
	}
	return nil
}

// Remove гасит интерфейс и убирает конфиг.
func (m *WGManager) Remove(ctx context.Context, name string) error {
	if m.run == nil {
		return msgs.Errorf("control.installationUnavailableMode")
	}
	if !wgNameRe.MatchString(name) {
		return msgs.Errorf("control.wgBadName", name)
	}
	cmd := fmt.Sprintf("wg-quick down %s >/dev/null 2>&1; systemctl disable wg-quick@%s >/dev/null 2>&1; rm -f %s", name, name, path.Join(wgDir, name+".conf"))
	if out, err := m.run(ctx, "sh", "-c", cmd); err != nil || out.ExitCode != 0 {
		return msgs.Errorf("control.wgApply", "wg-quick down", lastErr(out.Output(), err))
	}
	return nil
}

// Status — стоит ли wg, поднят ли интерфейс и что с рукопожатиями.
func (m *WGManager) Status(ctx context.Context, name string) (WGStatus, error) {
	st := WGStatus{Peers: []WGPeerStatus{}}
	if m.run == nil {
		return st, msgs.Errorf("control.installationUnavailableMode")
	}
	if !wgNameRe.MatchString(name) {
		return st, msgs.Errorf("control.wgBadName", name)
	}
	out, err := m.run(ctx, "sh", "-c", "command -v wg >/dev/null 2>&1 && echo installed; wg show "+name+" dump 2>/dev/null")
	if err != nil {
		return st, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out.Stdout), "\n") {
		if line == "installed" {
			st.Installed = true
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		if !st.Up {
			// Первая строка dump — сам интерфейс.
			st.Up = true
			continue
		}
		if len(f) < 7 {
			continue
		}
		p := WGPeerStatus{PublicKey: f[0], Endpoint: f[2]}
		if p.Endpoint == "(none)" {
			p.Endpoint = ""
		}
		p.LastHandshake, _ = strconv.ParseInt(f[4], 10, 64)
		p.RxBytes, _ = strconv.ParseInt(f[5], 10, 64)
		p.TxBytes, _ = strconv.ParseInt(f[6], 10, 64)
		st.Peers = append(st.Peers, p)
	}
	return st, nil
}

func lastErr(output string, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("%s", lastLineOf(output))
}
