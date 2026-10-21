package k8s

import (
	"fmt"
	"strings"
)

// CiliumIngressScript — включить Ingress-контроллер Cilium в режиме host
// network: Envoy слушает port на каждом узле (без LoadBalancer), Cilium —
// контроллер по умолчанию. Версия Cilium — работающая: без --version
// «cilium upgrade» заодно обновил бы Cilium до версии, встроенной в CLI.
// Нужна замена kube-proxy (без неё Ingress Cilium не работает) — иначе
// отказ с объяснением. port проверен вызывающим (1024…65535).
func CiliumIngressScript(flavor string, port int) string {
	kubeconfig := "/etc/kubernetes/admin.conf"
	if flavor == FlavorK3s {
		kubeconfig = "/etc/rancher/k3s/k3s.yaml"
	}
	return strings.Join([]string{
		"set -e",
		"export KUBECONFIG=" + kubeconfig,
		"command -v cilium >/dev/null || { echo 'cilium CLI not found: the cluster was created without Cilium'; exit 1; }",
		"kubectl -n kube-system get ds cilium >/dev/null",
		"KPR=$(kubectl -n kube-system get cm cilium-config -o jsonpath='{.data.kube-proxy-replacement}' 2>/dev/null || true)",
		`[ "$KPR" = "true" ] || { echo "Cilium Ingress needs kube-proxy replacement (kube-proxy-replacement=$KPR): create the cluster with Cilium replacing kube-proxy"; exit 1; }`,
		"V=$(kubectl -n kube-system get ds cilium -o jsonpath='{.spec.template.spec.containers[0].image}' | sed -E 's/.*:(v[0-9.]+).*/\\1/')",
		`echo "cilium $V"`,
		fmt.Sprintf(`cilium upgrade --version "$V" --reuse-values --set ingressController.enabled=true --set ingressController.default=true --set ingressController.loadbalancerMode=shared --set ingressController.hostNetwork.enabled=true --set ingressController.hostNetwork.sharedListenerPort=%d`, port),
		"kubectl -n kube-system rollout restart deployment/cilium-operator",
		"kubectl -n kube-system rollout restart ds/cilium",
		"kubectl -n kube-system rollout status ds/cilium --timeout=10m",
		"kubectl -n kube-system rollout status deployment/cilium-operator --timeout=5m",
		"kubectl get ingressclass cilium",
	}, "\n") + "\n"
}
