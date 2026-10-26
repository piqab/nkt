package k8s

import (
	"context"
	"strings"
	"testing"
)

// helm template для клиента — без репозиториев хоста (--repo) и с его
// namespace; имена и адреса проверяются; логи — только допустимые имена.
func TestTenantHelpers(t *testing.T) {
	m := fixtureManager()
	ctx := context.Background()
	argv, err := m.HelmTemplateArgv(ctx, HelmInstallRequest{RepoURL: "https://charts.example.com", Chart: "web", Version: "1.2.3", Release: "site", Namespace: "shop"}, "/tmp/v.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	for _, want := range []string{"template site -n shop web --repo https://charts.example.com", "--version 1.2.3", "-f /tmp/v.yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %s", want, got)
		}
	}
	if strings.Contains(got, "repo add") || strings.Contains(got, "install") {
		t.Errorf("touches host repos or installs: %s", got)
	}
	oci, err := m.HelmTemplateArgv(ctx, HelmInstallRequest{RepoURL: "oci://registry.example.com/charts", Chart: "web", Release: "site", Namespace: "shop"}, "")
	if err != nil || !strings.Contains(strings.Join(oci, " "), "oci://registry.example.com/charts/web") {
		t.Fatalf("oci: %v %v", oci, err)
	}
	for _, bad := range []HelmInstallRequest{
		{RepoURL: "https://u:p@charts.example.com", Chart: "web", Release: "site", Namespace: "shop"},
		{RepoURL: "file:///etc", Chart: "web", Release: "site", Namespace: "shop"},
		{RepoURL: "https://charts.example.com", Chart: "web", Release: "site", Namespace: "Shop"},
	} {
		if _, err := m.HelmTemplateArgv(ctx, bad, ""); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if _, err := m.PodLogs(ctx, "shop", "web; rm -rf /", "", 10); err == nil {
		t.Error("bad pod name accepted")
	}
	if _, err := m.NamespaceExists(ctx, "-x"); err == nil {
		t.Error("bad namespace accepted")
	}
}
