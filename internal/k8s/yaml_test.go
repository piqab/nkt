package k8s

import (
	"context"
	"strings"
	"testing"
)

func TestDocPath(t *testing.T) {
	for _, c := range [][3]string{{"deployments", "shop", "api"}, {"nodes", "", "lab-w-1"}, {"cr:certificates.cert-manager.io", "shop", "web"}} {
		k, ns, n, ok := ParseDocPath(DocPath(c[0], c[1], c[2]))
		if !ok || k != c[0] || ns != c[1] || n != c[2] {
			t.Errorf("%v → %q %q %q %v", c, k, ns, n, ok)
		}
	}
	for _, bad := range []string{"k8s://a/b/c/d", "lxd://x", "k8s://shop/secrets/db", "k8s:///x"} {
		if _, _, _, ok := ParseDocPath(bad); ok {
			t.Errorf("%q принят", bad)
		}
	}
}

func TestCleanYAML(t *testing.T) {
	out, err := CleanYAML(map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "x", "uid": "u", "resourceVersion": "1", "managedFields": []any{1},
			"annotations": map[string]any{"kubectl.kubernetes.io/last-applied-configuration": "{}"}},
		"data":   map[string]any{"a": "b"},
		"status": map[string]any{"x": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"uid", "resourceVersion", "managedFields", "last-applied", "status", "annotations"} {
		if strings.Contains(out, bad) {
			t.Errorf("осталось %s:\n%s", bad, out)
		}
	}
	if !strings.HasPrefix(out, "apiVersion: v1\n") {
		t.Errorf("порядок ключей:\n%s", out)
	}
}

func TestParseManifest(t *testing.T) {
	docs, err := ParseManifest("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: b\n  namespace: shop\n")
	if err != nil || len(docs) != 2 || docs[1].Namespace != "shop" {
		t.Fatalf("%v %v", docs, err)
	}
	for _, bad := range []string{"", "kind: ConfigMap\nmetadata:\n  name: a\n", "a: [", "---\n"} {
		if _, err := ParseManifest(bad); err == nil {
			t.Errorf("%q принят", bad)
		}
	}
}

func TestYAMLFromFixtures(t *testing.T) {
	m := fixtureManager()
	ctx := context.Background()
	doc, err := m.YAML(ctx, "deployments", "shop", "api")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Content, "kind: Deployment") || doc.HistoryPath != "k8s://shop/deployments/api" || doc.SHA256 == "" {
		t.Errorf("%+v", doc)
	}
	if _, err := m.YAML(ctx, "secrets", "shop", "db-credentials"); err == nil {
		t.Error("YAML секрета отдан")
	}
	tg, _ := m.Find(ctx, "deployments", "shop", "api")
	if err := m.CheckIdentity(ctx, tg, doc.Content); err != nil {
		t.Errorf("свой манифест: %v", err)
	}
	other := strings.Replace(doc.Content, "name: api\n", "name: other\n", 1)
	if err := m.CheckIdentity(ctx, tg, other); err == nil {
		t.Error("чужое имя принято")
	}
	d, err := m.Diff(ctx, doc.Content, "shop")
	if err != nil || !strings.Contains(d, "replicas") {
		t.Errorf("diff: %q %v", d, err)
	}
	if _, err := m.Apply(ctx, doc.Content, "shop"); err != nil {
		t.Errorf("apply: %v", err)
	}
}
