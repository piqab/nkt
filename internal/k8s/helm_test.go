package k8s

import (
	"context"
	"strings"
	"testing"
)

func TestHelmFromFixtures(t *testing.T) {
	m := fixtureManager().WithRunDir(t.TempDir() + "/.run")
	ctx := context.Background()
	st := m.Helm(ctx)
	if !st.Installed || len(st.Releases) != 4 || len(st.Repos) != 2 || st.Error != "" {
		t.Fatalf("%+v", st)
	}
	if _, err := m.FindRelease(ctx, "cert-manager", "cert-manager"); err != nil {
		t.Error(err)
	}
	if _, err := m.FindRelease(ctx, "shop", "cert-manager"); err == nil {
		t.Error("релиз в чужом namespace найден")
	}
	h, err := m.HelmHistory(ctx, "cert-manager", "cert-manager")
	if err != nil || len(h) != 3 {
		t.Errorf("history: %v %v", h, err)
	}
	req := HelmInstallRequest{RepoName: "bitnami", RepoURL: "https://charts.bitnami.com/bitnami", Chart: "redis", Version: "20.1.4", Release: "cache", Namespace: "shop"}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	vf, err := m.WriteValues("shop", "cache", "replica:\n  replicaCount: 1\n")
	if err != nil || vf == "" {
		t.Fatal(err)
	}
	cmds, err := m.HelmInstallCommands(ctx, req, vf)
	if err != nil || len(cmds) != 3 {
		t.Fatalf("%v %v", cmds, err)
	}
	last := strings.Join(cmds[2], " ")
	if !strings.HasPrefix(last, "helm upgrade --install cache bitnami/redis -n shop --create-namespace") || !strings.Contains(last, "-f "+vf) || !strings.Contains(last, "--kubeconfig /etc/rancher/k3s/k3s.yaml") {
		t.Errorf("install: %s", last)
	}
	m.SaveSource(req)
	if src := m.Source(HelmRelease{Name: "cache", Namespace: "shop", Chart: "redis-20.1.4"}); src.RepoName != "bitnami" || src.Chart != "redis" {
		t.Errorf("source: %+v", src)
	}
}

func TestHelmValidate(t *testing.T) {
	ok := HelmInstallRequest{RepoName: "r", RepoURL: "https://x.example/charts", Chart: "c", Release: "rel", Namespace: "ns"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	oci := HelmInstallRequest{RepoURL: "oci://registry-1.docker.io/bitnamicharts", Chart: "redis", Release: "rel", Namespace: "ns"}
	if err := oci.Validate(); err != nil || oci.ChartRef() != "oci://registry-1.docker.io/bitnamicharts/redis" {
		t.Fatalf("oci: %v %s", err, oci.ChartRef())
	}
	bad := []HelmInstallRequest{
		{RepoName: "r", RepoURL: "file:///etc", Chart: "c", Release: "rel", Namespace: "ns"},
		{RepoName: "r", RepoURL: "https://u:p@x.example", Chart: "c", Release: "rel", Namespace: "ns"},
		{RepoName: "r", RepoURL: "https://x.example/${HOME}", Chart: "c", Release: "rel", Namespace: "ns"},
		{RepoName: "r", RepoURL: "https://x.example", Chart: "../c", Release: "rel", Namespace: "ns"},
		{RepoName: "r", RepoURL: "https://x.example", Chart: "c", Release: "Rel", Namespace: "ns"},
		{RepoName: "r", RepoURL: "https://x.example", Chart: "c", Release: "rel", Namespace: "ns", Version: "1 --set x=y"},
	}
	for _, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("принято: %+v", b)
		}
	}
	for in, want := range map[string]string{"traefik-27.0.201+up27.0.2": "traefik", "cert-manager-v1.15.3": "cert-manager", "redis-20.1.4": "redis", "traefik-crd-27.0.201": "traefik-crd"} {
		if got := ChartName(in); got != want {
			t.Errorf("%s → %s", in, got)
		}
	}
}
