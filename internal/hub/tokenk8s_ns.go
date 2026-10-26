package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Пространство имён в общем кластере для токена (1.17.8): токен «по
// правам» с действием «свои namespace» заводит namespace в разрешённом
// кластере (не больше потолка «namespace»), хаб ставит туда:
//
//   - метки Pod Security «restricted» (enforce, audit, warn);
//   - ResourceQuota и LimitRange по потолкам токена;
//   - NetworkPolicy: входящие — только из этого же namespace;
//   - учётную запись nkt-client с ролью edit только в этом namespace и её
//     долгоживущий токен (секрет service-account-token);
//
// и один раз отдаёт kubeconfig этой учётной записи (повторно — только
// перевыпуском: старый токен перестаёт работать). Свои namespace токену
// открыты и для манифестов (k8s.apply), логов и helm — helm через рендер
// на хосте и те же проверки манифеста, что у токена.

// #nosec G101 -- имена учётной записи и секрета Kubernetes, а не учётные данные.
const (
	ownNSKV       = "k8s.ownns"
	ownNSAccount  = "nkt-client"
	ownNSSecret   = "nkt-client-token"
	ownNSTokenTry = 15
)

// OwnNamespace — namespace, заведённый токеном.
type OwnNamespace struct {
	ClusterID int64  `json:"cluster_id"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	TokenID   int64  `json:"token_id"`
	Token     string `json:"token"`
}

var ownNSMu sync.Mutex

func (s *Server) ownNamespaces(ctx context.Context) []OwnNamespace {
	var out []OwnNamespace
	if raw, ok, _ := s.db.KVGet(ctx, ownNSKV); ok {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) saveOwnNamespaces(ctx context.Context, list []OwnNamespace) error {
	raw, _ := json.Marshal(list)
	return s.db.KVSet(ctx, ownNSKV, string(raw))
}

// tokenAllowedNamespaces — namespace, где токену можно: из прав и свои.
func (s *Server) tokenAllowedNamespaces(ctx context.Context, t store.APIToken) []string {
	out := slices.Clone(t.Perms.Namespaces)
	for _, o := range s.ownNamespaces(ctx) {
		if o.TokenID == t.ID && !slices.Contains(out, o.Namespace) {
			out = append(out, o.Namespace)
		}
	}
	return out
}

// ownNSManifest — namespace клиента: PSA restricted, сетевая политика,
// учётная запись с ролью edit и её секрет; квота — tokenK8sQuotaManifest.
func ownNSManifest(ns, token string, l store.TokenLimits) string {
	var b strings.Builder
	fmt.Fprintf(&b, `apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
  annotations:
    nkt.io/token: %[2]q
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
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[3]s
  namespace: %[1]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: %[3]s-edit
  namespace: %[1]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: edit
subjects:
  - kind: ServiceAccount
    name: %[3]s
    namespace: %[1]s
---
`, ns, token, ownNSAccount)
	b.WriteString(ownNSSecretManifest(ns))
	if q := tokenK8sQuotaManifest(ns, token, l); q != "" {
		// Namespace там уже есть — без первого документа.
		if _, rest, ok := strings.Cut(q, "\n---\n"); ok {
			b.WriteString("---\n" + rest)
		}
	}
	return b.String()
}

func ownNSSecretManifest(ns string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
type: kubernetes.io/service-account-token
metadata:
  name: %s
  namespace: %s
  annotations:
    kubernetes.io/service-account.name: %s
`, ownNSSecret, ns, ownNSAccount)
}

// ownNSTarget — кластер по имени в пределах токена и его первый узел.
func (s *Server) ownNSTarget(ctx context.Context, t store.APIToken, cluster string) (store.Cluster, int64, error) {
	list, err := s.db.ListClusters(ctx)
	if err != nil {
		return store.Cluster{}, 0, err
	}
	for _, cl := range list {
		if cl.Name != cluster {
			continue
		}
		if len(t.Perms.Clusters) > 0 && !slices.Contains(t.Perms.Clusters, cl.Name) || !s.clusterInScope(ctx, cl.ID) {
			return store.Cluster{}, 0, msgs.Errorf("hub.tokenK8sCluster", cl.Name)
		}
		hosts, err := s.db.ClusterHosts(ctx, cl.ID)
		if err != nil || len(hosts) == 0 {
			return store.Cluster{}, 0, msgs.Errorf("hub.clusterNoNodes")
		}
		return cl, hosts[0].ID, nil
	}
	return store.Cluster{}, 0, msgs.Errorf("hub.tokenK8sCluster", cluster)
}

// ownNSKubeconfig — kubeconfig учётной записи (токен секрета появляется
// не сразу — несколько попыток).
func (s *Server) ownNSKubeconfig(ctx context.Context, user string, cl store.Cluster, hostID int64, ns string) (string, error) {
	var last error
	for i := 0; i < ownNSTokenTry; i++ {
		var out struct {
			Kubeconfig string `json:"kubeconfig"`
		}
		_, err := s.hostCall(ctx, user, hostID, "POST", "/api/k8s/sa-kubeconfig",
			map[string]string{"namespace": ns, "secret": ownNSSecret, "server": cl.ServerAddr}, &out)
		if err == nil && out.Kubeconfig != "" {
			return out.Kubeconfig, nil
		}
		last = err
		if !sleepCtx(ctx, time.Second) {
			return "", ctx.Err()
		}
	}
	return "", last
}

// handleOwnNamespaces — GET /hub/k8s/own: свои namespace токена.
func (s *Server) handleOwnNamespaces(w http.ResponseWriter, r *http.Request) {
	t, ok := tokenFromContext(r.Context())
	if !ok || t.Role != store.TokenRoleCustom {
		writeError(w, http.StatusForbidden, msgs.Tc(r.Context(), "auth.tokenOnly"))
		return
	}
	out := []OwnNamespace{}
	for _, o := range s.ownNamespaces(r.Context()) {
		if o.TokenID == t.ID {
			out = append(out, o)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespaces": out})
}

// handleOwnNamespaceCreate — POST /hub/k8s/own {"cluster","namespace"}:
// namespace с квотой, политикой и учётной записью; в ответе — kubeconfig
// (один раз).
func (s *Server) handleOwnNamespaceCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, ok := tokenFromContext(ctx)
	if !ok || t.Role != store.TokenRoleCustom {
		writeError(w, http.StatusForbidden, msgs.Tc(ctx, "auth.tokenOnly"))
		return
	}
	var req struct {
		Cluster   string `json:"cluster"`
		Namespace string `json:"namespace"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ns := req.Namespace
	if !NamespaceRe.MatchString(ns) || strings.HasPrefix(ns, "kube-") || ns == "default" || strings.HasPrefix(ns, "nkt-") {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.ownNSBadName", ns))
		return
	}
	ownNSMu.Lock()
	defer ownNSMu.Unlock()
	list := s.ownNamespaces(ctx)
	n := 0
	for _, o := range list {
		if o.TokenID == t.ID {
			n++
		}
	}
	if n >= t.Perms.Limits.K8sNamespaces {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("auth.tokenNamespaces", t.Perms.Limits.K8sNamespaces))
		return
	}
	cl, hostID, err := s.ownNSTarget(ctx, t, req.Cluster)
	if err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	user := auth.Username(ctx)
	// Чужой или уже существующий namespace не забирается.
	var exists struct {
		Exists bool `json:"exists"`
	}
	if _, err := s.hostCall(ctx, user, hostID, "POST", "/api/k8s/namespace-exists", map[string]string{"namespace": ns}, &exists); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if exists.Exists {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.ownNSExists", ns, cl.Name))
		return
	}
	l := t.Perms.Limits
	u := store.TokenUsage{TokenID: t.ID, Kind: store.UsageK8sNS, HostID: cl.ID, Ref: ns, Label: ns + "@" + cl.Name, CPU: l.K8sCPU, MemMB: l.K8sMemMB, DiskGB: l.K8sStorageGB}
	if err := s.tokenFits(ctx, t, u); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	if _, err := s.hostCall(ctx, user, hostID, "POST", "/api/k8s/apply", map[string]string{"content": ownNSManifest(ns, t.Name, l), "note": "nkt token " + t.Name}, nil); err != nil {
		s.db.Audit(ctx, user, "k8s.own_namespace", ns+"@"+cl.Name, "error", err.Error())
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	_ = s.db.SetTokenUsage(ctx, u)
	list = append(list, OwnNamespace{ClusterID: cl.ID, Cluster: cl.Name, Namespace: ns, TokenID: t.ID, Token: t.Name})
	if err := s.saveOwnNamespaces(ctx, list); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, user, "k8s.own_namespace", ns+"@"+cl.Name, "ok", nil)
	cfg, err := s.ownNSKubeconfig(ctx, user, cl, hostID, ns)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"namespace": ns, "cluster": cl.Name, "kubeconfig_error": msgs.Localize(msgs.FromContext(ctx), err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespace": ns, "cluster": cl.Name, "kubeconfig": cfg})
}

// ownNS — свой namespace токена из пути ({cluster}, {ns}).
func (s *Server) ownNS(w http.ResponseWriter, r *http.Request) (store.APIToken, store.Cluster, int64, string, bool) {
	ctx := r.Context()
	t, ok := tokenFromContext(ctx)
	ns := chi.URLParam(r, "ns")
	if !ok || t.Role != store.TokenRoleCustom || !slices.Contains(s.tokenAllowedNamespaces(ctx, t), ns) {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("hub.ownNSNotOwn", ns))
		return t, store.Cluster{}, 0, "", false
	}
	cl, hostID, err := s.ownNSTarget(ctx, t, chi.URLParam(r, "cluster"))
	if err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return t, store.Cluster{}, 0, "", false
	}
	return t, cl, hostID, ns, true
}

// isOwnNS — namespace заведён этим токеном в этом кластере.
func (s *Server) isOwnNS(ctx context.Context, t store.APIToken, clusterID int64, ns string) bool {
	return slices.ContainsFunc(s.ownNamespaces(ctx), func(o OwnNamespace) bool {
		return o.TokenID == t.ID && o.ClusterID == clusterID && o.Namespace == ns
	})
}

// handleOwnNamespaceKubeconfig — POST /hub/k8s/own/{cluster}/{ns}/kubeconfig:
// перевыпуск — секрет заводится заново, прежний токен больше не работает.
func (s *Server) handleOwnNamespaceKubeconfig(w http.ResponseWriter, r *http.Request) {
	t, cl, hostID, ns, ok := s.ownNS(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !s.isOwnNS(ctx, t, cl.ID, ns) {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("hub.ownNSNotOwn", ns))
		return
	}
	user := auth.Username(ctx)
	_, _ = s.hostCall(ctx, user, hostID, "POST", "/api/k8s/objects/action", map[string]string{"kind": "secrets", "namespace": ns, "name": ownNSSecret, "action": "delete"}, nil)
	if _, err := s.hostCall(ctx, user, hostID, "POST", "/api/k8s/apply", map[string]string{"content": ownNSSecretManifest(ns), "note": "nkt token " + t.Name}, nil); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	cfg, err := s.ownNSKubeconfig(ctx, user, cl, hostID, ns)
	s.db.Audit(ctx, user, "k8s.own_kubeconfig", ns+"@"+cl.Name, auditOutcome(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"kubeconfig": cfg})
}

// handleOwnNamespaceDelete — DELETE /hub/k8s/own/{cluster}/{ns}: namespace
// со всем содержимым и учёт.
func (s *Server) handleOwnNamespaceDelete(w http.ResponseWriter, r *http.Request) {
	t, cl, hostID, ns, ok := s.ownNS(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !s.isOwnNS(ctx, t, cl.ID, ns) {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("hub.ownNSNotOwn", ns))
		return
	}
	user := auth.Username(ctx)
	if _, err := s.hostCall(ctx, user, hostID, "POST", "/api/k8s/objects/action", map[string]string{"kind": "namespaces", "name": ns, "action": "delete"}, nil); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	ownNSMu.Lock()
	list := slices.DeleteFunc(s.ownNamespaces(ctx), func(o OwnNamespace) bool { return o.TokenID == t.ID && o.ClusterID == cl.ID && o.Namespace == ns })
	_ = s.saveOwnNamespaces(ctx, list)
	ownNSMu.Unlock()
	_ = s.db.DeleteTokenUsage(context.WithoutCancel(ctx), t.ID, store.UsageK8sNS, cl.ID, ns)
	s.db.Audit(ctx, user, "k8s.own_namespace_delete", ns+"@"+cl.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOwnNamespaceLogs — GET /hub/k8s/own/{cluster}/{ns}/logs?pod=&container=&tail=.
func (s *Server) handleOwnNamespaceLogs(w http.ResponseWriter, r *http.Request) {
	_, _, hostID, ns, ok := s.ownNS(w, r)
	if !ok {
		return
	}
	q := url.Values{"namespace": {ns}, "pod": {r.URL.Query().Get("pod")}, "container": {r.URL.Query().Get("container")}, "tail": {r.URL.Query().Get("tail")}}
	var out struct {
		Log string `json:"log"`
	}
	if _, err := s.hostCall(r.Context(), auth.Username(r.Context()), hostID, "GET", "/api/k8s/pods/log?"+q.Encode(), nil, &out); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleOwnNamespaceHelm — POST /hub/k8s/own/{cluster}/{ns}/helm
// {"repo_url","chart","version","release","values","dry_run"}: helm template
// на хосте, затем — проверки манифеста токена (свой namespace, без
// кластерных видов и доступа к узлу) и применение; dry_run — только
// рендер и проверка.
func (s *Server) handleOwnNamespaceHelm(w http.ResponseWriter, r *http.Request) {
	t, cl, hostID, ns, ok := s.ownNS(w, r)
	if !ok {
		return
	}
	var req struct {
		RepoURL string `json:"repo_url"`
		Chart   string `json:"chart"`
		Version string `json:"version"`
		Release string `json:"release"`
		Values  string `json:"values"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	var rendered struct {
		Manifest string `json:"manifest"`
	}
	if _, err := s.hostCall(ctx, user, hostID, "POST", "/api/k8s/helm/template", map[string]string{
		"repo_url": req.RepoURL, "chart": req.Chart, "version": req.Version, "release": req.Release, "namespace": ns, "values": req.Values,
	}, &rendered); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	content, err := withNamespace(rendered.Manifest, ns)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if _, err := tokenK8sNamespaces(content, []string{ns}); err != nil {
		writeErr(w, r, http.StatusForbidden, err)
		return
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]string{"manifest": content})
		return
	}
	if err := s.tokenRate(t, "k8s.apply"); err != nil {
		writeErr(w, r, http.StatusTooManyRequests, err)
		return
	}
	var out struct {
		Output string `json:"output"`
	}
	_, err = s.hostCall(ctx, user, hostID, "POST", "/api/k8s/apply", map[string]string{"content": content, "note": "helm " + req.Release + " (nkt token " + t.Name + ")"}, &out)
	s.db.Audit(ctx, user, "k8s.own_helm", req.Release+"@"+ns+"@"+cl.Name, auditOutcome(err), map[string]any{"chart": req.Chart, "version": req.Version})
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out.Output})
}

// withNamespace — документы без namespace (helm template оставляет их
// так) получают namespace клиента; пустые документы выбрасываются.
func withNamespace(content, ns string) (string, error) {
	dec := yaml.NewDecoder(strings.NewReader(content))
	var b strings.Builder
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", msgs.Errorf("k8s.manifestYAML", err.Error())
		}
		if len(doc) == 0 {
			continue
		}
		kind, _ := doc["kind"].(string)
		if meta, ok := doc["metadata"].(map[string]any); ok && kind != "Namespace" && !tokenK8sClusterWide[kind] {
			if v, _ := meta["namespace"].(string); v == "" {
				meta["namespace"] = ns
			}
		}
		raw, err := yaml.Marshal(doc)
		if err != nil {
			return "", err
		}
		b.WriteString("---\n")
		b.Write(raw)
	}
	return b.String(), nil
}
