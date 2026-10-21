package k8s

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Сценарий Ingress Cilium: версия — работающая, порт — заданный, у k3s и
// kubeadm — свой kubeconfig; синтаксис bash верный.
func TestCiliumIngressScript(t *testing.T) {
	s := CiliumIngressScript(FlavorKubeadm, 8080)
	for _, want := range []string{
		"KUBECONFIG=/etc/kubernetes/admin.conf", `--version "$V" --reuse-values`, "hostNetwork.enabled=true", "sharedListenerPort=8080",
		"kube-proxy-replacement", "rollout status ds/cilium",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("нет %q:\n%s", want, s)
		}
	}
	if !strings.Contains(CiliumIngressScript(FlavorK3s, 9000), "/etc/rancher/k3s/k3s.yaml") {
		t.Error("k3s kubeconfig")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("нет bash")
	}
	f := filepath.Join(t.TempDir(), "s.sh")
	if err := os.WriteFile(f, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v %s", err, out)
	}
}
