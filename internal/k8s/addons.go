package k8s

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Дополнения кластера — то, что ставится поверх готового кластера:
// подготовка каждого узла (пакеты, службы — нужна не всем) и helm-чарт на
// первом control plane. Реестр — addonDefs: Longhorn (блочные тома с
// репликами по узлам), ingress-nginx, cert-manager, metrics-server,
// MetalLB, kube-prometheus-stack, Kyverno и Argo CD.
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

// Имена остальных дополнений реестра.
const (
	AddonIngressNginx        = "ingress-nginx"
	AddonCertManager         = "cert-manager"
	AddonMetricsServer       = "metrics-server"
	AddonMetallb             = "metallb"
	AddonKubePrometheusStack = "kube-prometheus-stack"
	AddonKyverno             = "kyverno"
	AddonArgoCD              = "argo-cd"
)

// AddonDef — запись реестра дополнений. Default — версия, проверенная nkt
// (её же хаб подставляет, когда версия не выбрана); репозиторий отдаёт и
// более новые — они видны в форме и окне дополнений.
type AddonDef struct {
	Name      string
	Chart     string // имя чарта в entries: index.yaml
	Repo      string // URL helm-репозитория
	RepoName  string // имя репозитория для helm repo add
	Default   string // версия по умолчанию (X.Y.Z)
	Namespace string
	Release   string
	// Storage — реплики томов и StorageClass по умолчанию (Longhorn).
	Storage bool
	// NodePrepare — узлы перед установкой готовятся (пакеты, службы).
	NodePrepare bool
	Packages    []string
	MinDiskGB   int
	// NoFlavor — вариант, где дополнение не ставится: уже встроено.
	NoFlavor string
	// StrictUpgrade — обновление только вперёд и не дальше следующей
	// минорной ветки (правило Longhorn); остальным downgrade запрещает
	// CheckAddonUpgrade и так.
	StrictUpgrade bool
	// ExtraSets — флаги --set свежей установки (обновление сохраняет
	// прежние values — --reuse-values).
	ExtraSets []string
}

// addonDefs — реестр дополнений. Версии Default проверены по живым
// index.yaml репозиториев (2026-11-12).
var addonDefs = []AddonDef{
	{
		Name: AddonLonghorn, Chart: "longhorn", Repo: LonghornRepoURL, RepoName: "longhorn",
		Default: LonghornDefaultVersion, Namespace: LonghornNamespace, Release: "longhorn",
		Storage: true, NodePrepare: true, Packages: []string{"open-iscsi", "nfs-common"},
		MinDiskGB: LonghornMinDiskGB, StrictUpgrade: true,
	},
	{
		// На голом железе без MetalLB сервис контроллера повис бы в
		// LoadBalancer/Pending — тип NodePort, как у ingress Cilium.
		Name: AddonIngressNginx, Chart: "ingress-nginx", Repo: "https://kubernetes.github.io/ingress-nginx",
		RepoName: "ingress-nginx", Default: "4.15.1", Namespace: "ingress-nginx", Release: "ingress-nginx",
		ExtraSets: []string{"controller.service.type=NodePort"},
	},
	{
		// CRD ставятся явно: без них ClusterIssuer/Certificate не создать.
		// Два ключа — новый (crds.enabled) и прежний (installCRDs): лишняя
		// values-пара helm не мешает, зато старая версия чарта тоже с CRD.
		Name: AddonCertManager, Chart: "cert-manager", Repo: "https://charts.jetstack.io",
		RepoName: "jetstack", Default: "1.21.3", Namespace: "cert-manager", Release: "cert-manager",
		ExtraSets: []string{"crds.enabled=true", "installCRDs=true"},
	},
	{
		// В k3s metrics-server встроен. На kubeadm kubelet отдаёт
		// самоподписанный сертификат без SAN адреса — без
		// --kubelet-insecure-tls метрики не собираются; список args
		// задаётся целиком: --set по индексу заменил бы весь список.
		Name: AddonMetricsServer, Chart: "metrics-server", Repo: "https://kubernetes-sigs.github.io/metrics-server",
		RepoName: "metrics-server", Default: "3.14.0", Namespace: "kube-system", Release: "metrics-server",
		NoFlavor: FlavorK3s,
		ExtraSets: []string{
			"args[0]=--cert-dir=/tmp",
			"args[1]=--secure-port=10250",
			"args[2]=--kubelet-preferred-address-types=InternalIP\\,ExternalIP\\,Hostname",
			"args[3]=--kubelet-use-node-status-port",
			"args[4]=--metric-resolution=15s",
			"args[5]=--kubelet-insecure-tls",
		},
	},
	{
		// Пул адресов (IPAddressPool + L2Advertisement) создаётся после
		// установки — значениями чарта он не задаётся.
		Name: AddonMetallb, Chart: "metallb", Repo: "https://metallb.github.io/metallb",
		RepoName: "metallb", Default: "0.16.2", Namespace: "metallb-system", Release: "metallb",
	},
	{
		Name: AddonKubePrometheusStack, Chart: "kube-prometheus-stack", Repo: "https://prometheus-community.github.io/helm-charts",
		RepoName: "prometheus-community", Default: "93.2.2", Namespace: "monitoring", Release: "kube-prometheus-stack",
	},
	{
		Name: AddonKyverno, Chart: "kyverno", Repo: "https://kyverno.github.io/kyverno",
		RepoName: "kyverno", Default: "3.10.0", Namespace: "kyverno", Release: "kyverno",
	},
	{
		Name: AddonArgoCD, Chart: "argo-cd", Repo: "https://argoproj.github.io/argo-helm",
		RepoName: "argo", Default: "10.18.0", Namespace: "argo-cd", Release: "argo-cd",
	},
}

// AddonDefOf — запись реестра по имени.
func AddonDefOf(name string) (AddonDef, bool) {
	for _, d := range addonDefs {
		if d.Name == name {
			return d, true
		}
	}
	return AddonDef{}, false
}

// Addons — известные дополнения (имена в порядке реестра).
var Addons = func() []string {
	out := make([]string, 0, len(addonDefs))
	for _, d := range addonDefs {
		out = append(out, d.Name)
	}
	return out
}()

// AddonNeedsPrepare — нужна ли дополнению подготовка узлов.
func AddonNeedsPrepare(name string) bool {
	d, ok := AddonDefOf(name)
	return ok && d.NodePrepare
}

// AddonFlavorConflict — flavor, с которым дополнение несовместимо (пусто —
// совместимо со всеми).
func AddonFlavorConflict(name string) string {
	d, _ := AddonDefOf(name)
	return d.NoFlavor
}

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
	// Install: версия чарта, число реплик томов (1…3, только Longhorn),
	// сделать ли StorageClass по умолчанию (только Longhorn), обновление
	// (прежние values сохраняются).
	Version  string `json:"version,omitempty"`
	Replicas int    `json:"replicas,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Upgrade  bool   `json:"upgrade,omitempty"`
	// ChartURL — адрес tarball чарта из index.yaml репозитория: хаб
	// подставляет его, чтобы шаг загрузки через кэш знал, что качать
	// (у каждого репозитория свой шаблон URL). Пусто — установка возьмёт
	// чарт из репозитория напрямую. Только https.
	ChartURL string `json:"chart_url,omitempty"`
	// Prepare на k3s-сервере: перестать вести local-path манифестом k3s,
	// иначе при перезапуске он вернёт ему метку «по умолчанию».
	SkipLocalPath bool `json:"skip_local_path,omitempty"`
}

// AddonPackages — пакеты дистрибутива, которые дополнение требует на
// каждом узле. Хаб ставит их cloud-init при первом запуске машины
// (vmcreate.Spec.Packages), а не шагом подготовки на готовом кластере: к
// тому моменту в дате-пас узла уже сидит Cilium с заменой kube-proxy, и
// в NAT-сети хоста TCP-соединения наружу могут не проходить — apt
// повиснет до таймаута задания, хотя ping и DNS остаются живыми.
func AddonPackages(name string) []string {
	d, _ := AddonDefOf(name)
	return d.Packages
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
	def, ok := AddonDefOf(r.Name)
	if !ok {
		return msgs.Errorf("k8s.addonUnknown", r.Name)
	}
	switch r.Action {
	case AddonPrepare:
		if !def.NodePrepare {
			return msgs.Errorf("k8s.addonNoPrepare", r.Name)
		}
		return nil
	case AddonInstall:
		if !ValidAddonVersion(r.Version) {
			return msgs.Errorf("k8s.addonBadVersion", r.Version)
		}
		if def.Storage && (r.Replicas < 1 || r.Replicas > 3) {
			return msgs.Errorf("k8s.addonBadReplicas", r.Replicas)
		}
		if r.ChartURL != "" {
			u, err := url.Parse(r.ChartURL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return msgs.Errorf("k8s.addonBadChartURL", r.ChartURL)
			}
		}
		return nil
	}
	return msgs.Errorf("k8s.addonBadAction", r.Action)
}

// AddonPrepareScript — подготовка узла под Longhorn: iSCSI (тома
// подключаются по нему), NFS-клиент (тома ReadWriteMany), модуль ядра.
// Текст — константа; skipLocalPath — только флаг.
//
// Пакеты обычно приходят с машиной из cloud-init (AddonPackages), так что
// apt здесь нужен лишь там, где их нет: узел-хост, машина прежнего
// кластера, образ без пакетов. Ходит он с короткими таймаутами и без
// повторов: на узле после Cilium исходящий TCP может молчать, и прежний
// «update -qq» с Lock::Timeout=600 вешал шаг на весь таймаут задания,
// тогда как ICMP и DNS оставались рабочими — по ним неисправность не
// увидеть.
func AddonPrepareScript(skipLocalPath bool) string {
	lines := []string{
		"set -e",
		"export DEBIAN_FRONTEND=noninteractive",
		"need=0",
		"for p in open-iscsi nfs-common; do dpkg -s $p >/dev/null 2>&1 || need=1; done",
		"if [ $need = 1 ]; then",
		"  if ! timeout 90 apt-get -o Acquire::Retries=0 -o Acquire::http::Timeout=8 -o Acquire::https::Timeout=8" +
			" -o DPkg::Lock::Timeout=60 update -qq; then",
		`    echo "apt update failed: this node has no working TCP egress (ICMP and DNS can still be fine). The packages normally arrive with the machine from cloud-init; after Cilium replaces kube-proxy check the node's outbound path, or install open-iscsi and nfs-common by hand." >&2`,
		"    exit 42",
		"  fi",
		"  apt-get -o DPkg::Lock::Timeout=600 install -y -qq open-iscsi nfs-common",
		"fi",
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

// LonghornChartURL — tarball чарта в GitHub releases: ровно тот файл, на
// который указывает urls: в index.yaml репозитория (проверено по живому
// индексу: шаблон един для всех 54 версий, что в нём лежат). Путь с
// /releases/download/<версия>/ кэш хаба считает неизменяемым, поэтому
// чарт ложится на хабе бессрочно и уходит в интернет один раз на все узлы.
func LonghornChartURL(version string) string {
	return "https://github.com/longhorn/charts/releases/download/longhorn-" + version + "/longhorn-" + version + ".tgz"
}

// AddonChartPath — где чарт лежит на control plane: каталог данных nkt,
// а не /tmp — файл с предсказуемым именем в общем каталоге подменили бы
// между загрузкой и установкой.
func AddonChartPath(name, version string) string {
	return "/var/lib/nkt/addons/" + name + "-" + version + ".tgz"
}

// LonghornChartPath — путь чарта Longhorn (прежний контракт).
func LonghornChartPath(version string) string { return AddonChartPath(AddonLonghorn, version) }

// AddonChartFetchScript — скачать чарт через кэш хаба (шаг опционален:
// не приехал — установка возьмёт чарт из репозитория, как раньше).
// chartURL — адрес tarball из index.yaml репозитория (проверен Validate:
// только https); пусто — качать нечего, шаг честно выходит нулём и
// установка берёт чарт из репозитория. Проверка tar -tzf обязательна:
// кэш при недоступном источнике отдаёт и устаревшую копию, а тело ответа
// «проверьте доступ» от curl с -f не отличается от настоящего файла.
func AddonChartFetchScript(hubCache, name, version, chartURL string) string {
	if chartURL == "" {
		return "echo \"no chart URL for " + name + "-" + version + " — the install step will take it from the repository\"\n"
	}
	dest := shq(AddonChartPath(name, version))
	part := shq(AddonChartPath(name, version) + ".part")
	return strings.Join([]string{
		strings.TrimRight(fetchPreludeCache(hubCache), "\n"),
		"set -u",
		"install -d -m 700 /var/lib/nkt/addons",
		"if tar -tzf " + dest + " >/dev/null 2>&1; then echo \"" + name + "-" + version + ".tgz is already on this node; not re-downloading\"; exit 0; fi",
		"art " + shq(chartURL) + " > " + part,
		"tar -tzf " + part + " >/dev/null",
		"mv " + part + " " + dest,
		"ls -l " + dest,
	}, "\n") + "\n"
}

// LonghornChartFetchScript — скачать чарт Longhorn через кэш хаба: URL
// у Longhorn выводится из версии (GitHub releases, шаблон един для всех
// версий репозитория).
func LonghornChartFetchScript(hubCache, version string) string {
	return AddonChartFetchScript(hubCache, AddonLonghorn, version, LonghornChartURL(version))
}

// LonghornInstallScript — установка или обновление чарта Longhorn и
// ожидание готовности; helm — команда helm с настройками nkt без
// подкоманды (HelmArgv), hubCache — адрес кэша хаба на этом узле (пусто —
// выхода через хаб нет и чарт берётся из репозитория напрямую). Значения
// проверены Validate.
func LonghornInstallScript(flavor string, helm []string, r AddonRequest, hubCache string) string {
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
	// Порядок флагов — прежний: установка по репозиторию обязана выглядеть
	// ровно как до кэха. version — то, что стоит на месте --version: два
	// флага либо ничего (скачанный tarball сам знает свою версию), либо
	// неразвёрнутая $vflags — выбор делает уже сценарий.
	flags := func(version ...string) []string {
		fl := append([]string{"-n", LonghornNamespace, "--create-namespace"}, version...)
		fl = append(fl, "--wait", "--timeout", "15m")
		if r.Upgrade {
			return append(fl, "--reuse-values")
		}
		return append(fl,
			"--set", "persistence.defaultClass="+strconv.FormatBool(r.Default),
			"--set", "persistence.defaultClassReplicaCount="+strconv.Itoa(r.Replicas))
	}
	install := append([]string{"upgrade", "--install", "longhorn", "longhorn/longhorn"}, flags("--version", r.Version)...)
	lines := []string{
		"set -e",
		"export KUBECONFIG=" + kubeconfig,
	}
	if hubCache == "" {
		// Кэша нет: прежний путь — репозиторий напрямую с узла.
		lines = append(lines,
			h("repo", "add", "longhorn", LonghornRepoURL, "--force-update"),
			h("repo", "update", "longhorn"),
			h(install...))
	} else {
		// Кэш есть: ставим скачанный tarball, а репозиторий остаётся
		// запасным путём — и он идёт под прокси хаба, только команды
		// репозитория: установка helm говорит с API кластера, и общий
		// HTTPS_PROXY украл бы у неё этот запрос (см. HelmProxyScript).
		proxy := "HTTPS_PROXY=" + shq(hubCache) + " HTTP_PROXY=" + shq(hubCache) + " "
		installLocal := quoted[0] + " upgrade --install longhorn \"$chart\" " +
			strings.Join(flags("$vflags"), " ") + " " + strings.Join(quoted[1:], " ")
		lines = append(lines,
			"chart=longhorn/longhorn",
			"vflags='--version "+r.Version+"'",
			"if [ -s "+shq(LonghornChartPath(r.Version))+" ]; then chart="+shq(LonghornChartPath(r.Version))+"; vflags=; fi",
			"if [ \"$chart\" = longhorn/longhorn ]; then",
			`  echo "chart did not arrive from the hub cache — taking longhorn/longhorn from the repository through the hub proxy" >&2`,
			"  "+proxy+h("repo", "add", "longhorn", LonghornRepoURL, "--force-update"),
			"  "+proxy+h("repo", "update", "longhorn"),
			"else",
			`  echo "installing the chart fetched through the hub cache: $chart"`,
			"fi",
			installLocal)
	}
	lines = append(lines,
		"kubectl -n "+LonghornNamespace+" rollout status ds/longhorn-manager --timeout=10m",
		"for i in $(seq 1 60); do kubectl get storageclass longhorn >/dev/null 2>&1 && break; sleep 5; done",
	)
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

// AddonInstallScript — скрипт установки или обновления дополнения:
// Longhorn сохраняет свой (реплики томов, StorageClass по умолчанию,
// ожидание готовности manager и класса), остальные дополнения реестра —
// общий путь (helm upgrade --install из кэша хаба или репозитория).
func AddonInstallScript(flavor string, helm []string, r AddonRequest, hubCache string) string {
	if r.Name == AddonLonghorn {
		return LonghornInstallScript(flavor, helm, r, hubCache)
	}
	return addonChartInstallScript(flavor, helm, r, hubCache)
}

// addonChartInstallScript — общий helm upgrade --install по записи
// реестра: тот же двойной путь, что у Longhorn (tarball из кэша хаба,
// запасной — репозиторий через прокси хаба), --reuse-values на
// обновлении и ExtraSets только на свежей установке. Значения проверены
// Validate.
func addonChartInstallScript(flavor string, helm []string, r AddonRequest, hubCache string) string {
	def, _ := AddonDefOf(r.Name)
	kubeconfig := "/etc/kubernetes/admin.conf"
	if flavor == FlavorK3s {
		kubeconfig = "/etc/rancher/k3s/k3s.yaml"
	}
	quoted := make([]string, len(helm))
	for i, a := range helm {
		quoted[i] = shq(a)
	}
	h := func(args ...string) string {
		return quoted[0] + " " + strings.Join(args, " ") + " " + strings.Join(quoted[1:], " ")
	}
	flags := func(version ...string) []string {
		fl := append([]string{"-n", def.Namespace, "--create-namespace"}, version...)
		fl = append(fl, "--wait", "--timeout", "15m")
		if r.Upgrade {
			return append(fl, "--reuse-values")
		}
		for _, s := range def.ExtraSets {
			fl = append(fl, "--set", shq(s))
		}
		return fl
	}
	chartRef := def.RepoName + "/" + def.Chart
	install := append([]string{"upgrade", "--install", def.Release, chartRef}, flags("--version", r.Version)...)
	lines := []string{
		"set -e",
		"export KUBECONFIG=" + kubeconfig,
	}
	if hubCache == "" {
		lines = append(lines,
			h("repo", "add", def.RepoName, def.Repo, "--force-update"),
			h("repo", "update", def.RepoName),
			h(install...))
	} else {
		proxy := "HTTPS_PROXY=" + shq(hubCache) + " HTTP_PROXY=" + shq(hubCache) + " "
		installLocal := quoted[0] + " upgrade --install " + def.Release + " \"$chart\" " +
			strings.Join(flags("$vflags"), " ") + " " + strings.Join(quoted[1:], " ")
		lines = append(lines,
			"chart="+shq(chartRef),
			"vflags='--version "+r.Version+"'",
			"if [ -s "+shq(AddonChartPath(r.Name, r.Version))+" ]; then chart="+shq(AddonChartPath(r.Name, r.Version))+"; vflags=; fi",
			"if [ \"$chart\" = "+shq(chartRef)+" ]; then",
			"  echo \"chart did not arrive from the hub cache — taking "+chartRef+" from the repository through the hub proxy\" >&2",
			"  "+proxy+h("repo", "add", def.RepoName, def.Repo, "--force-update"),
			"  "+proxy+h("repo", "update", def.RepoName),
			"else",
			"  echo \"installing the chart fetched through the hub cache: $chart\"",
			"fi",
			installLocal)
	}
	lines = append(lines, h("status", def.Release, "-n", def.Namespace))
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

// CheckAddonUpgrade — правила обновления дополнения: downgrade запрещён
// всем; Longhorn сверх того — только вперёд и не больше чем на одну
// минорную ветку за раз (1.10 → 1.11 → 1.12).
func CheckAddonUpgrade(name, cur, next string) error {
	if cur == "" {
		return nil
	}
	if AddonVersionCmp(next, cur) < 0 {
		return msgs.Errorf("k8s.addonDowngrade", cur, next)
	}
	def, _ := AddonDefOf(name)
	if !def.StrictUpgrade {
		return nil
	}
	c, n := versionParts(cur), versionParts(next)
	if n[0] != c[0] || n[1] > c[1]+1 {
		return msgs.Errorf("k8s.addonMinorStep", cur, next)
	}
	return nil
}

// CheckLonghornUpgrade — правила обновления Longhorn (прежний контракт).
func CheckLonghornUpgrade(cur, next string) error {
	return CheckAddonUpgrade(AddonLonghorn, cur, next)
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
