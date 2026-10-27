package k8s

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Дополнения кластера — то, что ставится поверх готового кластера:
// подготовка каждого узла (пакеты, службы) и чарт на первом control plane.
// Пока одно — Longhorn (блочные тома с репликами по узлам).
const (
	AddonLonghorn = "longhorn"
	// LonghornDefaultVersion — версия по умолчанию: её nkt ставит, когда
	// версия не выбрана, и она же — когда список версий недоступен.
	LonghornDefaultVersion = "1.12.1"
	LonghornRepoURL        = "https://charts.longhorn.io"
	LonghornNamespace      = "longhorn-system"
	// LonghornMinDiskGB — меньше диска на узле Longhorn почти бесполезен:
	// реплики томов лежат в /var/lib/longhorn на том же диске.
	LonghornMinDiskGB = 20
)

// Addons — известные дополнения.
var Addons = []string{AddonLonghorn}

var addonVersionRe = regexp.MustCompile(`^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)

// Действия AddonRequest.
const (
	AddonPrepare = "prepare"
	AddonInstall = "install"
)

// AddonRequest — POST /k8s/addon: подготовить узел или поставить
// дополнение на control plane.
type AddonRequest struct {
	Action string `json:"action"`
	Name   string `json:"name"`
	// Install: версия чарта, число реплик томов (1…3), сделать ли
	// StorageClass по умолчанию, обновление (прежние values сохраняются).
	Version  string `json:"version,omitempty"`
	Replicas int    `json:"replicas,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Upgrade  bool   `json:"upgrade,omitempty"`
	// Prepare на k3s-сервере: перестать вести local-path манифестом k3s,
	// иначе при перезапуске он вернёт ему метку «по умолчанию».
	SkipLocalPath bool `json:"skip_local_path,omitempty"`
}

// ValidAddon — известно ли дополнение.
func ValidAddon(name string) bool {
	for _, a := range Addons {
		if a == name {
			return true
		}
	}
	return false
}

// ValidAddonVersion — версия чарта вида 1.12.1.
func ValidAddonVersion(v string) bool { return addonVersionRe.MatchString(v) }

func (r AddonRequest) Validate() error {
	if !ValidAddon(r.Name) {
		return msgs.Errorf("k8s.addonUnknown", r.Name)
	}
	switch r.Action {
	case AddonPrepare:
		return nil
	case AddonInstall:
		if !ValidAddonVersion(r.Version) {
			return msgs.Errorf("k8s.addonBadVersion", r.Version)
		}
		if r.Replicas < 1 || r.Replicas > 3 {
			return msgs.Errorf("k8s.addonBadReplicas", r.Replicas)
		}
		return nil
	}
	return msgs.Errorf("k8s.addonBadAction", r.Action)
}

// AddonPrepareScript — подготовка узла под Longhorn: iSCSI (тома
// подключаются по нему), NFS-клиент (тома ReadWriteMany), модуль ядра.
// Текст — константа; skipLocalPath — только флаг.
func AddonPrepareScript(skipLocalPath bool) string {
	lines := []string{
		"set -e",
		"export DEBIAN_FRONTEND=noninteractive",
		"apt-get -o DPkg::Lock::Timeout=600 update -qq",
		"apt-get -o DPkg::Lock::Timeout=600 install -y -qq open-iscsi nfs-common",
		"modprobe iscsi_tcp",
		"echo iscsi_tcp > /etc/modules-load.d/nkt-longhorn.conf",
		"systemctl enable --now iscsid",
		"systemctl is-active --quiet iscsid",
		// multipathd забирает диски Longhorn себе — частая поломка. Чужой
		// multipath.conf не правим (на «железе» он может быть нужен), только
		// предупреждаем.
		`if systemctl is-active --quiet multipathd; then echo "warning: multipathd is running — if Longhorn volumes fail to attach, blacklist devnode \"^sd[a-z0-9]+\" in /etc/multipath.conf (see the Longhorn docs)"; fi`,
	}
	if skipLocalPath {
		lines = append(lines,
			"if [ -f /var/lib/rancher/k3s/server/manifests/local-storage.yaml ]; then touch /var/lib/rancher/k3s/server/manifests/local-storage.yaml.skip; fi")
	}
	lines = append(lines, "echo \"iscsiadm $(iscsiadm --version 2>/dev/null | awk '{print $3}')\"")
	return strings.Join(lines, "\n") + "\n"
}

// LonghornInstallScript — установка или обновление чарта Longhorn и
// ожидание готовности; helm — команда helm с настройками nkt без
// подкоманды (HelmArgv). Значения проверены Validate.
func LonghornInstallScript(flavor string, helm []string, r AddonRequest) string {
	kubeconfig := "/etc/kubernetes/admin.conf"
	if flavor == FlavorK3s {
		kubeconfig = "/etc/rancher/k3s/k3s.yaml"
	}
	quoted := make([]string, len(helm))
	for i, a := range helm {
		quoted[i] = shq(a)
	}
	// Флаги helm (--kubeconfig, --repository-config…) идут в HelmArgv после
	// аргументов — здесь подкоманда вставляется перед ними.
	h := func(args ...string) string {
		return quoted[0] + " " + strings.Join(args, " ") + " " + strings.Join(quoted[1:], " ")
	}
	install := []string{"upgrade", "--install", "longhorn", "longhorn/longhorn", "-n", LonghornNamespace, "--create-namespace",
		"--version", r.Version, "--wait", "--timeout", "15m"}
	if r.Upgrade {
		install = append(install, "--reuse-values")
	} else {
		install = append(install,
			"--set", "persistence.defaultClass="+strconv.FormatBool(r.Default),
			"--set", "persistence.defaultClassReplicaCount="+strconv.Itoa(r.Replicas))
	}
	lines := []string{
		"set -e",
		"export KUBECONFIG=" + kubeconfig,
		h("repo", "add", "longhorn", LonghornRepoURL, "--force-update"),
		h("repo", "update", "longhorn"),
		h(install...),
		"kubectl -n " + LonghornNamespace + " rollout status ds/longhorn-manager --timeout=10m",
		"for i in $(seq 1 60); do kubectl get storageclass longhorn >/dev/null 2>&1 && break; sleep 5; done",
	}
	if r.Default && !r.Upgrade {
		// Прежний класс по умолчанию (local-path у k3s) — больше не по
		// умолчанию: два таких класса — неоднозначность для PVC без класса.
		lines = append(lines,
			`for sc in $(kubectl get storageclass -o jsonpath='{range .items[*]}{.metadata.name}={.metadata.annotations.storageclass\.kubernetes\.io/is-default-class}{"\n"}{end}' | awk -F= '$2=="true"{print $1}'); do`,
			`  [ "$sc" = longhorn ] || kubectl patch storageclass "$sc" -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"false"}}}'`,
			"done")
	}
	lines = append(lines, "kubectl get storageclass")
	return strings.Join(lines, "\n") + "\n"
}

// AddonVersionCmp сравнивает версии вида 1.12.1 (-1, 0, 1).
func AddonVersionCmp(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3) {
		n, _ := strconv.Atoi(strings.SplitN(p, "-", 2)[0])
		out[i] = n
	}
	return out
}

// CheckLonghornUpgrade — правила обновления Longhorn: только вперёд и не
// больше чем на одну минорную ветку за раз (1.10 → 1.11 → 1.12).
func CheckLonghornUpgrade(cur, next string) error {
	if cur == "" {
		return nil
	}
	if AddonVersionCmp(next, cur) < 0 {
		return msgs.Errorf("k8s.addonDowngrade", cur, next)
	}
	c, n := versionParts(cur), versionParts(next)
	if n[0] != c[0] || n[1] > c[1]+1 {
		return msgs.Errorf("k8s.addonMinorStep", cur, next)
	}
	return nil
}

var kubeMinRe = regexp.MustCompile(`>=\s*v?([0-9]+)\.([0-9]+)`)

// KubeVersionOK — подходит ли версия Kubernetes («1.34», «v1.33.4+k3s1»)
// под ограничение чарта kubeVersion. Понимается только нижняя граница
// «>=X.Y»; что не разобрали — считается подходящим.
func KubeVersionOK(constraint, k8sVersion string) bool {
	m := kubeMinRe.FindStringSubmatch(constraint)
	if m == nil || strings.TrimSpace(k8sVersion) == "" {
		return true
	}
	v := versionParts(k8sVersion)
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return v[0] > maj || (v[0] == maj && v[1] >= min)
}
