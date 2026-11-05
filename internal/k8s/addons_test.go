package k8s

import (
	"os"
	"os/exec"
	"path/filepath"
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
	s := LonghornInstallScript(FlavorK3s, helm, AddonRequest{Name: AddonLonghorn, Version: "1.12.1", Replicas: 1, Default: true})
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
	up := LonghornInstallScript(FlavorKubeadm, helm, AddonRequest{Name: AddonLonghorn, Version: "1.12.1", Replicas: 3, Upgrade: true, Default: true})
	if !strings.Contains(up, "--reuse-values") || strings.Contains(up, "persistence.defaultClass") || strings.Contains(up, "kubectl patch") || !strings.Contains(up, "/etc/kubernetes/admin.conf") {
		t.Errorf("upgrade script:\n%s", up)
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
	f := filepath.Join(t.TempDir(), "prepare.sh")
	if err := os.WriteFile(f, []byte(p), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v %s", err, out)
	}
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
