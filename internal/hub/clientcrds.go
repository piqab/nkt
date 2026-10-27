package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
)

// Ресурсы операторов для клиентов (1.17.15): администратор разрешает
// клиентам namespace (роль edit) ресурсы CRD — например, Registry
// rgstroperator. Хаб ставит ClusterRole с метками aggregate-to-edit и
// aggregate-to-admin: Kubernetes сам добавляет эти права в роли edit и
// admin. Политики допуска namespace клиента действуют и на то, что
// оператор создаёт по ресурсу клиента (Service, Ingress, поды).

const clientCRDKV = "k8s.clientcrds"

// clientCRDRe — «ресурс.группа» как в kubectl: registries.registry.rgstr.io.
var clientCRDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}\.([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// clientCRDDenyGroups — группы, которые клиентам не отдаются: права,
// допуск, расширения API, сеть и политики кластера.
var clientCRDDenyGroups = []string{
	"rbac.authorization.k8s.io", "admissionregistration.k8s.io", "apiextensions.k8s.io", "apiregistration.k8s.io",
	"networking.k8s.io", "policy", "cilium.io", "certificates.k8s.io", "authentication.k8s.io", "authorization.k8s.io",
	"storage.k8s.io", "node.k8s.io", "scheduling.k8s.io", "gateway.networking.k8s.io", "coordination.k8s.io",
}

func (s *Server) clientCRDs(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	if raw, ok, _ := s.db.KVGet(ctx, clientCRDKV); ok {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// clientCRDManifest — ClusterRole кластера с правами на ресурсы (пустой
// список — без правил: права снимаются).
func clientCRDManifest(list []string) string {
	var b strings.Builder
	b.WriteString(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: nkt-client-crds
  labels:
    rbac.authorization.k8s.io/aggregate-to-edit: "true"
    rbac.authorization.k8s.io/aggregate-to-admin: "true"
rules:`)
	if len(list) == 0 {
		b.WriteString(" []\n")
		return b.String()
	}
	b.WriteString("\n")
	for _, r := range list {
		res, group, _ := strings.Cut(r, ".")
		fmt.Fprintf(&b, "  - apiGroups: [%q]\n    resources: [%q]\n    verbs: [get, list, watch, create, update, patch, delete]\n", group, res)
	}
	return b.String()
}

// handleClientCRDs — GET/PUT /hub/clusters/{id}/client-crds
// {"resources":["registries.registry.rgstr.io"]}.
func (s *Server) handleClientCRDs(w http.ResponseWriter, r *http.Request) {
	id, err := clusterIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	cl, err := s.db.ClusterByID(ctx, id)
	if err != nil {
		fail(w, r, err)
		return
	}
	all := s.clientCRDs(ctx)
	if r.Method == http.MethodGet {
		list := all[cl.Name]
		if list == nil {
			list = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"resources": list})
		return
	}
	var req struct {
		Resources []string `json:"resources"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	list := []string{}
	for _, x := range req.Resources {
		x = strings.ToLower(strings.TrimSpace(x))
		if x == "" || slices.Contains(list, x) {
			continue
		}
		_, group, _ := strings.Cut(x, ".")
		if !clientCRDRe.MatchString(x) || slices.Contains(clientCRDDenyGroups, group) || strings.HasSuffix(group, ".k8s.io") {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.clientCRDBad", x))
			return
		}
		list = append(list, x)
	}
	if len(list) > 50 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.clientCRDBad", "…"))
		return
	}
	sort.Strings(list)
	hosts, err := s.db.ClusterHosts(ctx, cl.ID)
	if err != nil || len(hosts) == 0 {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.clusterNoNodes"))
		return
	}
	user := auth.Username(ctx)
	if _, err := s.hostCall(ctx, user, hosts[0].ID, "POST", "/api/k8s/apply", map[string]string{"content": clientCRDManifest(list), "note": "nkt client resources"}, nil); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	before := all[cl.Name]
	all[cl.Name] = list
	raw, _ := json.Marshal(all)
	if err := s.db.KVSet(ctx, clientCRDKV, string(raw)); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, user, "cluster.client_crds", cl.Name, "ok", map[string]any{"before": before, "after": list})
	writeJSON(w, http.StatusOK, map[string]any{"resources": list})
}
