// Package k8s — Kubernetes на хосте: установка ролей k3s и kubeadm,
// токен для присоединения, kubeconfig, узлы и поды. Каждая команда
// здесь — та же официальная процедура, что и в документации проекта,
// обёрнутая шагами с журналом; произвольных команд наружу не выдаётся,
// как и у установки Docker.
//
// Хаб создаёт машины и вызывает это через API каждой из них: сначала
// control plane, потом — worker'ы с токеном от него.
package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Flavor — чем ставится кластер.
const (
	FlavorK3s     = "k3s"
	FlavorKubeadm = "kubeadm"
)

// Role — роль узла.
const (
	RoleServer = "server" // control plane (у одиночного узла — он же worker)
	RoleAgent  = "agent"  // worker
)

// KindInstall — задание хоста: поставить роль.
const KindInstall = "k8s.install"

// InstallSpec — что ставить.
type InstallSpec struct {
	Flavor string `json:"flavor"`
	Role   string `json:"role"`
	// Single — одиночный узел: control plane без taint, чтобы поды
	// планировались на него.
	Single bool `json:"single,omitempty"`
	// ClusterInit — первый из нескольких control plane (k3s: встроенный
	// etcd; kubeadm: --control-plane-endpoint).
	ClusterInit bool `json:"cluster_init,omitempty"`
	// ServerURL и Token — для agent и дополнительных control plane.
	ServerURL string `json:"server_url,omitempty"`
	Token     string `json:"token,omitempty"`
	// CAHash — kubeadm: discovery-token-ca-cert-hash.
	CAHash string `json:"ca_hash,omitempty"`
	// CertKey — kubeadm: ключ для --control-plane у дополнительных CP.
	CertKey string `json:"cert_key,omitempty"`
	// TLSSANs — дополнительные имена/адреса в сертификате API (адрес
	// хоста при пробросе наружу).
	TLSSANs []string `json:"tls_sans,omitempty"`
	// ControlPlaneEndpoint — kubeadm HA: адрес балансировщика.
	ControlPlaneEndpoint string `json:"control_plane_endpoint,omitempty"`
	// NodeName — имя узла в кластере; пусто — hostname.
	NodeName string `json:"node_name,omitempty"`
}

// Validate — базовая проверка формы: имена, роли, отсутствие shell-мусора.
func (s InstallSpec) Validate() error {
	if s.Flavor != FlavorK3s && s.Flavor != FlavorKubeadm {
		return msgs.Errorf("k8s.badFlavor", s.Flavor)
	}
	if s.Role != RoleServer && s.Role != RoleAgent {
		return msgs.Errorf("k8s.badRole", s.Role)
	}
	if s.Role == RoleAgent && (s.ServerURL == "" || s.Token == "") {
		return msgs.Errorf("k8s.agentNeedsServer")
	}
	for _, v := range append([]string{s.ServerURL, s.Token, s.CAHash, s.CertKey, s.ControlPlaneEndpoint, s.NodeName}, s.TLSSANs...) {
		if strings.ContainsAny(v, " \t\n'\"`$\\;&|") {
			return msgs.Errorf("k8s.badValue", v)
		}
	}
	return nil
}

// Status — что стоит на хосте.
type Status struct {
	Installed bool   `json:"installed"`
	Flavor    string `json:"flavor,omitempty"`
	Role      string `json:"role,omitempty"`
	Version   string `json:"version,omitempty"`
	Active    bool   `json:"active"`
}

// Node — узел кластера по kubectl get nodes.
type Node struct {
	Name       string `json:"name"`
	Roles      string `json:"roles"`
	Ready      bool   `json:"ready"`
	Version    string `json:"version"`
	InternalIP string `json:"internal_ip"`
	Age        string `json:"age"`
}

// JoinInfo — что нужно worker'у, чтобы войти.
type JoinInfo struct {
	Flavor    string `json:"flavor"`
	ServerURL string `json:"server_url"`
	Token     string `json:"token"`
	CAHash    string `json:"ca_hash,omitempty"`
	CertKey   string `json:"cert_key,omitempty"`
}

// Manager — операции на хосте.
type Manager struct {
	c   collect.Collector
	run control.PrivilegedRunner
}

func New(c collect.Collector, run control.PrivilegedRunner) *Manager { return &Manager{c: c, run: run} }

// Status определяет, что установлено: по бинарникам и службам.
func (m *Manager) Status(ctx context.Context) Status {
	st := Status{}
	switch {
	case collect.Which(ctx, m.c, "k3s"):
		st.Installed, st.Flavor = true, FlavorK3s
		st.Role = RoleAgent
		if m.c.Exists("/etc/systemd/system/k3s.service") {
			st.Role = RoleServer
		}
		if out, err := m.c.Run(ctx, "k3s", "--version"); err == nil && out.OK() {
			st.Version = firstField(out.Stdout, 2)
		}
		unit := "k3s"
		if st.Role == RoleAgent {
			unit = "k3s-agent"
		}
		st.Active = m.active(ctx, unit)
	case collect.Which(ctx, m.c, "kubeadm"):
		st.Installed, st.Flavor = true, FlavorKubeadm
		st.Role = RoleAgent
		if m.c.Exists("/etc/kubernetes/admin.conf") {
			st.Role = RoleServer
		}
		if out, err := m.c.Run(ctx, "kubeadm", "version", "-o", "short"); err == nil && out.OK() {
			st.Version = strings.TrimSpace(out.Stdout)
		}
		st.Active = m.active(ctx, "kubelet")
	}
	return st
}

func (m *Manager) active(ctx context.Context, unit string) bool {
	out, err := m.c.Run(ctx, "systemctl", "is-active", unit)
	return err == nil && strings.TrimSpace(out.Stdout) == "active"
}

// kubectl — команда kubectl с kubeconfig control plane.
func (m *Manager) kubectl(ctx context.Context, args ...string) (collect.CommandResult, error) {
	st := m.Status(ctx)
	switch st.Flavor {
	case FlavorK3s:
		return m.c.Run(ctx, "k3s", append([]string{"kubectl"}, args...)...)
	case FlavorKubeadm:
		return m.c.Run(ctx, "kubectl", append([]string{"--kubeconfig", "/etc/kubernetes/admin.conf"}, args...)...)
	}
	return collect.CommandResult{}, msgs.Errorf("k8s.notInstalled")
}

// Nodes — узлы кластера (только на control plane).
func (m *Manager) Nodes(ctx context.Context) ([]Node, error) {
	out, err := m.kubectl(ctx, "get", "nodes", "-o", "json")
	if err != nil {
		return nil, err
	}
	if !out.OK() {
		return nil, msgs.Errorf("k8s.kubectl", strings.TrimSpace(out.Stderr))
	}
	return ParseNodes([]byte(out.Stdout))
}

// ParseNodes разбирает kubectl get nodes -o json.
func ParseNodes(raw []byte) ([]Node, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				Labels            map[string]string `json:"labels"`
				CreationTimestamp time.Time         `json:"creationTimestamp"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(list.Items))
	for _, it := range list.Items {
		n := Node{Name: it.Metadata.Name, Version: it.Status.NodeInfo.KubeletVersion}
		var roles []string
		for k := range it.Metadata.Labels {
			if strings.HasPrefix(k, "node-role.kubernetes.io/") {
				roles = append(roles, strings.TrimPrefix(k, "node-role.kubernetes.io/"))
			}
		}
		if len(roles) == 0 {
			roles = []string{"worker"}
		}
		n.Roles = strings.Join(sortStrings(roles), ",")
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" {
				n.Ready = c.Status == "True"
			}
		}
		for _, a := range it.Status.Addresses {
			if a.Type == "InternalIP" {
				n.InternalIP = a.Address
			}
		}
		if !it.Metadata.CreationTimestamp.IsZero() {
			n.Age = time.Since(it.Metadata.CreationTimestamp).Round(time.Minute).String()
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// Join — токен и адрес для присоединения новых узлов.
func (m *Manager) Join(ctx context.Context, serverAddr string) (JoinInfo, error) {
	st := m.Status(ctx)
	if !st.Installed || st.Role != RoleServer {
		return JoinInfo{}, msgs.Errorf("k8s.notServer")
	}
	switch st.Flavor {
	case FlavorK3s:
		raw, err := m.c.ReadFile("/var/lib/rancher/k3s/server/node-token")
		if err != nil {
			return JoinInfo{}, msgs.Errorf("k8s.tokenRead", err)
		}
		return JoinInfo{Flavor: FlavorK3s, ServerURL: "https://" + serverAddr + ":6443", Token: strings.TrimSpace(string(raw))}, nil
	case FlavorKubeadm:
		out, err := m.run(ctx, "kubeadm", "token", "create", "--print-join-command")
		if err != nil {
			return JoinInfo{}, err
		}
		if out.ExitCode != 0 {
			return JoinInfo{}, msgs.Errorf("k8s.tokenRead", strings.TrimSpace(out.Stderr))
		}
		info, err := ParseJoinCommand(out.Stdout)
		if err != nil {
			return JoinInfo{}, err
		}
		info.ServerURL = serverAddr + ":6443"
		// Сертификаты для дополнительных control plane — отдельно;
		// живут два часа, как и токен.
		if out, err := m.run(ctx, "kubeadm", "init", "phase", "upload-certs", "--upload-certs"); err == nil && out.ExitCode == 0 {
			lines := strings.Fields(out.Stdout)
			if len(lines) > 0 {
				info.CertKey = lines[len(lines)-1]
			}
		}
		return info, nil
	}
	return JoinInfo{}, msgs.Errorf("k8s.notInstalled")
}

var reJoin = regexp.MustCompile(`--token\s+(\S+)\s+--discovery-token-ca-cert-hash\s+(\S+)`)

// ParseJoinCommand достаёт токен и хэш из вывода kubeadm.
func ParseJoinCommand(out string) (JoinInfo, error) {
	m := reJoin.FindStringSubmatch(out)
	if m == nil {
		return JoinInfo{}, msgs.Errorf("k8s.tokenRead", "unexpected kubeadm output")
	}
	return JoinInfo{Flavor: FlavorKubeadm, Token: m[1], CAHash: m[2]}, nil
}

// Kubeconfig — конфиг администратора с подставленным адресом сервера.
func (m *Manager) Kubeconfig(ctx context.Context, serverAddr string) (string, error) {
	st := m.Status(ctx)
	var path string
	switch st.Flavor {
	case FlavorK3s:
		path = "/etc/rancher/k3s/k3s.yaml"
	case FlavorKubeadm:
		path = "/etc/kubernetes/admin.conf"
	default:
		return "", msgs.Errorf("k8s.notInstalled")
	}
	raw, err := m.c.ReadFile(path)
	if err != nil {
		// Файл читается только root — через runner.
		out, rerr := m.run(ctx, "cat", path)
		if rerr != nil || out.ExitCode != 0 {
			return "", msgs.Errorf("k8s.kubeconfigRead", err)
		}
		raw = []byte(out.Stdout)
	}
	cfg := string(raw)
	if serverAddr != "" {
		cfg = regexp.MustCompile(`server: https://[^\s]+`).ReplaceAllString(cfg, "server: https://"+serverAddr+":6443")
	}
	return cfg, nil
}

// Pods — поды всех namespace или одного.
func (m *Manager) Pods(ctx context.Context, namespace string) ([]byte, error) {
	args := []string{"get", "pods", "-o", "json"}
	if namespace == "" {
		args = append(args, "-A")
	} else {
		args = append(args, "-n", namespace)
	}
	out, err := m.kubectl(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !out.OK() {
		return nil, msgs.Errorf("k8s.kubectl", strings.TrimSpace(out.Stderr))
	}
	return []byte(out.Stdout), nil
}

// Uninstall убирает узел: официальные скрипты k3s или kubeadm reset.
func (m *Manager) Uninstall(ctx context.Context) error {
	st := m.Status(ctx)
	var script string
	switch st.Flavor {
	case FlavorK3s:
		script = "if [ -x /usr/local/bin/k3s-uninstall.sh ]; then /usr/local/bin/k3s-uninstall.sh; elif [ -x /usr/local/bin/k3s-agent-uninstall.sh ]; then /usr/local/bin/k3s-agent-uninstall.sh; fi"
	case FlavorKubeadm:
		script = "kubeadm reset -f; apt-get remove -y --purge kubeadm kubelet kubectl; rm -rf /etc/kubernetes /var/lib/etcd /etc/cni/net.d"
	default:
		return nil
	}
	out, err := m.run(ctx, "sh", "-c", script)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return msgs.Errorf("k8s.uninstall", strings.TrimSpace(out.Stderr))
	}
	return nil
}

// InstallRunner — задание хоста.
type InstallRunner struct{ m *Manager }

func NewInstallRunner(m *Manager) *InstallRunner { return &InstallRunner{m: m} }

// Run выполняет шаги установки роли.
func (r *InstallRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var spec InstallSpec
	if err := jc.Params(&spec); err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if r.m.run == nil {
		return msgs.Errorf("control.installationUnavailableMode")
	}
	steps := Steps(spec)
	for i, st := range steps {
		jc.Step(i+1, len(steps), msgs.T(jc.Lang(), st.Title))
		out, err := r.m.run(ctx, "sh", "-c", st.Script)
		if err != nil {
			return msgs.Errorf("k8s.stepFailed", msgs.T(jc.Lang(), st.Title), err)
		}
		for _, line := range tail(out.Output(), 8) {
			jc.Logf("      %s", line)
		}
		if out.ExitCode != 0 {
			return msgs.Errorf("k8s.stepFailed", msgs.T(jc.Lang(), st.Title), fmt.Sprintf("exit %d: %s", out.ExitCode, lastLine(out.Output())))
		}
	}
	jc.Log("k8s.installed", spec.Flavor, spec.Role)
	return nil
}

// Step — один шаг установки: заголовок (ключ msgs) и скрипт.
type Step struct {
	Title  string
	Script string
}

// Steps — последовательность для спецификации; чистая функция ради
// тестов и сухого прогона.
func Steps(s InstallSpec) []Step {
	switch s.Flavor {
	case FlavorK3s:
		return k3sSteps(s)
	default:
		return kubeadmSteps(s)
	}
}

func shq(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

func k3sSteps(s InstallSpec) []Step {
	var exec []string
	env := []string{"INSTALL_K3S_SKIP_SELINUX_RPM=true"}
	if s.Role == RoleServer {
		exec = append(exec, "server")
		if s.ClusterInit && s.ServerURL == "" {
			exec = append(exec, "--cluster-init")
		}
		if s.ServerURL != "" {
			// Дополнительный control plane входит по адресу первого.
			env = append(env, "K3S_URL="+shq(s.ServerURL))
		}
		for _, san := range s.TLSSANs {
			exec = append(exec, "--tls-san", san)
		}
		if !s.Single && s.Role == RoleServer {
			// Control plane многоузлового кластера не принимает рабочие
			// поды — как в документации k3s.
			exec = append(exec, "--node-taint", "node-role.kubernetes.io/control-plane:NoSchedule")
		}
	} else {
		exec = append(exec, "agent")
		env = append(env, "K3S_URL="+shq(s.ServerURL))
	}
	if s.Token != "" {
		env = append(env, "K3S_TOKEN="+shq(s.Token))
	}
	if s.NodeName != "" {
		exec = append(exec, "--node-name", s.NodeName)
	}
	unit := "k3s"
	if s.Role == RoleAgent {
		unit = "k3s-agent"
	}
	steps := []Step{
		{"k8s.step.prepare", "set -e\nexport DEBIAN_FRONTEND=noninteractive\napt-get update -qq\napt-get install -y -qq curl ca-certificates\nswapoff -a || true\nsed -i.bak '/\\sswap\\s/s/^/#/' /etc/fstab || true"},
		{"k8s.step.download", "set -e\ncurl -fsSL https://get.k3s.io -o /tmp/nkt-k3s-install.sh\nhead -c 200 /tmp/nkt-k3s-install.sh | grep -q '#!/bin/sh'"},
		{"k8s.step.install", "set -e\n" + strings.Join(env, " ") + " INSTALL_K3S_EXEC=" + shq(strings.Join(exec, " ")) + " sh /tmp/nkt-k3s-install.sh\nrm -f /tmp/nkt-k3s-install.sh"},
		{"k8s.step.wait", "set -e\nfor i in $(seq 1 60); do systemctl is-active --quiet " + unit + " && break; sleep 2; done\nsystemctl is-active --quiet " + unit},
	}
	if s.Role == RoleServer {
		steps = append(steps, Step{"k8s.step.ready", "set -e\nfor i in $(seq 1 90); do k3s kubectl get nodes 2>/dev/null | grep -q ' Ready' && exit 0; sleep 2; done\nk3s kubectl get nodes\nexit 1"})
	}
	return steps
}

func kubeadmSteps(s InstallSpec) []Step {
	const repo = "set -e\nexport DEBIAN_FRONTEND=noninteractive\napt-get update -qq\napt-get install -y -qq apt-transport-https ca-certificates curl gpg containerd\n" +
		"install -m 0755 -d /etc/apt/keyrings\ncurl -fsSL https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key | gpg --dearmor --yes -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg\n" +
		"echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.31/deb/ /' > /etc/apt/sources.list.d/kubernetes.list\n" +
		"apt-get update -qq\napt-get install -y -qq kubelet kubeadm kubectl\napt-mark hold kubelet kubeadm kubectl"
	const sysprep = "set -e\nswapoff -a || true\nsed -i.bak '/\\sswap\\s/s/^/#/' /etc/fstab || true\n" +
		"printf 'overlay\\nbr_netfilter\\n' > /etc/modules-load.d/k8s.conf\nmodprobe overlay\nmodprobe br_netfilter\n" +
		"printf 'net.bridge.bridge-nf-call-iptables=1\\nnet.bridge.bridge-nf-call-ip6tables=1\\nnet.ipv4.ip_forward=1\\n' > /etc/sysctl.d/k8s.conf\nsysctl --system >/dev/null\n" +
		"mkdir -p /etc/containerd\ncontainerd config default > /etc/containerd/config.toml\nsed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml\nsystemctl restart containerd\nsystemctl enable containerd"
	steps := []Step{{"k8s.step.prepare", sysprep}, {"k8s.step.download", repo}}
	if s.Role == RoleServer && s.ServerURL == "" {
		init := "set -e\nkubeadm init --pod-network-cidr=10.244.0.0/16"
		for _, san := range s.TLSSANs {
			init += " --apiserver-cert-extra-sans=" + san
		}
		if s.ControlPlaneEndpoint != "" {
			init += " --control-plane-endpoint=" + s.ControlPlaneEndpoint + " --upload-certs"
		}
		if s.NodeName != "" {
			init += " --node-name=" + s.NodeName
		}
		init += "\nmkdir -p /root/.kube\ncp -f /etc/kubernetes/admin.conf /root/.kube/config\n" +
			"kubectl --kubeconfig /etc/kubernetes/admin.conf apply -f https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml"
		if s.Single {
			init += "\nkubectl --kubeconfig /etc/kubernetes/admin.conf taint nodes --all node-role.kubernetes.io/control-plane- || true"
		}
		steps = append(steps, Step{"k8s.step.install", init})
		steps = append(steps, Step{"k8s.step.ready", "set -e\nfor i in $(seq 1 90); do kubectl --kubeconfig /etc/kubernetes/admin.conf get nodes 2>/dev/null | grep -q ' Ready' && exit 0; sleep 2; done\nexit 1"})
		return steps
	}
	join := "set -e\nkubeadm join " + s.ServerURL + " --token " + s.Token + " --discovery-token-ca-cert-hash " + s.CAHash
	if s.Role == RoleServer {
		join += " --control-plane --certificate-key " + s.CertKey
	}
	if s.NodeName != "" {
		join += " --node-name=" + s.NodeName
	}
	steps = append(steps, Step{"k8s.step.install", join})
	steps = append(steps, Step{"k8s.step.wait", "set -e\nfor i in $(seq 1 60); do systemctl is-active --quiet kubelet && break; sleep 2; done\nsystemctl is-active --quiet kubelet"})
	return steps
}

func firstField(s string, i int) string {
	f := strings.Fields(s)
	if len(f) < i {
		return ""
	}
	return f[i-1]
}

func sortStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}

func tail(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func lastLine(s string) string {
	t := tail(s, 1)
	if len(t) == 0 {
		return ""
	}
	return t[0]
}
