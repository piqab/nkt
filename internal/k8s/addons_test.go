package k8s

import (
	"strings"
	"testing"
)

func TestAddonRequestValidate(t *testing.T) {
	ok := []AddonRequest{
		{Action: AddonPrepare, Name: AddonLonghorn},
		{Action: AddonInstall, Name: AddonLonghorn, Version: "1.12.1", Replicas: 3},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []AddonRequest{
		{Action: AddonPrepare, Name: "rook"},
		{Action: "run", Name: AddonLonghorn},
		{Action: AddonInstall, Name: AddonLonghorn, Version: "1.12.1; rm -rf /", Replicas: 1},
		{Action: AddonInstall, Name: AddonLonghorn, Version: "1.12.1-rc1", Replicas: 1},
		{Action: AddonInstall, Name: AddonLonghorn, Version: "1.12.1", Replicas: 4},
		{Action: AddonInstall, Name: AddonLonghorn, Version: "1.12.1", Replicas: 0},
	}
	for _, r := range bad {
		if r.Validate() == nil {
			t.Errorf("%+v: accepted", r)
		}
	}
}

func TestLonghornScripts(t *testing.T) {
	helm := []string{"helm", "--kubeconfig", "/etc/rancher/k3s/k3s.yaml", "--repository-config", "/var/lib/nkt/helm/repositories.yaml"}
	// Без кэша хаба — прежний путь: репозиторий напрямую с узла.
	s := LonghornInstallScript(FlavorK3s, helm, AddonRequest{Name: AddonLonghorn, Version: "1.12.1", Replicas: 1, Default: true}, "")
	for _, want := range []string{
		"'helm' upgrade --install longhorn longhorn/longhorn -n longhorn-system --create-namespace --version 1.12.1",
		"--set persistence.defaultClass=true --set persistence.defaultClassReplicaCount=1 '--kubeconfig' '/etc/rancher/k3s/k3s.yaml'",
		"'helm' repo add longhorn https://charts.longhorn.io --force-update '--kubeconfig'",
		"export KUBECONFIG=/etc/rancher/k3s/k3s.yaml",
		"is-default-class\":\"false\"",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q:\n%s", want, s)
		}
	}
	up := LonghornInstallScript(FlavorKubeadm, helm, AddonRequest{Name: AddonLonghorn, Version: "1.12.1", Replicas: 3, Upgrade: true, Default: true}, "")
	if !strings.Contains(up, "--reuse-values") || strings.Contains(up, "persistence.defaultClass") || strings.Contains(up, "kubectl patch") || !strings.Contains(up, "/etc/kubernetes/admin.conf") {
		t.Errorf("upgrade script:\n%s", up)
	}
	if strings.Contains(up, "HTTPS_PROXY") {
		t.Errorf("без кэша хаба прокси в сценарии появился ниоткуда:\n%s", up)
	}
	if p := AddonPrepareScript(false); !strings.Contains(p, "open-iscsi nfs-common") || strings.Contains(p, ".skip") {
		t.Errorf("prepare:\n%s", p)
	}
	if p := AddonPrepareScript(true); !strings.Contains(p, "local-storage.yaml.skip") {
		t.Errorf("prepare with skip:\n%s", p)
	}
	// apt — только когда пакет нет, и короткими таймаутами: на узле после
	// Cilium исходящий TCP может молчать, и прежний update -qq вешал шаг.
	p := AddonPrepareScript(false)
	for _, want := range []string{"dpkg -s $p", "Acquire::Retries=0", "Acquire::http::Timeout=8", "exit 42", "modprobe iscsi_tcp"} {
		if !strings.Contains(p, want) {
			t.Errorf("prepare lacks %q:\n%s", want, p)
		}
	}
	if !strings.Contains(p, "for p in open-iscsi nfs-common") || !strings.Contains(p, "[ $need = 1 ]") {
		t.Errorf("apt must stay behind the dpkg check:\n%s", p)
	}
	// Сценарий пишется построчно из Go — синтаксис проверяем bash'ем, как и
	// остальные серверные сценарии.
	checkBashSyntax(t, "addon prepare", p)
}

// Longhorn через кэш хаба: чарт приезжает отдельным шагом, установка
// ставит скачанный tarball, а репозиторий остаётся запасным путём — под
// прокси и только командами репозитория.
func TestLonghornChartThroughCache(t *testing.T) {
	const cache = "http://127.0.0.1:3142"
	if got := LonghornChartURL("1.12.1"); got != "https://github.com/longhorn/charts/releases/download/longhorn-1.12.1/longhorn-1.12.1.tgz" {
		t.Errorf("chart url: %s", got)
	}
	if got := LonghornChartPath("1.12.1"); got != "/var/lib/nkt/addons/longhorn-1.12.1.tgz" {
		t.Errorf("chart path: %s", got)
	}
	f := LonghornChartFetchScript(cache, "1.12.1")
	for _, want := range []string{
		"NKT_CACHE=" + cache,
		"art 'https://github.com/longhorn/charts/releases/download/longhorn-1.12.1/longhorn-1.12.1.tgz'",
		"'/var/lib/nkt/addons/longhorn-1.12.1.tgz.part'",
		"tar -tzf",
		"install -d -m 700 /var/lib/nkt/addons",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("chart fetch lacks %q:\n%s", want, f)
		}
	}
	checkBashSyntax(t, "chart fetch", f)

	helm := []string{"helm", "--kubeconfig", "/etc/rancher/k3s/k3s.yaml"}
	s := LonghornInstallScript(FlavorK3s, helm, AddonRequest{Name: AddonLonghorn, Version: "1.12.1", Replicas: 2, Default: true}, cache)
	tgz := shq(LonghornChartPath("1.12.1"))
	if !strings.Contains(s, "[ -s "+tgz+" ]") || !strings.Contains(s, "chart="+tgz) {
		t.Errorf("install must prefer the cached chart:\n%s", s)
	}
	if !strings.Contains(s, `"$chart" -n longhorn-system --create-namespace $vflags --wait --timeout 15m`) ||
		!strings.Contains(s, "vflags='--version 1.12.1'") || !strings.Contains(s, "vflags=;") {
		t.Errorf("--version передаётся только когда чарт берётся из репозитория:\n%s", s)
	}
	if strings.Contains(s, "export HTTPS_PROXY") {
		t.Errorf("общий export HTTPS_PROXY увёл бы у helm запрос к API кластера:\n%s", s)
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "HTTPS_PROXY") && !strings.Contains(line, "repo add longhorn") && !strings.Contains(line, "repo update longhorn") {
			t.Errorf("прокси появился не на команде репозитория: %q", line)
		}
	}
	if !strings.Contains(s, "upgrade --install longhorn \"$chart\"") || strings.Contains(s, "upgrade --install longhorn longhorn/longhorn") {
		t.Errorf("установка обязана идти по переменной, а не по имени из репозитория:\n%s", s)
	}
	checkBashSyntax(t, "install with cache", s)
}

func TestAddonPackages(t *testing.T) {
	got := AddonPackages(AddonLonghorn)
	if strings.Join(got, " ") != "open-iscsi nfs-common" {
		t.Errorf("longhorn packages: %v", got)
	}
	if len(AddonPackages("неткого")) != 0 {
		t.Error("неизвестное дополнение не должно давать пакетов")
	}
}

func TestLonghornUpgradeRules(t *testing.T) {
	cases := []struct {
		cur, next string
		ok        bool
	}{
		{"", "1.12.1", true},
		{"1.11.3", "1.12.1", true},
		{"1.12.0", "1.12.1", true},
		{"1.12.1", "1.12.1", true},
		{"1.10.2", "1.12.1", false},
		{"1.12.1", "1.11.3", false},
		{"1.12.1", "2.0.0", false},
	}
	for _, c := range cases {
		if err := CheckLonghornUpgrade(c.cur, c.next); (err == nil) != c.ok {
			t.Errorf("%s → %s: %v", c.cur, c.next, err)
		}
	}
}

func TestKubeVersionOK(t *testing.T) {
	cases := []struct {
		constraint, k8s string
		ok              bool
	}{
		{">=1.25.0-0", "1.34", true},
		{">=1.34.0-0", "1.33", false},
		{">=1.34.0-0", "v1.34.2+k3s1", true},
		{">=1.34.0-0", "2.0", true},
		{"", "1.20", true},
		{"~1.30", "1.20", true},
	}
	for _, c := range cases {
		if got := KubeVersionOK(c.constraint, c.k8s); got != c.ok {
			t.Errorf("%q %q: %v", c.constraint, c.k8s, got)
		}
	}
}

// Реестр дополнений: имена уникальны, значения по умолчанию — вида X.Y.Z,
// репозитории — https, у каждого есть release/namespace/chart.
func TestAddonRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range Addons {
		def, ok := AddonDefOf(name)
		if !ok || def.Name != name {
			t.Fatalf("no def for %q", name)
		}
		if seen[name] {
			t.Errorf("addon %s twice in the registry", name)
		}
		seen[name] = true
		if !ValidAddonVersion(def.Default) {
			t.Errorf("%s: default version %q is not X.Y.Z", name, def.Default)
		}
		if !strings.HasPrefix(def.Repo, "https://") || def.Chart == "" || def.Release == "" || def.Namespace == "" || def.RepoName == "" {
			t.Errorf("%s: incomplete def %+v", name, def)
		}
		if !strings.HasPrefix(def.Docs, "https://") {
			t.Errorf("%s: docs link %q is not an https URL", name, def.Docs)
		}
		if !ValidAddon(name) {
			t.Errorf("%s: ValidAddon is false", name)
		}
	}
	for _, want := range []string{AddonLonghorn, AddonIngressNginx, AddonCertManager, AddonMetricsServer, AddonMetallb, AddonKubePrometheusStack, AddonKyverno, AddonArgoCD} {
		if !seen[want] {
			t.Errorf("registry lacks %s", want)
		}
	}
	// Longhorn — единственное с подготовкой узлов, репликами и минимумом диска.
	if !AddonNeedsPrepare(AddonLonghorn) {
		t.Error("longhorn must require prepare")
	}
	for _, name := range Addons {
		def, _ := AddonDefOf(name)
		if name != AddonLonghorn && (def.NodePrepare || def.Storage || len(def.Packages) != 0) {
			t.Errorf("%s: storage/prepare flags leaked past longhorn: %+v", name, def)
		}
	}
	if AddonFlavorConflict(AddonMetricsServer) != FlavorK3s {
		t.Errorf("metrics-server flavor conflict: %q", AddonFlavorConflict(AddonMetricsServer))
	}
	if AddonFlavorConflict(AddonLonghorn) != "" {
		t.Error("longhorn has no flavor conflict")
	}
}

// Validate: реплики обязательны только Longhorn, prepare — только
// дополнениям с подготовкой, ChartURL — только https.
func TestAddonRequestValidateRegistry(t *testing.T) {
	ok := []AddonRequest{
		{Action: AddonInstall, Name: AddonIngressNginx, Version: "4.15.1"},
		{Action: AddonInstall, Name: AddonCertManager, Version: "1.21.3", ChartURL: "https://charts.jetstack.io/charts/cert-manager-v1.21.3.tgz"},
		{Action: AddonInstall, Name: AddonArgoCD, Version: "10.18.0", Upgrade: true},
		{Action: AddonUninstall, Name: AddonCertManager},
		{Action: AddonUninstall, Name: AddonLonghorn},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []AddonRequest{
		{Action: AddonPrepare, Name: AddonIngressNginx}, // no node preparation
		{Action: AddonUninstall, Name: "rook"},          // unknown name
		{Action: AddonInstall, Name: AddonMetallb, Version: "0.16"},
		{Action: AddonInstall, Name: AddonKyverno, Version: "3.10.0", ChartURL: "http://kyverno.github.io/kyverno/kyverno-3.10.0.tgz"},
		{Action: AddonInstall, Name: AddonKyverno, Version: "3.10.0", ChartURL: "kyverno-3.10.0.tgz"},
		{Action: AddonInstall, Name: "rook", Version: "1.2.3"},
	}
	for _, r := range bad {
		if r.Validate() == nil {
			t.Errorf("%+v: accepted", r)
		}
	}
}

// Общий скрипт установки: helm upgrade --install из репозитория (без кэша)
// и из tarball кэша (с кэшем), флаги --set свежей установки, --reuse-values
// на обновлении, статус релиза в конце.
func TestAddonChartInstallScript(t *testing.T) {
	helm := []string{"helm", "--kubeconfig", "/etc/kubernetes/admin.conf"}
	s := AddonInstallScript(FlavorKubeadm, helm, AddonRequest{Name: AddonCertManager, Version: "1.21.3"}, "")
	for _, want := range []string{
		"export KUBECONFIG=/etc/kubernetes/admin.conf",
		"'helm' repo add jetstack https://charts.jetstack.io --force-update",
		"'helm' repo update jetstack",
		"'helm' upgrade --install cert-manager jetstack/cert-manager -n cert-manager --create-namespace --version 1.21.3 --wait --timeout 15m",
		"--set 'crds.enabled=true'",
		"--set 'installCRDs=true'",
		"'helm' status cert-manager -n cert-manager",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q:\n%s", want, s)
		}
	}
	checkBashSyntax(t, "cert-manager install", s)

	// Upgrade keeps previous values and adds no --set of its own.
	up := AddonInstallScript(FlavorKubeadm, helm, AddonRequest{Name: AddonCertManager, Version: "1.21.3", Upgrade: true}, "")
	if !strings.Contains(up, "--reuse-values") || strings.Contains(up, "crds.enabled") {
		t.Errorf("upgrade script:\n%s", up)
	}
	checkBashSyntax(t, "cert-manager upgrade", up)

	// ingress-nginx serves NodePort on bare metal.
	ing := AddonInstallScript(FlavorKubeadm, helm, AddonRequest{Name: AddonIngressNginx, Version: "4.15.1"}, "")
	if !strings.Contains(ing, "--set 'controller.service.type=NodePort'") || !strings.Contains(ing, "-n ingress-nginx") {
		t.Errorf("ingress-nginx script:\n%s", ing)
	}
	checkBashSyntax(t, "ingress-nginx install", ing)

	// metrics-server: the full args list survives shell quoting with the
	// helm comma escapes intact.
	ms := AddonInstallScript(FlavorK3s, helm, AddonRequest{Name: AddonMetricsServer, Version: "3.14.0"}, "")
	if !strings.Contains(ms, `--set 'args[5]=--kubelet-insecure-tls'`) ||
		!strings.Contains(ms, `--set 'args[2]=--kubelet-preferred-address-types=InternalIP\,ExternalIP\,Hostname'`) ||
		!strings.Contains(ms, "-n kube-system") {
		t.Errorf("metrics-server script:\n%s", ms)
	}
	checkBashSyntax(t, "metrics-server install", ms)

	// Through the hub cache: the downloaded tarball wins, the repository
	// stays the fallback under the proxy.
	const cache = "http://127.0.0.1:3142"
	c := AddonInstallScript(FlavorKubeadm, helm, AddonRequest{Name: AddonKyverno, Version: "3.10.0"}, cache)
	tgz := shq(AddonChartPath(AddonKyverno, "3.10.0"))
	if !strings.Contains(c, "[ -s "+tgz+" ]") || !strings.Contains(c, "chart="+tgz) {
		t.Errorf("install must prefer the cached chart:\n%s", c)
	}
	if !strings.Contains(c, "upgrade --install kyverno \"$chart\"") || !strings.Contains(c, "vflags='--version 3.10.0'") {
		t.Errorf("cached install script:\n%s", c)
	}
	for _, line := range strings.Split(c, "\n") {
		if strings.Contains(line, "HTTPS_PROXY") && !strings.Contains(line, "repo add kyverno") && !strings.Contains(line, "repo update kyverno") {
			t.Errorf("proxy outside repository commands: %q", line)
		}
	}
	checkBashSyntax(t, "kyverno install with cache", c)

	// Longhorn keeps its own script through the dispatcher.
	lh := AddonInstallScript(FlavorKubeadm, helm, AddonRequest{Name: AddonLonghorn, Version: "1.12.1", Replicas: 2, Default: true}, "")
	if lh != LonghornInstallScript(FlavorKubeadm, helm, AddonRequest{Name: AddonLonghorn, Version: "1.12.1", Replicas: 2, Default: true}, "") {
		t.Error("dispatcher changed the longhorn script")
	}
}

// Загрузка чарта через кэш: URL из запроса, проверка tar, пустой URL —
// честный выход без скачивания.
func TestAddonChartFetchScriptGeneric(t *testing.T) {
	const cache = "http://127.0.0.1:3142"
	f := AddonChartFetchScript(cache, AddonCertManager, "1.21.3", "https://charts.jetstack.io/charts/cert-manager-v1.21.3.tgz")
	for _, want := range []string{
		"art 'https://charts.jetstack.io/charts/cert-manager-v1.21.3.tgz'",
		"'/var/lib/nkt/addons/cert-manager-1.21.3.tgz.part'",
		"tar -tzf",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("fetch script lacks %q:\n%s", want, f)
		}
	}
	checkBashSyntax(t, "addon chart fetch", f)

	empty := AddonChartFetchScript(cache, AddonCertManager, "1.21.3", "")
	if strings.Contains(empty, "art '") || !strings.Contains(empty, "repository") {
		t.Errorf("empty URL must not download:\n%s", empty)
	}
	checkBashSyntax(t, "addon chart fetch without url", empty)

	if LonghornChartFetchScript(cache, "1.12.1") != AddonChartFetchScript(cache, AddonLonghorn, "1.12.1", LonghornChartURL("1.12.1")) {
		t.Error("longhorn fetch script changed meaning")
	}
}

// Скрипт удаления: guard идемпотентности (нет релиза — не ошибка),
// --wait --timeout, подсказка о ручной дочистке namespace, kubeconfig
// k3s.
func TestAddonUninstallScript(t *testing.T) {
	helm := []string{"helm", "--kubeconfig", "/etc/kubernetes/admin.conf"}
	s := AddonUninstallScript(FlavorKubeadm, helm, AddonRequest{Action: AddonUninstall, Name: AddonCertManager})
	for _, want := range []string{
		"export KUBECONFIG=/etc/kubernetes/admin.conf",
		"if 'helm' status cert-manager -n cert-manager '--kubeconfig' '/etc/kubernetes/admin.conf' >/dev/null 2>&1; then",
		"'helm' uninstall cert-manager -n cert-manager --wait --timeout 10m '--kubeconfig' '/etc/kubernetes/admin.conf'",
		"nothing to uninstall",
		"kubectl delete namespace cert-manager",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("uninstall script lacks %q:\n%s", want, s)
		}
	}
	checkBashSyntax(t, "cert-manager uninstall", s)

	k3 := AddonUninstallScript(FlavorK3s, helm, AddonRequest{Action: AddonUninstall, Name: AddonLonghorn})
	if !strings.Contains(k3, "export KUBECONFIG=/etc/rancher/k3s/k3s.yaml") || !strings.Contains(k3, "uninstall longhorn -n longhorn-system") {
		t.Errorf("k3s longhorn uninstall script:\n%s", k3)
	}
	checkBashSyntax(t, "longhorn uninstall on k3s", k3)
}

// Правила обновления: Longhorn строгий (ветка), остальные — только запрет
// отката.
func TestCheckAddonUpgradeRegistry(t *testing.T) {
	if err := CheckAddonUpgrade(AddonCertManager, "1.19.0", "1.21.3"); err != nil {
		t.Errorf("cert-manager two minors forward: %v", err)
	}
	if err := CheckAddonUpgrade(AddonArgoCD, "10.18.0", "9.0.0"); err == nil {
		t.Error("argo-cd downgrade accepted")
	}
	if err := CheckAddonUpgrade(AddonLonghorn, "1.10.2", "1.12.1"); err == nil {
		t.Error("longhorn two minors forward must be rejected")
	}
	if err := CheckAddonUpgrade(AddonMetallb, "", "0.16.2"); err != nil {
		t.Errorf("fresh install: %v", err)
	}
}
