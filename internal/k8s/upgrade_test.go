package k8s

import (
	"context"
	"strings"
	"testing"
)

func TestUpgradeInfoAndValidate(t *testing.T) {
	info, err := fixtureManager().Upgrade(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Flavor != FlavorK3s || info.Minor != "1.31" || strings.Join(info.Channels, ",") != "1.31,1.32,1.33" {
		t.Errorf("%+v", info)
	}
	if err := (UpgradeSpec{Version: "1.32"}).Validate(FlavorK3s, "v1.31.4+k3s1"); err != nil {
		t.Error(err)
	}
	if err := (UpgradeSpec{Version: "1.30"}).Validate(FlavorK3s, "v1.31.4+k3s1"); err == nil {
		t.Error("понижение принято")
	}
	if err := (UpgradeSpec{Version: "1.33"}).Validate(FlavorKubeadm, "v1.31.2"); err == nil {
		t.Error("kubeadm через версию принят")
	}
	if err := (UpgradeSpec{Version: "1.32; rm -rf /"}).Validate(FlavorK3s, "v1.31.4"); err == nil {
		t.Error("мусор в версии принят")
	}
}

func TestUpgradeScript(t *testing.T) {
	k3s := UpgradeScript(FlavorK3s, RoleAgent, UpgradeSpec{Version: "1.32"})
	if !strings.Contains(k3s, `"id":"v1.32"`) || !strings.Contains(k3s, "systemctl restart k3s-agent") || strings.Contains(k3s, "get.k3s.io") {
		t.Errorf("k3s:\n%s", k3s)
	}
	first := UpgradeScript(FlavorKubeadm, RoleServer, UpgradeSpec{Version: "1.35", First: true})
	other := UpgradeScript(FlavorKubeadm, RoleAgent, UpgradeSpec{Version: "1.35"})
	if !strings.Contains(first, "kubeadm upgrade apply") || !strings.Contains(other, "kubeadm upgrade node") || !strings.Contains(first, "v1.35/deb") {
		t.Errorf("kubeadm:\n%s\n%s", first, other)
	}
}
