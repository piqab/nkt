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
