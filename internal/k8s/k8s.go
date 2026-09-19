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
	// CNI — «cilium»: кластер поднимается без flannel, а после старта
	// control plane ставится Cilium через его CLI. Пусто — как у варианта
	// по умолчанию (flannel).
	CNI string `json:"cni,omitempty"`
	// KubeProxyReplacement — Cilium вместо kube-proxy (eBPF).
	KubeProxyReplacement bool `json:"kube_proxy_replacement,omitempty"`
	// APIAddr — адрес control plane для Cilium при замене kube-proxy: без
	// kube-proxy агенту нужен прямой адрес API, а не ClusterIP.
	APIAddr string `json:"api_addr,omitempty"`
	// NodeIP — адрес узла для других узлов (и API у server): нужен, когда
	// у хоста несколько адресов, а видеть его должны через туннель.
	NodeIP string `json:"node_ip,omitempty"`
	// Version — минорная версия Kubernetes («1.36»): у kubeadm — ветка
	// репозитория pkgs.k8s.io (пусто — DefaultKubeadmVersion), у k3s —
	// канал v1.36 (пусто — stable). Хаб передаёт одну и ту же всем узлам
	// кластера.
	Version string `json:"version,omitempty"`
	// HubCache — адрес кэша хаба на этом хосте (http://127.0.0.1:3142),
	// когда хаб держит проброс: установщики, бинарники, ключи и образы
	// машин берутся через него (и оседают на хабе), а containerd тянет
	// образы контейнеров через зеркало registry на том же адресе. Пусто
	// — всё напрямую из интернета.
	HubCache string `json:"hub_cache,omitempty"`
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
	if s.CNI != "" && s.CNI != "cilium" {
		return msgs.Errorf("k8s.badCNI", s.CNI)
	}
	for _, v := range append([]string{s.ServerURL, s.Token, s.CAHash, s.CertKey, s.ControlPlaneEndpoint, s.NodeName, s.APIAddr, s.NodeIP, s.HubCache}, s.TLSSANs...) {
		if strings.ContainsAny(v, " \t\n'\"`$\\;&|") {
			return msgs.Errorf("k8s.badValue", v)
		}
	}
	if s.Version != "" && !kubeadmVersionRe.MatchString(s.Version) {
		return msgs.Errorf("k8s.badValue", s.Version)
	}
	return nil
}

// DefaultKubeadmVersion — ветка pkgs.k8s.io, когда хаб не смог узнать
// актуальную stable. Старые ветки (v1.31 и раньше) подписаны так, что apt
// на Debian 13 (sqv) их отвергает — «Signature Packet v3 is not
// considered secure».
const DefaultKubeadmVersion = "1.34"

var kubeadmVersionRe = regexp.MustCompile(`^\d+\.\d+$`)

// KubeadmRepo — репозиторий pkgs.k8s.io для минорной версии.
func KubeadmRepo(version string) string {
	if version == "" {
		version = DefaultKubeadmVersion
	}
	return "https://pkgs.k8s.io/core:/stable:/v" + version + "/deb"
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
		// «k3s version v1.31.4+k3s1 (a562d090)» — третье слово.
		if out, err := m.c.Run(ctx, "k3s", "--version"); err == nil && out.OK() {
			st.Version = firstField(out.Stdout, 3)
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
		if age := time.Since(it.Metadata.CreationTimestamp); !it.Metadata.CreationTimestamp.IsZero() && age > 0 {
			n.Age = humanAge(age)
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
		if !strings.Contains(serverAddr, ":") {
			serverAddr += ":6443"
		}
		cfg = regexp.MustCompile(`server: https://[^\s]+`).ReplaceAllString(cfg, "server: https://"+serverAddr)
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

// Resumable — да: сделанные шаги записаны.
func (r *InstallRunner) Resumable() bool { return true }

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
	// Продолжение: сделанные шаги пропускаются (все они и так
	// идемпотентны, но повторять apt и загрузки незачем).
	var done struct {
		Steps int `json:"steps"`
	}
	_ = jc.LoadResume(&done)
	steps := Steps(spec)
	for i, st := range steps {
		jc.StepKey(i+1, len(steps), st.Title)
		if i < done.Steps {
			jc.Log("k8s.stepSkipped")
			continue
		}
		out, err := r.m.run(ctx, "sh", "-c", st.Script)
		if err != nil {
			return msgs.Errorf("k8s.stepFailed", msgs.T(jc.Lang(), st.Title), err)
		}
		for _, line := range tail(out.Stdout, 8) {
			jc.Logf("      %s", line)
		}
		if out.ExitCode != 0 {
			// Ошибка apt/dpkg уходит в stderr, а stdout заканчивается
			// безобидным «Processing triggers…» — в журнал и в текст
			// ошибки идёт именно stderr.
			for _, line := range tail(out.Stderr, 12) {
				jc.Logf("      ! %s", line)
			}
			return msgs.Errorf("k8s.stepFailed", msgs.T(jc.Lang(), st.Title), fmt.Sprintf("exit %d: %s", out.ExitCode, failureLine(out)))
		}
		done.Steps = i + 1
		jc.SaveResume(done)
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
		if s.CNI == "cilium" {
			// Cilium ставится сам: встроенный flannel и сетевые политики
			// k3s выключаются, а с заменой kube-proxy — и он.
			exec = append(exec, "--flannel-backend=none", "--disable-network-policy")
			if s.KubeProxyReplacement {
				exec = append(exec, "--disable-kube-proxy")
			}
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
	if s.NodeIP != "" {
		exec = append(exec, "--node-ip", s.NodeIP)
		if s.Role == RoleServer {
			exec = append(exec, "--advertise-address", s.NodeIP)
		}
	}
	unit := "k3s"
	if s.Role == RoleAgent {
		unit = "k3s-agent"
	}
	download := fetchPrelude(s) + "art https://get.k3s.io > /tmp/nkt-k3s-install.sh\nhead -c 200 /tmp/nkt-k3s-install.sh | grep -q '#!/bin/sh'"
	installEnv := strings.Join(env, " ")
	// Канал k3s: stable или минорная ветка (v1.36) — та же «версия», что
	// у kubeadm задаёт ветку репозитория.
	channel := "stable"
	if s.Version != "" {
		channel = "v" + s.Version
		installEnv += " INSTALL_K3S_CHANNEL=" + channel
	}
	if s.HubCache != "" {
		// Через хаб: бинарник k3s и его airgap-образы берутся с кэша хаба
		// заранее, установщик их не качает; образы контейнеров — через
		// зеркало registry на хабе.
		download += "\n" + strings.Join([]string{
			"ARCH=$(uname -m); case $ARCH in x86_64) A=amd64; BIN=k3s;; aarch64) A=arm64; BIN=k3s-arm64;; *) echo \"unsupported arch $ARCH\"; exit 1;; esac",
			"VER=$(art https://update.k3s.io/v1-release/channels | grep -o '\"id\":\"" + channel + "\".\\{0,400\\}' | grep -o '\"latest\":\"[^\"]*\"' | head -1 | cut -d'\"' -f4)",
			"test -n \"$VER\"",
			"echo \"k3s $VER ($A) via hub cache\"",
			"REL=https://github.com/k3s-io/k3s/releases/download/$VER",
			"art $REL/sha256sum-$A.txt > /tmp/nkt-k3s.sums",
			"art $REL/$BIN > /tmp/nkt-k3s.bin",
			"echo \"$(grep \" $BIN$\" /tmp/nkt-k3s.sums | awk '{print $1}')  /tmp/nkt-k3s.bin\" | sha256sum -c - >/dev/null",
			"install -m 755 /tmp/nkt-k3s.bin /usr/local/bin/k3s && rm -f /tmp/nkt-k3s.bin",
			"mkdir -p /var/lib/rancher/k3s/agent/images",
			"art $REL/k3s-airgap-images-$A.tar.zst > /var/lib/rancher/k3s/agent/images/k3s-airgap-images-$A.tar.zst.part",
			"echo \"$(grep \" k3s-airgap-images-$A.tar.zst$\" /tmp/nkt-k3s.sums | awk '{print $1}')  /var/lib/rancher/k3s/agent/images/k3s-airgap-images-$A.tar.zst.part\" | sha256sum -c - >/dev/null",
			"mv -f /var/lib/rancher/k3s/agent/images/k3s-airgap-images-$A.tar.zst.part /var/lib/rancher/k3s/agent/images/k3s-airgap-images-$A.tar.zst",
			"mkdir -p /etc/rancher/k3s",
			"cat > /etc/rancher/k3s/registries.yaml <<'EOF'\n" + registriesYAML(s.HubCache) + "EOF",
		}, "\n")
		installEnv += " INSTALL_K3S_SKIP_DOWNLOAD=true"
	}
	steps := []Step{
		{"k8s.step.prepare", "set -e\nexport DEBIAN_FRONTEND=noninteractive\napt-get -o DPkg::Lock::Timeout=600 update -qq\napt-get -o DPkg::Lock::Timeout=600 install -y -qq curl ca-certificates\nswapoff -a || true\nsed -i.bak '/\\sswap\\s/s/^/#/' /etc/fstab || true"},
		{"k8s.step.download", download},
		{"k8s.step.install", "set -e\n" + installEnv + " INSTALL_K3S_EXEC=" + shq(strings.Join(exec, " ")) + " sh /tmp/nkt-k3s-install.sh\nrm -f /tmp/nkt-k3s-install.sh"},
		{"k8s.step.wait", "set -e\nfor i in $(seq 1 60); do systemctl is-active --quiet " + unit + " && break; sleep 2; done\nsystemctl is-active --quiet " + unit},
	}
	if s.Role == RoleServer {
		if s.CNI == "cilium" && s.ServerURL == "" {
			// Без CNI узел не станет Ready — Cilium раньше ожидания.
			steps = append(steps, ciliumStep(s, "/etc/rancher/k3s/k3s.yaml"))
		}
		steps = append(steps, Step{"k8s.step.ready", "set -e\nfor i in $(seq 1 90); do k3s kubectl get nodes 2>/dev/null | grep -q ' Ready' && exit 0; sleep 2; done\nk3s kubectl get nodes\nexit 1"})
	}
	return steps
}

// ciliumStep — установка Cilium официальным CLI: последняя стабильная
// версия, архив с GitHub с проверкой суммы, затем cilium install и
// ожидание готовности.
func ciliumStep(s InstallSpec, kubeconfig string) Step {
	install := "cilium install"
	if s.KubeProxyReplacement {
		install += " --set kubeProxyReplacement=true"
		if s.APIAddr != "" {
			install += " --set k8sServiceHost=" + s.APIAddr + " --set k8sServicePort=6443"
		}
	}
	script := strings.Join([]string{
		strings.TrimRight(fetchPrelude(s), "\n"),
		"export KUBECONFIG=" + kubeconfig,
		"if ! command -v cilium >/dev/null; then",
		"  ARCH=$(dpkg --print-architecture 2>/dev/null || uname -m); case $ARCH in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac",
		"  cd /tmp && art https://github.com/cilium/cilium-cli/releases/latest/download/cilium-linux-$ARCH.tar.gz > cilium-linux-$ARCH.tar.gz && art https://github.com/cilium/cilium-cli/releases/latest/download/cilium-linux-$ARCH.tar.gz.sha256sum > cilium-linux-$ARCH.tar.gz.sha256sum",
		"  sha256sum --check cilium-linux-$ARCH.tar.gz.sha256sum",
		"  tar -C /usr/local/bin -xzf cilium-linux-$ARCH.tar.gz && rm -f cilium-linux-$ARCH.tar.gz*",
		"fi",
		"cilium status --brief >/dev/null 2>&1 || " + install,
		"cilium status --wait --wait-duration 10m",
	}, "\n")
	return Step{"k8s.step.cilium", script}
}

// containerdStep ставит и настраивает containerd под kubeadm с оглядкой
// на то, что уже есть на машине. На виртуалке из облачного образа
// containerd нет — ставится пакет дистрибутива. На «железном» хосте он
// часто уже стоит: containerd.io от Docker (пакет `containerd` из
// дистрибутива с ним конфликтует — apt снёс бы Docker) или пакет
// дистрибутива; тогда пакет не трогается. Конфиг: у Docker'овского
// containerd.io в config.toml выключен CRI (disabled_plugins = ["cri"]),
// без него kubelet не заработает — такой конфиг заменяется на конфиг по
// умолчанию с копией в config.toml.nkt-bak (Docker с ним работает так
// же); свой конфиг с включённым CRI остаётся, в нём только включается
// SystemdCgroup — kubelet использует драйвер cgroup systemd. Перезапуск
// containerd не останавливает контейнеры Docker: shim'ы живут отдельно.
// /etc/default/kubelet пишется после установки пакета — иначе dpkg
// упёрся бы в чужой conffile.
func containerdStep(s InstallSpec) string {
	lines := []string{
		"set -e",
		"export DEBIAN_FRONTEND=noninteractive",
		"if command -v containerd >/dev/null 2>&1; then",
		"  echo \"containerd: already installed ($(containerd --version 2>/dev/null | head -1)), keeping the package\"",
		"else",
		"  apt-get -o DPkg::Lock::Timeout=600 install -y -qq containerd",
		"fi",
		"mkdir -p /etc/containerd",
		// Свой конфиг остаётся только если в нём уже настроен runc с
		// SystemdCgroup — иначе это заглушка дистрибутива или конфиг
		// Docker с выключенным CRI: без SystemdCgroup=true kubelet
		// (драйвер systemd) и containerd (cgroupfs) расходятся, поды
		// падают, API-сервер моргает, и kubeadm init не доживает до
		// последней фазы.
		"if [ -f /etc/containerd/config.toml ] && grep -q 'SystemdCgroup' /etc/containerd/config.toml && ! grep -Eq '^[[:space:]]*disabled_plugins[[:space:]]*=.*\"cri\"' /etc/containerd/config.toml; then",
		"  cp -a /etc/containerd/config.toml /etc/containerd/config.toml.nkt-bak",
		"  echo 'containerd: keeping the existing config.toml (runc options present), backup in config.toml.nkt-bak'",
		"else",
		"  [ -f /etc/containerd/config.toml ] && cp -a /etc/containerd/config.toml /etc/containerd/config.toml.nkt-bak && echo 'containerd: config.toml is a stub or has CRI disabled — writing the default config, backup in config.toml.nkt-bak'",
		"  containerd config default > /etc/containerd/config.toml",
		"fi",
		"sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml",
		"grep -q 'SystemdCgroup = true' /etc/containerd/config.toml",
		// Образ pause — тот, что ждёт kubeadm этой версии: иначе kubelet
		// держит две пары pause и ругается.
		"PAUSE=$(kubeadm config images list 2>/dev/null | grep '/pause:' | head -1); [ -n \"$PAUSE\" ] && sed -i \"s#sandbox_image = \\\"[^\\\"]*\\\"#sandbox_image = \\\"$PAUSE\\\"#\" /etc/containerd/config.toml",
		"systemctl enable containerd >/dev/null 2>&1 || true",
		"systemctl restart containerd",
		"for i in $(seq 1 30); do test -S /run/containerd/containerd.sock && break; sleep 1; done",
		"test -S /run/containerd/containerd.sock",
	}
	if s.HubCache != "" {
		// Зеркало registry на хабе: hosts.toml на каждый registry, сам
		// containerd добавляет ?ns=<registry> к запросам зеркала.
		lines = append(lines,
			"if grep -q 'config_path = \"\"' /etc/containerd/config.toml; then sed -i 's#config_path = \"\"#config_path = \"/etc/containerd/certs.d\"#' /etc/containerd/config.toml; elif ! grep -q 'config_path = \"/etc/containerd/certs.d\"' /etc/containerd/config.toml; then echo 'containerd: config.toml without registry config_path — hub registry mirror not enabled'; fi")
		for _, reg := range mirroredRegistries {
			lines = append(lines,
				"mkdir -p /etc/containerd/certs.d/"+reg,
				"printf 'server = \"https://"+registryServer(reg)+"\"\\n\\n[host.\""+s.HubCache+"\"]\\n  capabilities = [\"pull\", \"resolve\"]\\n' > /etc/containerd/certs.d/"+reg+"/hosts.toml")
		}
		lines = append(lines, "systemctl restart containerd")
	}
	if s.NodeIP != "" {
		lines = append(lines, "echo 'KUBELET_EXTRA_ARGS=--node-ip="+s.NodeIP+"' > /etc/default/kubelet")
	}
	return strings.Join(lines, "\n")
}

func kubeadmSteps(s InstallSpec) []Step {
	// Подготовка системы: swap, модули, sysctl. containerd здесь не
	// трогается — он ставится и настраивается отдельным шагом.
	const sysprep = "set -e\nswapoff -a || true\nsed -i.bak '/\\sswap\\s/s/^/#/' /etc/fstab || true\n" +
		"printf 'overlay\\nbr_netfilter\\n' > /etc/modules-load.d/k8s.conf\nmodprobe overlay\nmodprobe br_netfilter\n" +
		"printf 'net.bridge.bridge-nf-call-iptables=1\\nnet.bridge.bridge-nf-call-ip6tables=1\\nnet.ipv4.ip_forward=1\\n' > /etc/sysctl.d/k8s.conf\nsysctl --system >/dev/null"
	repo := fetchPrelude(s) + "export DEBIAN_FRONTEND=noninteractive\napt-get -o DPkg::Lock::Timeout=600 update -qq\napt-get -o DPkg::Lock::Timeout=600 install -y -qq apt-transport-https ca-certificates curl gpg\n" +
		"install -m 0755 -d /etc/apt/keyrings\nart " + KubeadmRepo(s.Version) + "/Release.key | gpg --dearmor --yes -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg\n" +
		"echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] " + KubeadmRepo(s.Version) + "/ /' > /etc/apt/sources.list.d/kubernetes.list\n" +
		"apt-get -o DPkg::Lock::Timeout=600 update -qq\napt-get -o DPkg::Lock::Timeout=600 install -y -qq kubelet kubeadm kubectl\napt-mark hold kubelet kubeadm kubectl"
	runtime := containerdStep(s)
	steps := []Step{{"k8s.step.sysprep", sysprep}, {"k8s.step.download", repo}, {"k8s.step.runtime", runtime}}
	if s.Role == RoleServer && s.ServerURL == "" {
		// Версия — своя, без похода в интернет за stable-1.txt (у узла
		// его может не быть, а ждать таймаут незачем). Остатки прошлой
		// неудачной попытки — снести reset'ом; живой control plane —
		// оставить.
		init := "set -e\nif [ -f /etc/kubernetes/admin.conf ]; then if kubectl --kubeconfig /etc/kubernetes/admin.conf get --raw=/healthz >/dev/null 2>&1; then echo 'kubeadm: control plane already initialized, skipping init'; SKIP_INIT=1; else echo 'kubeadm: leftovers of a failed init — resetting'; kubeadm reset -f >/dev/null 2>&1 || true; fi; fi\n" +
			"[ -n \"$SKIP_INIT\" ] || kubeadm init --kubernetes-version=$(kubeadm version -o short) --pod-network-cidr=10.244.0.0/16"
		if s.NodeIP != "" {
			init += " --apiserver-advertise-address=" + s.NodeIP
		}
		for _, san := range s.TLSSANs {
			init += " --apiserver-cert-extra-sans=" + san
		}
		if s.ControlPlaneEndpoint != "" {
			init += " --control-plane-endpoint=" + s.ControlPlaneEndpoint + " --upload-certs"
		}
		if s.NodeName != "" {
			init += " --node-name=" + s.NodeName
		}
		if s.CNI == "cilium" && s.KubeProxyReplacement {
			init += " --skip-phases=addon/kube-proxy"
		}
		init += "\nmkdir -p /root/.kube\ncp -f /etc/kubernetes/admin.conf /root/.kube/config"
		if s.CNI != "cilium" {
			init = fetchPrelude(s) + strings.TrimPrefix(init, "set -e\n") +
				"\nart https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml | kubectl --kubeconfig /etc/kubernetes/admin.conf apply -f -"
		}
		if s.Single {
			init += "\nkubectl --kubeconfig /etc/kubernetes/admin.conf taint nodes --all node-role.kubernetes.io/control-plane- || true"
		}
		steps = append(steps, Step{"k8s.step.install", init})
		if s.CNI == "cilium" {
			steps = append(steps, ciliumStep(s, "/etc/kubernetes/admin.conf"))
		}
		steps = append(steps, Step{"k8s.step.ready", "set -e\nfor i in $(seq 1 90); do kubectl --kubeconfig /etc/kubernetes/admin.conf get nodes 2>/dev/null | grep -q ' Ready' && exit 0; sleep 2; done\nexit 1"})
		return steps
	}
	join := "set -e\nif [ -f /etc/kubernetes/kubelet.conf ]; then if systemctl is-active --quiet kubelet; then echo 'kubeadm: node already joined, skipping join'; exit 0; fi; echo 'kubeadm: leftovers of a failed join — resetting'; kubeadm reset -f >/dev/null 2>&1 || true; fi\n" +
		"kubeadm join " + s.ServerURL + " --token " + s.Token + " --discovery-token-ca-cert-hash " + s.CAHash
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

// mirroredRegistries — registry, которые containerd берёт через зеркало
// хаба, когда есть кэш хаба.
var mirroredRegistries = []string{"docker.io", "quay.io", "registry.k8s.io", "ghcr.io", "gcr.io"}

func registryServer(reg string) string {
	if reg == "docker.io" {
		return "registry-1.docker.io"
	}
	return reg
}

// fetchPrelude — начало скрипта с функцией art URL: через кэш хаба
// (файл оседает на хабе) или напрямую curl'ом.
func fetchPrelude(s InstallSpec) string {
	if s.HubCache != "" {
		return "set -e\nNKT_CACHE=" + s.HubCache + "\nart() { curl -fsSL -G --data-urlencode \"url=$1\" \"$NKT_CACHE/nkt/artifact\"; }\n"
	}
	return "set -e\nart() { curl -fsSL \"$1\"; }\n"
}

// registriesYAML — зеркала для k3s (/etc/rancher/k3s/registries.yaml).
func registriesYAML(cache string) string {
	var b strings.Builder
	b.WriteString("mirrors:\n")
	for _, reg := range mirroredRegistries {
		b.WriteString("  " + reg + ":\n    endpoint:\n      - \"" + cache + "\"\n")
	}
	return b.String()
}

// humanAge — как у kubectl: 5d, 3h, 12m.
func humanAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
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

// failureLine — строка, объясняющая провал: «E: …» apt или последняя
// строка stderr, и только если stderr пуст — последняя строка stdout.
func failureLine(out collect.CommandResult) string {
	errLines := tail(out.Stderr, 50)
	for i := len(errLines) - 1; i >= 0; i-- {
		if strings.HasPrefix(errLines[i], "E: ") || strings.HasPrefix(errLines[i], "error:") || strings.HasPrefix(errLines[i], "Error:") {
			return errLines[i]
		}
	}
	if len(errLines) > 0 {
		return errLines[len(errLines)-1]
	}
	return lastLine(out.Stdout)
}

func lastLine(s string) string {
	t := tail(s, 1)
	if len(t) == 0 {
		return ""
	}
	return t[0]
}
