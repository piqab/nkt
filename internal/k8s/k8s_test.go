package k8s

import (
	"strings"
	"testing"
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
	for _, want := range []string{"pkgs.k8s.io", "containerd", "SystemdCgroup = true", "br_netfilter", "kubeadm init --pod-network-cidr=10.244.0.0/16 --apiserver-cert-extra-sans=203.0.113.5", "kube-flannel.yml", "taint nodes --all"} {
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
