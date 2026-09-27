package k8s

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Обновление Kubernetes на узле — задание хоста со сценарием:
//
//	k3s     — бинарник нужной версии (последний выпуск канала v1.X) вместо
//	          /usr/local/bin/k3s и перезапуск службы: флаги установки в
//	          юните остаются как были (повторный запуск установщика без
//	          них поставил бы службу заново с настройками по умолчанию);
//	kubeadm — ветка репозитория pkgs.k8s.io v1.X, новый kubeadm, затем
//	          «kubeadm upgrade apply» на первом control plane или «kubeadm
//	          upgrade node» на остальных, затем kubelet и kubectl.
//
// kubeadm поднимается не больше чем на одну минорную версию за раз — это
// его собственное правило, проверяется до запуска. Порядок по узлам
// (control plane первыми, drain и uncordon) держит хаб, у одиночного узла
// — сам оператор.

// UpgradeSpec — параметры обновления узла.
type UpgradeSpec struct {
	// Version — минорная версия («1.35»).
	Version string `json:"version"`
	// First — первый control plane (kubeadm upgrade apply).
	First bool `json:"first"`
	// HubCache — кэш хаба на хосте (как у установки).
	HubCache string `json:"hub_cache,omitempty"`
}

// UpgradeInfo — ответ GET /k8s/upgrade.
type UpgradeInfo struct {
	Flavor   string   `json:"flavor"`
	Role     string   `json:"role"`
	Version  string   `json:"version"`  // версия этого узла
	Minor    string   `json:"minor"`    // её минорная часть
	Channels []string `json:"channels"` // доступные минорные версии
}

var (
	minorRe        = regexp.MustCompile(`v?(\d+)\.(\d+)`)
	k3sChannelIDRe = regexp.MustCompile(`"id":\s*"v(\d+\.\d+)"`)
)

// MinorOf — «v1.31.4+k3s1» → «1.31».
func MinorOf(v string) string {
	m := minorRe.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

// minorParts — «1.31» → 1, 31.
func minorParts(v string) (int, int, bool) {
	m := minorRe.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a, b, true
}

// Upgrade — что стоит на узле и куда можно обновиться: у k3s — каналы с
// update.k3s.io не ниже текущей версии, у kubeadm — текущая и следующая
// минорные.
func (m *Manager) Upgrade(ctx context.Context) (UpgradeInfo, error) {
	st := m.Status(ctx)
	if !st.Installed {
		return UpgradeInfo{}, msgs.Errorf("k8s.notInstalled")
	}
	info := UpgradeInfo{Flavor: st.Flavor, Role: st.Role, Version: st.Version, Minor: MinorOf(st.Version), Channels: []string{}}
	major, minor, ok := minorParts(info.Minor)
	if !ok {
		return info, nil
	}
	switch st.Flavor {
	case FlavorKubeadm:
		info.Channels = []string{info.Minor, strconv.Itoa(major) + "." + strconv.Itoa(minor+1)}
	case FlavorK3s:
		if out, err := m.c.Run(ctx, "curl", "-fsSL", "--max-time", "15", "https://update.k3s.io/v1-release/channels"); err == nil && out.OK() {
			seen := map[string]bool{}
			for _, mm := range k3sChannelIDRe.FindAllStringSubmatch(out.Stdout, -1) {
				if a, b, ok := minorParts(mm[1]); ok && (a > major || (a == major && b >= minor)) && !seen[mm[1]] {
					seen[mm[1]] = true
					info.Channels = append(info.Channels, mm[1])
				}
			}
		}
		if len(info.Channels) == 0 {
			info.Channels = []string{info.Minor}
		}
	}
	return info, nil
}

// Validate — версия и допустимый шаг для kubeadm (current — версия узла).
func (u UpgradeSpec) Validate(flavor, current string) error {
	if !kubeadmVersionRe.MatchString(u.Version) {
		return msgs.Errorf("k8s.badValue", u.Version)
	}
	if strings.ContainsAny(u.HubCache, " \t\n'\"`$\\;&|") {
		return msgs.Errorf("k8s.badValue", u.HubCache)
	}
	a, b, _ := minorParts(u.Version)
	ca, cb, ok := minorParts(current)
	if !ok {
		return nil
	}
	if a < ca || (a == ca && b < cb) {
		return msgs.Errorf("k8s.upgradeDowngrade", u.Version, MinorOf(current))
	}
	if flavor == FlavorKubeadm && (a != ca || b > cb+1) {
		return msgs.Errorf("k8s.upgradeOneMinor", MinorOf(current))
	}
	return nil
}

// UpgradeScript — сценарий обновления узла.
func UpgradeScript(flavor, role string, u UpgradeSpec) string {
	prelude := fetchPrelude(InstallSpec{HubCache: u.HubCache})
	if flavor == FlavorK3s {
		unit := "k3s"
		if role == RoleAgent {
			unit = "k3s-agent"
		}
		return prelude + strings.Join([]string{
			"ARCH=$(uname -m); case $ARCH in x86_64) A=amd64; BIN=k3s;; aarch64) A=arm64; BIN=k3s-arm64;; armv7l) A=arm; BIN=k3s-armhf;; *) echo \"unsupported arch $ARCH\"; exit 1;; esac",
			"VER=$(art https://update.k3s.io/v1-release/channels | grep -o '\"id\":\"v" + u.Version + "\".\\{0,400\\}' | grep -o '\"latest\":\"[^\"]*\"' | head -1 | cut -d'\"' -f4)",
			"test -n \"$VER\" || { echo 'no k3s release in channel v" + u.Version + "'; exit 1; }",
			"echo \"k3s: $(k3s --version | head -1) -> $VER\"",
			"REL=https://github.com/k3s-io/k3s/releases/download/$VER",
			"art $REL/sha256sum-$A.txt > /tmp/nkt-k3s.sums",
			"art $REL/$BIN > /tmp/nkt-k3s.bin",
			"echo \"$(grep \" $BIN$\" /tmp/nkt-k3s.sums | awk '{print $1}')  /tmp/nkt-k3s.bin\" | sha256sum -c - >/dev/null",
			"install -m 755 /tmp/nkt-k3s.bin /usr/local/bin/k3s && rm -f /tmp/nkt-k3s.bin /tmp/nkt-k3s.sums",
			"systemctl restart " + unit,
			"sleep 5; systemctl is-active " + unit,
			"k3s --version | head -1",
		}, "\n")
	}
	repo := KubeadmRepo(u.Version)
	upgrade := "kubeadm upgrade node"
	if u.First {
		upgrade = "kubeadm upgrade apply -y \"$(kubeadm version -o short)\""
	}
	return prelude + strings.Join([]string{
		"install -m 0755 -d /etc/apt/keyrings",
		"art " + repo + "/Release.key | gpg --dearmor --yes -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg",
		"echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] " + repo + "/ /' > /etc/apt/sources.list.d/kubernetes.list",
		"apt-get -o DPkg::Lock::Timeout=600 update -qq",
		"apt-mark unhold kubeadm kubelet kubectl",
		"apt-get -o DPkg::Lock::Timeout=600 install -y -qq kubeadm",
		"kubeadm version -o short",
		upgrade,
		"apt-get -o DPkg::Lock::Timeout=600 install -y -qq kubelet kubectl",
		"apt-mark hold kubeadm kubelet kubectl",
		"systemctl daemon-reload",
		"systemctl restart kubelet",
		"sleep 5; systemctl is-active kubelet",
	}, "\n")
}

// NodeVersions — версии kubelet узлов кластера (с control plane).
func (m *Manager) NodeVersions(ctx context.Context) (map[string]string, error) {
	nodes, err := m.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, n := range nodes {
		out[n.Name] = n.Version
	}
	return out, nil
}
