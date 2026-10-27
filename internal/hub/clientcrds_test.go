package hub

import (
	"strings"
	"testing"
)

// Ресурсы операторов для клиентов: ClusterRole с aggregate-to-edit,
// пустой список — без правил; Registry клиента — те же правила Service
// и Ingress, что у своих.
func TestClientCRDsAndRegistry(t *testing.T) {
	m := clientCRDManifest([]string{"registries.registry.rgstr.io"})
	for _, want := range []string{"name: nkt-client-crds", `rbac.authorization.k8s.io/aggregate-to-edit: "true"`, `apiGroups: ["registry.rgstr.io"]`, `resources: ["registries"]`} {
		if !strings.Contains(m, want) {
			t.Errorf("no %q in:\n%s", want, m)
		}
	}
	if !strings.Contains(clientCRDManifest(nil), "rules: []") {
		t.Error("empty list keeps rules")
	}
	for _, x := range []string{"registries.registry.rgstr.io", "widgets.example.com"} {
		if !clientCRDRe.MatchString(x) {
			t.Errorf("%s rejected", x)
		}
	}
	for _, x := range []string{"registries", "Registries.x.io", "a b.c.io"} {
		if clientCRDRe.MatchString(x) {
			t.Errorf("%s accepted", x)
		}
	}
	ok := "apiVersion: registry.rgstr.io/v1alpha1\nkind: Registry\nmetadata: {name: r, namespace: shop}\nspec: {ingress: {enabled: true, host: reg.c.example.com}}"
	if _, err := tokenK8sNamespaces(ok, []string{"shop"}, "c.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"apiVersion: registry.rgstr.io/v1alpha1\nkind: Registry\nmetadata: {name: r, namespace: shop}\nspec: {serviceType: LoadBalancer}",
		"apiVersion: registry.rgstr.io/v1alpha1\nkind: Registry\nmetadata: {name: r, namespace: shop}\nspec: {ingress: {enabled: true, host: registry.evil.com}}",
	} {
		if _, err := tokenK8sNamespaces(bad, []string{"shop"}, "c.example.com"); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
}
