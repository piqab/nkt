package k8s

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

func TestValidate(t *testing.T) {
	ok := InstallSpec{Flavor: FlavorK3s, Role: RoleServer, Single: true}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []InstallSpec{
		{Flavor: "minikube", Role: RoleServer},
		{Flavor: FlavorK3s, Role: "master"},
		{Flavor: FlavorK3s, Role: RoleAgent},
		{Flavor: FlavorK3s, Role: RoleAgent, ServerURL: "https://x:6443", Token: "a b"},
		{Flavor: FlavorKubeadm, Role: RoleServer, TLSSANs: []string{"$(id)"}},
	} {
		if bad.Validate() == nil {
			t.Errorf("прошло: %+v", bad)
		}
	}
}

// Шаги — официальные команды, в нужном порядке и с нужными флагами.
func TestSteps(t *testing.T) {
	single := Steps(InstallSpec{Flavor: FlavorK3s, Role: RoleServer, Single: true, TLSSANs: []string{"203.0.113.5"}})
	joined := joinScripts(single)
	for _, want := range []string{"get.k3s.io", "INSTALL_K3S_EXEC='server --tls-san 203.0.113.5'", "systemctl is-active --quiet k3s", "k3s kubectl get nodes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("k3s single: нет %q", want)
		}
	}
	if strings.Contains(joined, "node-taint") {
		t.Error("одиночный узел не должен получать taint")
	}
	cp := joinScripts(Steps(InstallSpec{Flavor: FlavorK3s, Role: RoleServer, ClusterInit: true}))
	if !strings.Contains(cp, "--cluster-init") || !strings.Contains(cp, "--node-taint node-role.kubernetes.io/control-plane:NoSchedule") {
		t.Errorf("k3s HA control plane: %s", cp)
	}
	agent := joinScripts(Steps(InstallSpec{Flavor: FlavorK3s, Role: RoleAgent, ServerURL: "https://10.0.0.1:6443", Token: "K10abc"}))
	if !strings.Contains(agent, "K3S_URL='https://10.0.0.1:6443'") || !strings.Contains(agent, "K3S_TOKEN='K10abc'") || !strings.Contains(agent, "INSTALL_K3S_EXEC='agent'") || !strings.Contains(agent, "k3s-agent") {
		t.Errorf("k3s agent: %s", agent)
	}

	ka := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, Single: true, TLSSANs: []string{"203.0.113.5"}}))
	for _, want := range []string{"pkgs.k8s.io", "containerd", "SystemdCgroup = true", "br_netfilter", "kubeadm init --kubernetes-version=$(kubeadm version -o short) --pod-network-cidr=10.244.0.0/16 --apiserver-cert-extra-sans=203.0.113.5", "kube-flannel.yml", "taint nodes --all"} {
		if !strings.Contains(ka, want) {
			t.Errorf("kubeadm single: нет %q", want)
		}
	}
	kw := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleAgent, ServerURL: "10.0.0.1:6443", Token: "abcdef.0123456789abcdef", CAHash: "sha256:00"}))
	if !strings.Contains(kw, "kubeadm join 10.0.0.1:6443 --token abcdef.0123456789abcdef --discovery-token-ca-cert-hash sha256:00") || strings.Contains(kw, "--control-plane") {
		t.Errorf("kubeadm worker: %s", kw)
	}
	kcp := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "10.0.0.1:6443", Token: "t", CAHash: "h", CertKey: "ck"}))
	if !strings.Contains(kcp, "--control-plane --certificate-key ck") {
		t.Errorf("kubeadm extra control plane: %s", kcp)
	}
}

func joinScripts(steps []Step) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.Script)
		b.WriteString("\n")
	}
	return b.String()
}

func TestParseJoinCommand(t *testing.T) {
	info, err := ParseJoinCommand("kubeadm join 10.0.0.1:6443 --token abcdef.0123456789abcdef --discovery-token-ca-cert-hash sha256:deadbeef \n")
	if err != nil || info.Token != "abcdef.0123456789abcdef" || info.CAHash != "sha256:deadbeef" {
		t.Errorf("%+v %v", info, err)
	}
}

func TestParseNodes(t *testing.T) {
	raw := `{"items":[{"metadata":{"name":"cp-1","labels":{"node-role.kubernetes.io/control-plane":"true","node-role.kubernetes.io/master":"true"},"creationTimestamp":"2026-09-18T10:00:00Z"},"status":{"conditions":[{"type":"Ready","status":"True"}],"addresses":[{"type":"InternalIP","address":"192.168.122.10"}],"nodeInfo":{"kubeletVersion":"v1.31.4+k3s1"}}},{"metadata":{"name":"w-1","labels":{}},"status":{"conditions":[{"type":"Ready","status":"False"}],"addresses":[],"nodeInfo":{"kubeletVersion":"v1.31.4+k3s1"}}}]}`
	nodes, err := ParseNodes([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Roles != "control-plane,master" || !nodes[0].Ready || nodes[0].InternalIP != "192.168.122.10" || nodes[1].Roles != "worker" || nodes[1].Ready {
		t.Errorf("%+v", nodes)
	}
}

// Cilium: k3s без flannel (и без kube-proxy при замене), kubeadm без
// Flannel и с пропуском kube-proxy; шаг с CLI и cilium install до
// ожидания Ready.
func TestStepsCilium(t *testing.T) {
	k := joinScripts(Steps(InstallSpec{Flavor: FlavorK3s, Role: RoleServer, Single: true, CNI: "cilium", KubeProxyReplacement: true, APIAddr: "192.168.122.10"}))
	for _, want := range []string{"--flannel-backend=none", "--disable-network-policy", "--disable-kube-proxy", "cilium-cli/releases/latest", "sha256sum --check", "cilium install --set kubeProxyReplacement=true --set k8sServiceHost=192.168.122.10 --set k8sServicePort=6443", "cilium status --wait"} {
		if !strings.Contains(k, want) {
			t.Errorf("k3s cilium: нет %q", want)
		}
	}
	// Guard уже поставленного Cilium: по DaemonSet, а не по «cilium status
	// --brief» — такого флага у cilium-cli нет, и проверка всегда падала.
	if strings.Contains(k, "--brief") {
		t.Error("cilium: --brief у cilium-cli нет, guard должен смотреть на DaemonSet")
	}
	if !strings.Contains(k, "kubectl -n kube-system get ds cilium") {
		t.Error("cilium: нет проверки DaemonSet перед install")
	}
	if strings.Index(k, "cilium install") > strings.Index(k, "grep -q ' Ready'") {
		t.Error("Cilium должен ставиться до ожидания Ready")
	}
	ka := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, Single: true, CNI: "cilium"}))
	if strings.Contains(ka, "kube-flannel") || strings.Contains(ka, "skip-phases") ||
		!strings.Contains(ka, "else cilium install; fi") {
		t.Errorf("kubeadm cilium без замены kube-proxy: %s", ka)
	}
	kb := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, CNI: "cilium", KubeProxyReplacement: true}))
	if !strings.Contains(kb, "--skip-phases=addon/kube-proxy") {
		t.Error("kubeadm с заменой kube-proxy должен пропускать его установку")
	}
	if (InstallSpec{Flavor: FlavorK3s, Role: RoleServer, CNI: "calico"}).Validate() == nil {
		t.Error("неизвестный CNI прошёл проверку")
	}
}

// checkBashSyntax — «bash -n» на сценарий шага: синтаксис проверяется до
// того, как шаг уйдёт на машину.
func checkBashSyntax(t *testing.T, what, script string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("нет bash")
	}
	f := filepath.Join(t.TempDir(), "s.sh")
	if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("%s: bash -n: %v %s", what, err, out)
	}
}

// Guard уже поставленного Cilium прогоняется живьём с подставными kubectl и
// cilium: DaemonSet есть — «cilium install» не зовётся, DaemonSet нет —
// зовётся; ожидание готовности остаётся в обоих случаях.
func TestCiliumStepGuardSkipsInstalled(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("нет bash")
	}
	for _, s := range []InstallSpec{
		{Flavor: FlavorK3s, Role: RoleServer, Single: true, CNI: "cilium", KubeProxyReplacement: true, APIAddr: "192.168.122.10"},
		{Flavor: FlavorKubeadm, Role: RoleServer, Single: true, CNI: "cilium"},
	} {
		var guard, wait, full string
		for _, st := range Steps(s) {
			if st.Title != "k8s.step.cilium" {
				continue
			}
			full = st.Script
			for _, line := range strings.Split(st.Script, "\n") {
				if strings.Contains(line, "get ds cilium") {
					guard = line
				}
				if strings.Contains(line, "cilium status --wait") {
					wait = line
				}
			}
		}
		if guard == "" || wait == "" {
			t.Fatalf("%s: в шаге cilium нет строк guard/ожидания", s.Flavor)
		}
		checkBashSyntax(t, s.Flavor+" cilium step", full)
		// Подставные kubectl и cilium записывают вызовы в файл: по нему и
		// проверяется, звался ли install.
		for _, ds := range []bool{true, false} {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			kubectlExit := "1"
			if ds {
				kubectlExit = "0"
			}
			for _, stub := range []struct{ name, body string }{
				{"kubectl", "#!/bin/sh\necho \"kubectl $*\" >>" + log + "\nexit " + kubectlExit + "\n"},
				// У k3s guard зовёт «k3s kubectl» — подставку и под него.
				{"k3s", "#!/bin/sh\necho \"k3s $*\" >>" + log + "\nexit " + kubectlExit + "\n"},
				{"cilium", "#!/bin/sh\necho \"cilium $*\" >>" + log + "\nexit 0\n"},
			} {
				p := filepath.Join(dir, stub.name)
				if err := os.WriteFile(p, []byte(stub.body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			script := filepath.Join(dir, "step.sh")
			if err := os.WriteFile(script, []byte("set -e\n"+guard+"\n"+wait+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s, ds=%v: %v\n%s", s.Flavor, ds, err, out)
			}
			raw, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			calls := string(raw)
			if !strings.Contains(calls, "kubectl -n kube-system get ds cilium") {
				t.Errorf("%s: guard не спросил DaemonSet:\n%s", s.Flavor, calls)
			}
			if !strings.Contains(calls, "cilium status --wait") {
				t.Errorf("%s: нет ожидания готовности:\n%s", s.Flavor, calls)
			}
			if installed := strings.Contains(calls, "cilium install"); installed != !ds {
				if ds {
					t.Errorf("%s: переставил уже стоящий Cilium:\n%s", s.Flavor, calls)
				} else {
					t.Errorf("%s: не поставил Cilium, которого нет:\n%s", s.Flavor, calls)
				}
			}
		}
	}
}

// Провал apt объясняет строка «E: …» из stderr, а не последний
// «Processing triggers…» из stdout.
func TestFailureLine(t *testing.T) {
	out := collect.CommandResult{Stdout: "Setting up kubelet\nProcessing triggers for libc-bin ...\n",
		Stderr: "W: something\nE: Could not get lock /var/lib/dpkg/lock-frontend\nE: Unable to acquire the dpkg frontend lock\n", ExitCode: 100}
	if got := failureLine(out); got != "E: Unable to acquire the dpkg frontend lock" {
		t.Errorf("got %q", got)
	}
	if got := failureLine(collect.CommandResult{Stdout: "ok\nlast\n"}); got != "last" {
		t.Errorf("stdout fallback: %q", got)
	}
	if got := failureLine(collect.CommandResult{Stderr: "boom\n"}); got != "boom" {
		t.Errorf("stderr last: %q", got)
	}
}
