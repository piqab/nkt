package hub

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Защита namespace клиента в общем кластере (1.17.12). Клиент работает и
// своим kubeconfig напрямую, мимо хаба, поэтому правила живут в самом
// кластере:
//
//   - Pod Security «restricted» — без привилегий, сети и портов узла,
//     лишних capabilities;
//   - ValidatingAdmissionPolicy на namespace с меткой nkt.io/tenant:
//     Service — только ClusterIP и без externalIPs (externalIPs —
//     перехват чужих адресов, CVE-2020-8554; NodePort и LoadBalancer —
//     порты на всех узлах и адреса из пула администратора); Ingress и
//     HTTPRoute/GRPCRoute — только с именами в домене namespace
//     (аннотация nkt.io/domain, без домена — нельзя); Gateway и прочие
//     маршруты Gateway API — нельзя; сетевые политики учётные записи
//     клиента не меняют (политики складываются — своя открыла бы то, что
//     закрыла наша);
//   - NetworkPolicy: входящие — из своего namespace, из kube-system и от
//     ingress Cilium; исходящие — DNS кластера, свой namespace и интернет
//     без частных сетей и адреса метаданных, без порта 25, если почта
//     токену не разрешена.
//
// Нужны Cilium (он исполняет политики) и Kubernetes 1.30+ (политики
// допуска v1). Те же правила хаб проверяет сам — ранний понятный отказ.

// tenantLabel и tenantDomain — метка и аннотация namespace клиента.
const (
	tenantLabel  = "nkt.io/tenant"
	tenantDomain = "nkt.io/domain"
)

// tenantPrivate — куда из namespace клиента нельзя (как nkt_isolation).
var tenantPrivate = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "127.0.0.0/8"}

// clusterIsCilium — кластер nkt с Cilium (по его описанию).
func clusterIsCilium(cl store.Cluster) bool {
	var spec ClusterSpec
	return json.Unmarshal([]byte(cl.SpecJSON), &spec) == nil && spec.CNI == "cilium"
}

// tenantManifest — namespace клиента с метками, квотой и политиками.
func tenantManifest(ns string, t store.APIToken) string {
	var b strings.Builder
	fmt.Fprintf(&b, `apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
    %[2]s: "true"
  annotations:
    nkt.io/token: %[3]q
    %[4]s: %[5]q
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: nkt-same-namespace
  namespace: %[1]s
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector: {}
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
---
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: nkt-ingress-controller
  namespace: %[1]s
spec:
  endpointSelector: {}
  ingress:
    - fromEntities: [ingress]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: nkt-egress
  namespace: %[1]s
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - {protocol: UDP, port: 53}
        - {protocol: TCP, port: 53}
    - to:
        - podSelector: {}
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except: [%[6]s]
`, ns, tenantLabel, t.Name, tenantDomain, t.Perms.FlyDomain, strings.Join(tenantPrivate, ", "))
	if !t.Perms.AllowMail {
		b.WriteString(`      ports:
        - {protocol: TCP, port: 1, endPort: 24}
        - {protocol: TCP, port: 26, endPort: 65535}
        - {protocol: UDP, port: 1, endPort: 65535}
`)
	}
	if q := tokenK8sQuotaManifest(ns, t.Name, t.Perms.Limits); q != "" {
		// Namespace уже есть выше — без первого документа.
		if _, rest, ok := strings.Cut(q, "\n---\n"); ok {
			b.WriteString("---\n" + rest)
		}
	}
	return b.String()
}

// tenantAdmissionManifest — политики допуска для namespace клиентов
// (общие на кластер, применяются заново при каждом новом namespace).
const tenantAdmissionManifest = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: nkt-tenant-services
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["services"]
  validations:
    - expression: "!has(object.spec.type) || object.spec.type == 'ClusterIP'"
      message: "nkt: a client namespace allows only ClusterIP services (no NodePort, LoadBalancer or ExternalName)"
    - expression: "!has(object.spec.externalIPs) || size(object.spec.externalIPs) == 0"
      message: "nkt: externalIPs are not allowed in a client namespace"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: nkt-tenant-services
spec:
  policyName: nkt-tenant-services
  validationActions: [Deny]
  matchResources:
    namespaceSelector:
      matchLabels:
        nkt.io/tenant: "true"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: nkt-tenant-ingress
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: ["networking.k8s.io"]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["ingresses"]
  variables:
    - name: domain
      expression: "has(namespaceObject.metadata.annotations) && 'nkt.io/domain' in namespaceObject.metadata.annotations ? namespaceObject.metadata.annotations['nkt.io/domain'] : ''"
  validations:
    - expression: "variables.domain != '' && !has(object.spec.defaultBackend) && has(object.spec.rules) && object.spec.rules.all(r, has(r.host) && (r.host == variables.domain || r.host.endsWith('.' + variables.domain)))"
      message: "nkt: ingress hosts must be the namespace domain or names under it"
    - expression: "!has(object.spec.tls) || object.spec.tls.all(t, !has(t.hosts) || t.hosts.all(h, h == variables.domain || h.endsWith('.' + variables.domain)))"
      message: "nkt: ingress TLS hosts must be the namespace domain or names under it"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: nkt-tenant-ingress
spec:
  policyName: nkt-tenant-ingress
  validationActions: [Deny]
  matchResources:
    namespaceSelector:
      matchLabels:
        nkt.io/tenant: "true"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: nkt-tenant-gateway
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: ["gateway.networking.k8s.io"]
        apiVersions: ["*"]
        operations: ["CREATE", "UPDATE"]
        resources: ["gateways", "httproutes", "grpcroutes", "tlsroutes", "tcproutes", "udproutes"]
  variables:
    - name: domain
      expression: "has(namespaceObject.metadata.annotations) && 'nkt.io/domain' in namespaceObject.metadata.annotations ? namespaceObject.metadata.annotations['nkt.io/domain'] : ''"
  validations:
    - expression: "(request.resource.resource == 'httproutes' || request.resource.resource == 'grpcroutes') && variables.domain != '' && has(object.spec.hostnames) && size(object.spec.hostnames) > 0 && object.spec.hostnames.all(h, h == variables.domain || h.endsWith('.' + variables.domain))"
      message: "nkt: a client namespace allows only HTTP/gRPC routes with names in the namespace domain"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: nkt-tenant-gateway
spec:
  policyName: nkt-tenant-gateway
  validationActions: [Deny]
  matchResources:
    namespaceSelector:
      matchLabels:
        nkt.io/tenant: "true"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: nkt-tenant-netpol
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: ["networking.k8s.io"]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE", "DELETE"]
        resources: ["networkpolicies"]
      - apiGroups: ["cilium.io"]
        apiVersions: ["*"]
        operations: ["CREATE", "UPDATE", "DELETE"]
        resources: ["ciliumnetworkpolicies"]
  matchConditions:
    - name: client-accounts
      expression: "request.userInfo.username.startsWith('system:serviceaccount:') && !request.userInfo.username.startsWith('system:serviceaccount:kube-system:')"
  validations:
    - expression: "false"
      message: "nkt: network policies of a client namespace are set by the hub"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: nkt-tenant-netpol
spec:
  policyName: nkt-tenant-netpol
  validationActions: [Deny]
  matchResources:
    namespaceSelector:
      matchLabels:
        nkt.io/tenant: "true"
`

// tokenK8sDenyKinds — виды, которые токену нельзя и в своём namespace:
// сетевые политики ставит хаб, Gateway и не-HTTP маршруты — общая
// инфраструктура.
var tokenK8sDenyKinds = map[string]bool{
	"NetworkPolicy": true, "CiliumNetworkPolicy": true, "CiliumClusterwideNetworkPolicy": true,
	"Gateway": true, "TLSRoute": true, "TCPRoute": true, "UDPRoute": true,
}

// inDomain — имя — сам домен или под ним.
func inDomain(host, domain string) bool {
	return domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}

// tokenK8sDocCheck — правила политик допуска для одного документа (хаб
// проверяет их до кластера): Service, Ingress, маршруты Gateway API.
func tokenK8sDocCheck(doc map[string]any, kind, name, domain string) error {
	if tokenK8sDenyKinds[kind] {
		return msgs.Errorf("hub.tokenK8sKind", kind, name, kind)
	}
	spec, _ := doc["spec"].(map[string]any)
	strs := func(v any) []string {
		var out []string
		list, _ := v.([]any)
		for _, x := range list {
			s, _ := x.(string)
			out = append(out, s)
		}
		return out
	}
	switch kind {
	case "Service":
		if tp, _ := spec["type"].(string); tp != "" && tp != "ClusterIP" {
			return msgs.Errorf("hub.tokenK8sServiceType", name, tp)
		}
		if len(strs(spec["externalIPs"])) > 0 {
			return msgs.Errorf("hub.tokenK8sExternalIPs", name)
		}
	case "Ingress":
		rules, _ := spec["rules"].([]any)
		if domain == "" || spec["defaultBackend"] != nil || len(rules) == 0 {
			return msgs.Errorf("hub.tokenK8sIngressHost", name, "*", domain)
		}
		for _, r := range rules {
			rm, _ := r.(map[string]any)
			if h, _ := rm["host"].(string); !inDomain(h, domain) {
				return msgs.Errorf("hub.tokenK8sIngressHost", name, h, domain)
			}
		}
		tls, _ := spec["tls"].([]any)
		for _, t := range tls {
			tm, _ := t.(map[string]any)
			for _, h := range strs(tm["hosts"]) {
				if !inDomain(h, domain) {
					return msgs.Errorf("hub.tokenK8sIngressHost", name, h, domain)
				}
			}
		}
	case "HTTPRoute", "GRPCRoute":
		hosts := strs(spec["hostnames"])
		if len(hosts) == 0 || slices.ContainsFunc(hosts, func(h string) bool { return !inDomain(h, domain) }) {
			return msgs.Errorf("hub.tokenK8sIngressHost", name, strings.Join(hosts, ","), domain)
		}
	}
	return nil
}
