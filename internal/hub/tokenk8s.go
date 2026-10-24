package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// k8s для токена «по правам»: манифест — только в namespace из списка
// токена, без видов, меняющих кластер целиком, и без доступа к узлу
// (privileged, сеть и pid узла, hostPath). В каждом namespace хаб
// ставит ResourceQuota и LimitRange — потолок держит сам кластер, даже
// для подов, созданных изнутри.

// NamespaceRe — имя namespace (DNS-1123 label).
var NamespaceRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// tokenK8sClusterWide — виды, которые токену нельзя: кластерные или
// меняющие его квоту.
var tokenK8sClusterWide = map[string]bool{
	"ClusterRole": true, "ClusterRoleBinding": true, "CustomResourceDefinition": true, "PersistentVolume": true,
	"StorageClass": true, "PriorityClass": true, "ValidatingWebhookConfiguration": true, "MutatingWebhookConfiguration": true,
	"APIService": true, "Node": true, "RuntimeClass": true, "IngressClass": true, "CSIDriver": true, "VolumeAttachment": true,
	"ResourceQuota": true, "LimitRange": true, "PodSecurityPolicy": true, "ValidatingAdmissionPolicy": true,
	"ValidatingAdmissionPolicyBinding": true, "CertificateSigningRequest": true,
}

// tokenK8sUnsafeKeys — поля спецификации, дающие доступ к узлу.
var tokenK8sUnsafeKeys = map[string]bool{"hostNetwork": true, "hostPID": true, "hostIPC": true, "privileged": true, "hostPath": true, "allowPrivilegeEscalation": true}

// tokenK8sNamespaces — namespace манифеста (и проверка: свои, без
// запрещённого). Namespace-объект — только свой.
func tokenK8sNamespaces(content string, allowed []string) ([]string, error) {
	dec := yaml.NewDecoder(strings.NewReader(content))
	var out []string
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, msgs.Errorf("k8s.manifestYAML", err.Error())
		}
		if doc == nil {
			continue
		}
		kind, _ := doc["kind"].(string)
		meta, _ := doc["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		ns, _ := meta["namespace"].(string)
		if kind == "Namespace" {
			if !slices.Contains(allowed, name) {
				return nil, msgs.Errorf("hub.tokenK8sNamespace", kind, name, name)
			}
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
			continue
		}
		if tokenK8sClusterWide[kind] {
			return nil, msgs.Errorf("hub.tokenK8sKind", kind, name, kind)
		}
		if !slices.Contains(allowed, ns) {
			return nil, msgs.Errorf("hub.tokenK8sNamespace", kind, name, ns)
		}
		if bad := unsafeK8sKey(doc); bad != "" {
			return nil, msgs.Errorf("hub.tokenK8sUnsafe", kind, name, bad)
		}
		if !slices.Contains(out, ns) {
			out = append(out, ns)
		}
	}
	return out, nil
}

// unsafeK8sKey — первый ключ доступа к узлу со значением «да» (hostPath —
// любым).
func unsafeK8sKey(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if tokenK8sUnsafeKeys[k] {
				if b, ok := val.(bool); k == "hostPath" || (ok && b) {
					return k
				}
			}
			if bad := unsafeK8sKey(val); bad != "" {
				return bad
			}
		}
	case []any:
		for _, it := range x {
			if bad := unsafeK8sKey(it); bad != "" {
				return bad
			}
		}
	}
	return ""
}

// tokenK8sQuotaManifest — namespace с квотой и пределами по умолчанию
// для контейнеров (без них квота limits.* не пустила бы под без своих
// limits).
func tokenK8sQuotaManifest(ns, token string, l store.TokenLimits) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n  annotations:\n    nkt.io/token: %q\n", ns, token)
	hard := []string{}
	if l.K8sCPU > 0 {
		c := strconv.FormatFloat(l.K8sCPU, 'f', -1, 64)
		hard = append(hard, fmt.Sprintf("limits.cpu: %q", c), fmt.Sprintf("requests.cpu: %q", c))
	}
	if l.K8sMemMB > 0 {
		hard = append(hard, fmt.Sprintf("limits.memory: \"%dMi\"", l.K8sMemMB), fmt.Sprintf("requests.memory: \"%dMi\"", l.K8sMemMB))
	}
	if l.K8sStorageGB > 0 {
		hard = append(hard, fmt.Sprintf("requests.storage: \"%dGi\"", l.K8sStorageGB))
	}
	if l.K8sPods > 0 {
		hard = append(hard, fmt.Sprintf("pods: \"%d\"", l.K8sPods))
	}
	if len(hard) > 0 {
		fmt.Fprintf(&b, "---\napiVersion: v1\nkind: ResourceQuota\nmetadata:\n  name: nkt-token\n  namespace: %s\nspec:\n  hard:\n", ns)
		for _, h := range hard {
			b.WriteString("    " + h + "\n")
		}
	}
	def := []string{}
	if l.ServiceCPU > 0 {
		def = append(def, fmt.Sprintf("cpu: %q", strconv.FormatFloat(l.ServiceCPU, 'f', -1, 64)))
	}
	if l.ServiceMemMB > 0 {
		def = append(def, fmt.Sprintf("memory: \"%dMi\"", l.ServiceMemMB))
	}
	if len(def) > 0 {
		fmt.Fprintf(&b, "---\napiVersion: v1\nkind: LimitRange\nmetadata:\n  name: nkt-token\n  namespace: %s\nspec:\n  limits:\n    - type: Container\n", ns)
		for _, part := range []string{"default", "defaultRequest", "max"} {
			b.WriteString("      " + part + ":\n")
			for _, d := range def {
				b.WriteString("        " + d + "\n")
			}
		}
	}
	return b.String()
}

// tokenK8sCheck — манифест в правах токена: кластеры из списка, свои
// namespace. Возвращает namespace манифеста.
func (s *Server) tokenK8sCheck(ctx context.Context, t store.APIToken, req manifestRequest) ([]string, error) {
	if len(t.Perms.Clusters) > 0 {
		for _, id := range req.Clusters {
			cl, err := s.db.ClusterByID(ctx, id)
			if err != nil {
				return nil, err
			}
			if !slices.Contains(t.Perms.Clusters, cl.Name) {
				return nil, msgs.Errorf("hub.tokenK8sCluster", cl.Name)
			}
		}
	}
	return tokenK8sNamespaces(req.Content, t.Perms.Namespaces)
}

// tokenK8sPrepare — квоты namespace токена в кластерах до применения:
// новый namespace — по суммам токена, ставится и учитывается.
func (s *Server) tokenK8sPrepare(ctx context.Context, lang msgs.Lang, t store.APIToken, req manifestRequest, namespaces []string) error {
	usage, err := s.db.ListTokenUsage(ctx, t.ID)
	if err != nil {
		return err
	}
	l := t.Perms.Limits
	var fail error
	s.forEachCluster(ctx, lang, req.Clusters, func(cl store.Cluster, hostID int64) ManifestResult {
		if fail != nil {
			return ManifestResult{}
		}
		for _, ns := range namespaces {
			u := store.TokenUsage{TokenID: t.ID, Kind: store.UsageK8sNS, HostID: cl.ID, Ref: ns, Label: ns + "@" + cl.Name,
				CPU: l.K8sCPU, MemMB: l.K8sMemMB, DiskGB: l.K8sStorageGB}
			known := slices.ContainsFunc(usage, func(x store.TokenUsage) bool { return x.Kind == u.Kind && x.HostID == u.HostID && x.Ref == ns })
			if !known {
				if err := s.tokenFits(ctx, t, u); err != nil {
					fail = err
					return ManifestResult{}
				}
			}
			var res struct {
				Output string `json:"output"`
			}
			body := map[string]string{"content": tokenK8sQuotaManifest(ns, t.Name, l), "note": "nkt token " + t.Name}
			if _, err := s.hub.HostAPI(ctx, hostID, "POST", "/api/k8s/apply", body, &res); err != nil {
				fail = msgs.Errorf("hub.tokenK8sQuota", ns, cl.Name, err)
				return ManifestResult{}
			}
			if err := s.db.SetTokenUsage(ctx, u); err != nil {
				fail = err
				return ManifestResult{}
			}
		}
		return ManifestResult{}
	})
	return fail
}
