package k8s

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	kcp := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "10.0.0.1:6443", Token: "t", CAHash: "h", CertKey: certKey}))
	if !strings.Contains(kcp, "--control-plane --certificate-key "+certKey) {
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
	// Задание узла с Cilium НЕ ставит CNI и НЕ ждёт Ready: без CNI узел
	// легально NotReady, готовность проверит конвейер кластера после
	// отдельного шага cilium. k3s при этом отключает свой flannel.
	k := joinScripts(Steps(InstallSpec{Flavor: FlavorK3s, Role: RoleServer, Single: true, CNI: "cilium", KubeProxyReplacement: true, APIAddr: "192.168.122.10"}))
	for _, want := range []string{"--flannel-backend=none", "--disable-network-policy", "--disable-kube-proxy"} {
		if !strings.Contains(k, want) {
			t.Errorf("k3s cilium: нет %q", want)
		}
	}
	if strings.Contains(k, "cilium install") || strings.Contains(k, "cilium-cli/releases") {
		t.Error("k3s: задание узла не должно ставить Cilium — это шаг конвейера кластера")
	}
	if strings.Contains(k, "grep -q ' Ready'") {
		t.Error("k3s cilium: задание узла не должно ждать Ready до установки CNI")
	}
	ka := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, Single: true, CNI: "cilium"}))
	if strings.Contains(ka, "kube-flannel") {
		t.Errorf("kubeadm cilium: flannel ставиться не должен: %s", ka)
	}
	if strings.Contains(ka, "grep -q ' Ready'") {
		t.Error("kubeadm cilium: задание узла не должно ждать Ready до установки CNI")
	}
	// Без Cilium flannel ставится в задании узла и Ready ждётся там же.
	kf := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, Single: true}))
	if !strings.Contains(kf, "kube-flannel.yml") || !strings.Contains(kf, "grep -q ' Ready'") {
		t.Error("kubeadm без Cilium: flannel и ожидание Ready остаются в задании узла")
	}
	kb := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, CNI: "cilium", KubeProxyReplacement: true}))
	if !strings.Contains(kb, "--skip-phases=addon/kube-proxy") {
		t.Error("kubeadm с заменой kube-proxy должен пропускать его установку")
	}
	if (InstallSpec{Flavor: FlavorK3s, Role: RoleServer, CNI: "calico"}).Validate() == nil {
		t.Error("неизвестный CNI прошёл проверку")
	}
}

// Шаг cilium (задание KindCilium): guard по релизу Helm, а не по DaemonSet
// («cannot reuse a name that is still in use» на повторе), install и
// upgrade с одним набором флагов, путь CNI — тот, что ищет kubelet Debian.
func TestCiliumStepScript(t *testing.T) {
	st := ciliumStep(InstallSpec{Flavor: FlavorKubeadm, CNI: "cilium", KubeProxyReplacement: true, APIAddr: "192.168.122.10"}, "/etc/kubernetes/admin.conf")
	checkBashSyntax(t, "cilium", st.Script)
	for _, want := range []string{
		"helm status cilium -n kube-system",
		"cilium upgrade --set cni.binPath=/usr/lib/cni --set kubeProxyReplacement=true --set k8sServiceHost=192.168.122.10 --set k8sServicePort=6443",
		"cilium install --set cni.binPath=/usr/lib/cni --set kubeProxyReplacement=true --set k8sServiceHost=192.168.122.10 --set k8sServicePort=6443",
		"cilium status --wait",
		"cilium-linux-$ARCH.tar.gz.sha256sum",
	} {
		if !strings.Contains(st.Script, want) {
			t.Errorf("cilium step: нет %q", want)
		}
	}
	if strings.Contains(st.Script, "get ds cilium") {
		t.Error("cilium step: guard по DaemonSet заменён на helm status")
	}
	// Кастомные values уходят в файл и достаются обеим веткам.
	sv := ciliumStep(InstallSpec{Flavor: FlavorKubeadm, CNI: "cilium", CiliumValues: "cni:\n  binPath: /usr/lib/cni\n"}, "/etc/kubernetes/admin.conf")
	checkBashSyntax(t, "cilium values", sv.Script)
	for _, want := range []string{"/tmp/cilium-values.yaml", "upgrade --set cni.binPath=/usr/lib/cni --values /tmp/cilium-values.yaml", "install --set cni.binPath=/usr/lib/cni --values /tmp/cilium-values.yaml"} {
		if !strings.Contains(sv.Script, want) {
			t.Errorf("cilium step с values: нет %q", want)
		}
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
	s := InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, Single: true, CNI: "cilium"}
	full := ciliumStep(s, "/etc/kubernetes/admin.conf").Script
	var guard, wait string
	for _, line := range strings.Split(full, "\n") {
		if strings.Contains(line, "helm status cilium") {
			guard = line
		}
		if strings.Contains(line, "cilium status --wait") {
			wait = line
		}
	}
	if guard == "" || wait == "" {
		t.Fatalf("в шаге cilium нет строк guard/ожидания")
	}
	checkBashSyntax(t, "cilium step", full)
	// Подставные helm и cilium записывают вызовы в файл: по нему и
	// проверяется, какая ветка guard'а сработала. Релиз есть (helm exit 0)
	// — upgrade, релиза нет (exit 1) — install: повтор упавшей установки
	// с залипшим релизом не должен падать «cannot reuse a name».
	for _, release := range []bool{true, false} {
		dir := t.TempDir()
		log := filepath.Join(dir, "calls")
		helmExit := "1"
		if release {
			helmExit = "0"
		}
		for _, stub := range []struct{ name, body string }{
			{"helm", "#!/bin/sh\necho \"helm $*\" >>" + log + "\nexit " + helmExit + "\n"},
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
			t.Fatalf("release=%v: %v\n%s", release, err, out)
		}
		raw, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		calls := string(raw)
		if !strings.Contains(calls, "helm status cilium -n kube-system") {
			t.Errorf("guard не спросил релиз Helm:\n%s", calls)
		}
		if !strings.Contains(calls, "cilium status --wait") {
			t.Errorf("нет ожидания готовности:\n%s", calls)
		}
		upgraded := strings.Contains(calls, "cilium upgrade")
		installed := strings.Contains(calls, "cilium install")
		if release && !upgraded {
			t.Errorf("при живом релизе нужен upgrade:\n%s", calls)
		}
		if !release && !installed {
			t.Errorf("без релиза нужен install:\n%s", calls)
		}
		if upgraded && installed {
			t.Errorf("сработали обе ветки guard'а:\n%s", calls)
		}
	}
}

// nodeState — что лежит на узле в момент повтора шага «установка роли».
// Манифесты control plane пишутся в фазе control-plane-prepare, участником
// etcd узел становится и пишет etcd.yaml только в фазе etcd-join — дальше
// которой вход и падал, поэтому apiserver без etcd — недоделанный узел.
type nodeState struct {
	adminConf   bool // /etc/kubernetes/admin.conf
	apiserver   bool // manifests/kube-apiserver.yaml
	etcdMember  bool // manifests/etcd.yaml
	kubeletConf bool // /etc/kubernetes/kubelet.conf
	kubeletUp   bool // systemctl is-active kubelet
	healthzOK   bool // API отвечает
	resetFails  int  // сколько попыток reset подряд падают
}

// runInstallGuard прогоняет guard шага «установка роли» живьём: подставные
// kubeadm, kubectl и systemctl пишут вызовы в файл, состояние узла —
// подставной каталог в NKT_KUBE_DIR.
func runInstallGuard(t *testing.T, script string, st nodeState) (string, error) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	kubeDir := filepath.Join(dir, "kubernetes")
	if err := os.MkdirAll(filepath.Join(kubeDir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"admin.conf": st.adminConf, "kubelet.conf": st.kubeletConf,
		"manifests/kube-apiserver.yaml": st.apiserver, "manifests/etcd.yaml": st.etcdMember,
	} {
		if want {
			if err := os.WriteFile(filepath.Join(kubeDir, name), []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "reset-left"), []byte(itoa(st.resetFails)), 0o600); err != nil {
		t.Fatal(err)
	}
	// Отказывает ровно reset (все три фазы каскада зовут его первой
	// подкомандой), init и join — пишут себя в журнал и проходят.
	stubs := []struct{ name, body string }{
		{"kubeadm", "#!/bin/sh\necho \"kubeadm $*\" >>\"$NKT_TEST_CALLS\"\nif [ \"$1\" = reset ]; then n=$(cat \"$NKT_TEST_RESET\"); if [ \"$n\" -gt 0 ]; then echo $((n-1)) >\"$NKT_TEST_RESET\"; echo \"kubeadm: [reset] simulated failure\"; exit 1; fi; fi\nexit 0\n"},
		{"kubectl", "#!/bin/sh\necho \"kubectl $*\" >>\"$NKT_TEST_CALLS\"\nexit \"$NKT_TEST_HEALTHZ\"\n"},
		{"systemctl", "#!/bin/sh\necho \"systemctl $*\" >>\"$NKT_TEST_CALLS\"\nexit \"$NKT_TEST_KUBELET\"\n"},
	}
	for _, s := range stubs {
		if err := os.WriteFile(filepath.Join(dir, s.name), []byte(s.body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	healthz, kubelet := "1", "1"
	if st.healthzOK {
		healthz = "0"
	}
	if st.kubeletUp {
		kubelet = "0"
	}
	cmd := exec.Command("bash", f)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+":"+os.Getenv("PATH"),
		"NKT_KUBE_DIR="+kubeDir,
		"NKT_TEST_CALLS="+logPath,
		"NKT_TEST_RESET="+filepath.Join(dir, "reset-left"),
		"NKT_TEST_HEALTHZ="+healthz,
		"NKT_TEST_KUBELET="+kubelet,
	)
	out, err := cmd.CombinedOutput()
	raw, _ := os.ReadFile(logPath)
	return string(raw) + "\n" + string(out), err
}

func itoa(n int) string { return strconv.Itoa(n) }

// installStep — скрипт шага «установка роли».
func installStep(t *testing.T, s InstallSpec) string {
	t.Helper()
	for _, st := range Steps(s) {
		if st.Title == "k8s.step.install" {
			return st.Script
		}
	}
	t.Fatal("нет шага k8s.step.install")
	return ""
}

// guardCut — начало скрипта до строки cut (её уже не видно в бою — там
// права root и живой /root/.kube, тесту нужен только guard вместе с самой
// командой kubeadm).
func guardCut(script, cut string) string {
	var lines []string
	for _, l := range strings.Split(script, "\n") {
		if strings.HasPrefix(l, cut) {
			break
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

// Повтор шага «установка роли» определяет состояние узла по манифестам, а
// не по kubelet.conf, и сносит его каскадом reset'ов, не пряча вывод:
// недоделанный вход control plane (манифесты есть, etcd-участника нет)
// раньше объявлялся «уже присоединённым», а снесение состояния падало на
// фазе вычёркивания участника etcd — манифесты оставались, и следующий
// заход отказывался с FileAvailable--etc-kubernetes-manifests-*.yaml.
func TestKubeadmInstallStepGuardIsIdempotent(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("нет bash")
	}
	initSpec := InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, Single: true}
	cpSpec := InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "10.0.0.1:6443", Token: "t", CAHash: "h", CertKey: certKey}
	workerSpec := InstallSpec{Flavor: FlavorKubeadm, Role: RoleAgent, ServerURL: "10.0.0.1:6443", Token: "t", CAHash: "h"}
	initScript := guardCut(installStep(t, initSpec), "mkdir -p /root/.kube")
	cpScript := installStep(t, cpSpec)
	workerScript := installStep(t, workerSpec)
	for _, s := range []InstallSpec{initSpec, cpSpec, workerSpec} {
		checkBashSyntax(t, s.Flavor+" install step", installStep(t, s))
	}
	// Состояние узла решается по манифестам — проверки на «есть ли
	// kubelet.conf» и на живой kubelet без них недостаточно.
	for _, script := range []string{initScript, cpScript} {
		if !strings.Contains(script, "manifests/kube-apiserver.yaml") || !strings.Contains(script, "manifests/etcd.yaml") {
			t.Errorf("guard не смотрит на манифесты control plane:\n%s", script)
		}
	}
	// Сброс не должен молча проваливаться: старые формы «>/dev/null 2>&1 ||
	// true» прятали причину и оставляли узел недоделанным.
	for _, script := range []string{initScript, cpScript, workerScript} {
		if strings.Contains(script, "kubeadm reset -f >/dev/null") || strings.Contains(script, "reset -f || true") {
			t.Errorf("сброс состояния спрятан или прощён:\n%s", script)
		}
	}

	for _, tc := range []struct {
		name   string
		script string
		st     nodeState
		want   []string // подстроки журнала вызовов
		none   []string // чего в журнале быть не должно
		fail   bool     // шаг обязан упасть
	}{
		{
			name:   "init чистый узел",
			script: initScript,
			st:     nodeState{},
			want:   []string{"kubeadm init "},
			none:   []string{"kubeadm reset"},
		},
		{
			name:   "init живой control plane",
			script: initScript,
			st:     nodeState{adminConf: true, apiserver: true, etcdMember: true, kubeletConf: true, healthzOK: true},
			none:   []string{"kubeadm init", "kubeadm reset"},
			want:   []string{"control plane already initialized"},
		},
		{
			name:   "init только admin.conf",
			script: initScript,
			st:     nodeState{adminConf: true, kubeletConf: true, healthzOK: true},
			want:   []string{"kubeadm reset -f\n", "kubeadm init "},
		},
		{
			name:   "init манифесты без ответа API",
			script: initScript,
			st:     nodeState{adminConf: true, apiserver: true, etcdMember: true, kubeletConf: true},
			want:   []string{"leftovers of a failed init", "kubeadm reset -f\n", "kubeadm init "},
		},
		{
			name:   "вход control plane готов",
			script: cpScript,
			st:     nodeState{apiserver: true, etcdMember: true, kubeletConf: true, kubeletUp: true},
			want:   []string{"node already joined"},
			none:   []string{"kubeadm join", "kubeadm reset"},
		},
		{
			// Тот самый случай: узел дошёл до etcd-join и упал. Kubelet
			// при этом жив — прежний guard считал узел присоединённым.
			name:   "недоделанный вход control plane",
			script: cpScript,
			st:     nodeState{apiserver: true, kubeletConf: true, kubeletUp: true},
			want:   []string{"leftovers of a failed join", "kubeadm reset -f\n", "kubeadm join 10.0.0.1:6443"},
			none:   []string{"node already joined"},
		},
		{
			name:   "работник с мёртвым kubelet",
			script: workerScript,
			st:     nodeState{kubeletConf: true},
			want:   []string{"leftovers of a failed join", "kubeadm reset -f\n", "kubeadm join 10.0.0.1:6443"},
		},
		{
			name:   "работник готов",
			script: workerScript,
			st:     nodeState{kubeletConf: true, kubeletUp: true},
			want:   []string{"node already joined"},
			none:   []string{"kubeadm join", "kubeadm reset"},
		},
		{
			// Первая попытка падает на вычёркивании участника etcd —
			// каскад обязан повторить без неё.
			name:   "каскад: reset без вычёркивания участника",
			script: cpScript,
			st:     nodeState{apiserver: true, kubeletConf: true, resetFails: 1},
			want:   []string{"kubeadm reset -f\n", "kubeadm reset -f --skip-phases=remove-etcd-member\n", "kubeadm join 10.0.0.1:6443"},
			none:   []string{"dropping the node state"},
		},
		{
			name:   "каскад: только очистка узла",
			script: cpScript,
			st:     nodeState{apiserver: true, kubeletConf: true, resetFails: 2},
			want:   []string{"dropping the node state", "kubeadm reset phase cleanup-node\n", "kubeadm join 10.0.0.1:6443"},
		},
		{
			name:   "каскад: снести нечем",
			script: cpScript,
			st:     nodeState{apiserver: true, kubeletConf: true, resetFails: 3},
			want:   []string{"kubeadm reset phase cleanup-node"},
			none:   []string{"kubeadm join"},
			fail:   true,
		},
	} {
		log, err := runInstallGuard(t, tc.script, tc.st)
		if tc.fail != (err != nil) {
			t.Errorf("%s: ошибка %v, хотели fail=%v\n%s", tc.name, err, tc.fail, log)
		}
		for _, w := range tc.want {
			if !strings.Contains(log, w) {
				t.Errorf("%s: в журнале нет %q:\n%s", tc.name, w, log)
			}
		}
		for _, n := range tc.none {
			if strings.Contains(log, n) {
				t.Errorf("%s: в журнале лишнее %q:\n%s", tc.name, n, log)
			}
		}
	}
}

// Force — принудительный повтор шага установки (окно шагов: оператор
// отметил выполненный шаг заново): guard «уже вступил/уже инициализирован»
// не спрашивается, узел сбрасывается и роль ставится заново. Без этого
// повтор входа cp-2, который в прошлый раз отчитался успехом по локальному
// kubelet.conf, снова вышел бы 0, ничего не пересобрав.
func TestInstallForceResetsNode(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("нет bash")
	}
	initSpec := InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, Single: true, Force: true}
	cpSpec := InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "10.0.0.1:6443", Token: "t", CAHash: "h", CertKey: certKey, Force: true}
	workerSpec := InstallSpec{Flavor: FlavorKubeadm, Role: RoleAgent, ServerURL: "10.0.0.1:6443", Token: "t", CAHash: "h", Force: true}
	// У init хвост после «mkdir -p /root/.kube» режем, как в guard-тесте:
	// в тестовой среде нет прав на /root, а force-повтор живёт до него.
	initScript := guardCut(installStep(t, initSpec), "mkdir -p /root/.kube")
	cpScript := installStep(t, cpSpec)
	workerScript := installStep(t, workerSpec)
	for _, s := range []InstallSpec{initSpec, cpSpec, workerSpec} {
		checkBashSyntax(t, s.Flavor+" force install step", installStep(t, s))
	}
	// Скрипт принудительного повтора не содержит прежних guard'ов: их ветка
	// «успех без действий» ровно то, что Force обязан обойти.
	if strings.Contains(initScript, "already initialized") || strings.Contains(cpScript, "node already joined") || strings.Contains(workerScript, "node already joined") {
		t.Error("force-скрипт оставил guard «уже сделано»")
	}
	if !strings.Contains(initScript, "forced re-init") || !strings.Contains(cpScript, "forced re-join") {
		t.Error("force-скрипт не говорит, что повтор принудительный")
	}
	// Живой узел force-повтор не щадит: сброс и установка заново.
	for _, tc := range []struct {
		name   string
		script string
		st     nodeState
		want   []string
		none   []string
	}{
		{
			name:   "force init на живом control plane",
			script: initScript,
			st:     nodeState{adminConf: true, apiserver: true, etcdMember: true, kubeletConf: true, healthzOK: true},
			want:   []string{"forced re-init", "kubeadm reset -f\n", "kubeadm init "},
			none:   []string{"already initialized", "SKIP_INIT"},
		},
		{
			name:   "force вход control plane на «присоединённом» узле",
			script: cpScript,
			st:     nodeState{apiserver: true, etcdMember: true, kubeletConf: true, kubeletUp: true},
			want:   []string{"forced re-join", "kubeadm reset -f\n", "kubeadm join 10.0.0.1:6443"},
			none:   []string{"node already joined"},
		},
		{
			name:   "force вход работника на готовом узле",
			script: workerScript,
			st:     nodeState{kubeletConf: true, kubeletUp: true},
			want:   []string{"forced re-join", "kubeadm reset -f\n", "kubeadm join 10.0.0.1:6443"},
			none:   []string{"node already joined"},
		},
	} {
		log, err := runInstallGuard(t, tc.script, tc.st)
		if err != nil {
			t.Errorf("%s: %v\n%s", tc.name, err, log)
		}
		for _, w := range tc.want {
			if !strings.Contains(log, w) {
				t.Errorf("%s: в журнале нет %q:\n%s", tc.name, w, log)
			}
		}
		for _, n := range tc.none {
			if strings.Contains(log, n) {
				t.Errorf("%s: в журнале лишнее %q:\n%s", tc.name, n, log)
			}
		}
	}
	// k3s: force снимает предыдущую установку её же скриптом удаления —
	// server и agent своими. Без Force скриптов удаления в шаге нет.
	k3sServer := installStep(t, InstallSpec{Flavor: FlavorK3s, Role: RoleServer, Force: true})
	k3sAgent := installStep(t, InstallSpec{Flavor: FlavorK3s, Role: RoleAgent, Force: true})
	plain := installStep(t, InstallSpec{Flavor: FlavorK3s, Role: RoleServer})
	checkBashSyntax(t, "k3s force server", k3sServer)
	checkBashSyntax(t, "k3s force agent", k3sAgent)
	if !strings.Contains(k3sServer, "k3s-uninstall.sh") || strings.Contains(k3sServer, "k3s-agent-uninstall.sh") {
		t.Errorf("force server k3s: не тот скрипт удаления:\n%s", k3sServer)
	}
	if !strings.Contains(k3sAgent, "k3s-agent-uninstall.sh") {
		t.Errorf("force agent k3s: нет скрипта удаления agent:\n%s", k3sAgent)
	}
	if strings.Contains(plain, "uninstall") {
		t.Errorf("без Force k3s всё равно удаляет старое:\n%s", plain)
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

// certKey — ключ сертификатов нужной длины (64 hex-символа); в тестах
// фикстуры собираются вокруг него.
const certKey = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809"

// Общий адрес control plane: порт по умолчанию, форма имени, IPv6 без
// скобок — не порт.
func TestEndpointHostPort(t *testing.T) {
	for _, tc := range []struct {
		in   string
		host string
		port int
	}{
		{"k8s.example.com", "k8s.example.com", 6443},
		{"K8S.Example.COM:7443", "k8s.example.com", 7443},
		{"10.0.0.5", "10.0.0.5", 6443},
		{"10.0.0.5:7443", "10.0.0.5", 7443},
		{"::1", "::1", 6443},
		{"[::1]:7443", "::1", 7443},
		{"k8s.example.com.", "k8s.example.com.", 6443},
	} {
		host, port, err := EndpointHostPort(tc.in)
		if err != nil || host != tc.host || port != tc.port {
			t.Errorf("%q: %q %d %v", tc.in, host, port, err)
		}
	}
	for _, bad := range []string{"", ":7443", "k8s.example.com:0", "k8s.example.com:70000", "k8s_example", "k8s example", "-x",
		"10.0.0.5:6443:7443", "k8s.example..com:6443", "..example.com", "10.0.0.5:abc"} {
		if _, _, err := EndpointHostPort(bad); err == nil {
			t.Errorf("прошло: %q", bad)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"k8s.example.com", "k8s.example.com:6443"},
		{"::1", "[::1]:6443"},
		{"10.0.0.5:7443", "10.0.0.5:7443"},
		{"не понятно", "не понятно"}, // плохое значение отдаётся как есть — проверит Validate вызывающей стороны
	} {
		if got := JoinServer(tc.in); got != tc.want {
			t.Errorf("JoinServer(%q) = %q, хотели %q", tc.in, got, tc.want)
		}
	}
}

// Форма endpoint'а и ключа сертификатов проверяются на хосте, а не на
// машине: пустой ключ у входа control plane — ошибка формы.
func TestValidateEndpointAndCertKey(t *testing.T) {
	for _, ok := range []InstallSpec{
		{Flavor: FlavorKubeadm, Role: RoleServer, ControlPlaneEndpoint: "k8s.example.com"},
		{Flavor: FlavorKubeadm, Role: RoleServer, ControlPlaneEndpoint: "10.0.0.5:7443"},
		{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "k8s.example.com:6443", Token: "abcdef.0123456789abcdef", CAHash: "sha256:00", CertKey: certKey},
		// k3s своего адреса не знает — там узел входит на конкретный server
		{Flavor: FlavorK3s, Role: RoleServer},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("не прошло: %+v: %v", ok, err)
		}
	}
	for _, bad := range []InstallSpec{
		{Flavor: FlavorK3s, Role: RoleServer, ControlPlaneEndpoint: "k8s.example.com"},
		{Flavor: FlavorKubeadm, Role: RoleServer, ControlPlaneEndpoint: "k8s_example"},
		{Flavor: FlavorKubeadm, Role: RoleServer, ControlPlaneEndpoint: "k8s.example.com:0"},
		{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "k8s.example.com:6443", Token: "t", CAHash: "h"},
		{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "k8s.example.com:6443", Token: "t", CAHash: "h", CertKey: "deadbeef"},
		{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "k8s.example.com:6443", Token: "t", CAHash: "h", CertKey: strings.Repeat("g", 64)},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("прошло: %+v", bad)
		}
	}
	// Секрет в текст ошибки не попадает.
	err := (InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "h:6443", Token: "t", CAHash: "h", CertKey: certKey[:60] + "XXXX"}).Validate()
	if err == nil || strings.Contains(err.Error(), certKey[:40]) {
		t.Errorf("ключ виден в ошибке: %v", err)
	}
}

// Ключ берётся целиком из вывода фазы, а не «последнее слово»: шум до и
// после него, переход вывода в stderr — ключ всё равно находится, а
// ничего похожего на ключ — пусто, а не мусор.
func TestParseCertKey(t *testing.T) {
	if got := parseCertKey("[upload-certs] Storing the certificates in Secret \"kubeadm-certs\"\n[upload-certs] Using certificate key:\n" + certKey + "\n"); got != certKey {
		t.Errorf("stdout: %q", got)
	}
	if got := parseCertKey("W: noise\n" + certKey + "\n"); got != certKey {
		t.Errorf("stderr: %q", got)
	}
	for _, none := range []string{"", "error: unknown flag: --upload-certs", "abcd", "Secret \"kubeadm-certs\" created", "sha256:" + certKey} {
		if got := parseCertKey(none); got != "" {
			t.Errorf("%q: %q", none, got)
		}
	}
}

// kubeadm с общим адресом: endpoint в init, вход по нему, advertise-адрес
// у дополнительного control plane, адрес API у Cilium — тот же.
func TestStepsKubeadmHA(t *testing.T) {
	init := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, ClusterInit: true,
		ControlPlaneEndpoint: "k8s.example.com", NodeIP: "10.0.0.5", NodeName: "cp-1"}))
	for _, want := range []string{"--control-plane-endpoint=k8s.example.com:6443", "--upload-certs",
		"--apiserver-advertise-address=10.0.0.5", "--node-name=cp-1"} {
		if !strings.Contains(init, want) {
			t.Errorf("kubeadm init: нет %q", want)
		}
	}
	// Один control plane сертификаты в кластер не грузит: ключ живёт два
	// часа и всё равно берётся перед входом.
	one := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, ControlPlaneEndpoint: "k8s.example.com:7443"}))
	if !strings.Contains(one, "--control-plane-endpoint=k8s.example.com:7443") || strings.Contains(one, "--upload-certs") {
		t.Errorf("kubeadm с одним control plane: %s", one)
	}
	cp := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, ServerURL: "k8s.example.com:7443",
		Token: "abcdef.0123456789abcdef", CAHash: "sha256:00", CertKey: certKey, NodeIP: "10.0.0.6", NodeName: "cp-2"}))
	if !strings.Contains(cp, "kubeadm join k8s.example.com:7443 --token abcdef.0123456789abcdef --discovery-token-ca-cert-hash sha256:00 --control-plane --certificate-key "+certKey) {
		t.Errorf("вход control plane: %s", cp)
	}
	if !strings.Contains(cp, "--apiserver-advertise-address=10.0.0.6") || !strings.Contains(cp, "--node-name=cp-2") {
		t.Errorf("вход control plane без адреса узла: %s", cp)
	}
	w := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleAgent, ServerURL: "k8s.example.com:7443",
		Token: "abcdef.0123456789abcdef", CAHash: "sha256:00", NodeIP: "10.0.0.7"}))
	if !strings.Contains(w, "kubeadm join k8s.example.com:7443") || strings.Contains(w, "--control-plane") ||
		strings.Contains(w, "--apiserver-advertise-address") {
		t.Errorf("worker: %s", w)
	}
	// init без endpoint: ни адреса, ни загрузки сертификатов.
	bare := joinScripts(Steps(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, NodeName: "cp-1"}))
	if strings.Contains(bare, "--control-plane-endpoint") || strings.Contains(bare, "--upload-certs") {
		t.Errorf("kubeadm без endpoint: %s", bare)
	}
	// Cilium при замене kube-proxy смотрит на API по общему адресу и его
	// порту — 7443 вместо 6443 агенту чужд. Шаг cilium живёт в задании
	// KindCilium, а не в задании узла — проверяется сам шаг.
	cil := ciliumStep(InstallSpec{Flavor: FlavorKubeadm, Role: RoleServer, CNI: "cilium", KubeProxyReplacement: true,
		ControlPlaneEndpoint: "k8s.example.com:7443", APIAddr: "10.0.0.5"}, "/etc/kubernetes/admin.conf").Script
	if !strings.Contains(cil, "--set k8sServiceHost=k8s.example.com --set k8sServicePort=7443") {
		t.Errorf("cilium с endpoint: %s", cil)
	}
	for name, s := range map[string]string{"init": init, "one": one, "join cp": cp, "join worker": w, "cilium": cil} {
		checkBashSyntax(t, "kubeadm HA "+name, s)
	}
}
