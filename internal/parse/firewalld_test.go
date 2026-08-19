package parse

import (
	"context"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// TestFirewallParsesFirewalldFixture proves firewalld and ufw coexist in one
// scan without either clobbering the other's Backends/Rules/Policies — the
// fixtures host stubs both. Covers: default-zone-derived Policy, a zone's
// services/ports/rich-rule all becoming distinct FirewallRule entries, and
// the runtime-vs-permanent split (public has 8443/tcp in both views, but
// 9090/tcp only in permanent — simulating a change staged but not yet
// reloaded).
func TestFirewallParsesFirewalldFixture(t *testing.T) {
	res := Firewall(context.Background(), fixtureCollector(t))
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}

	fd := res.State.Manager("firewalld")
	if !fd.Installed {
		t.Fatal("firewalld должен быть определён как установленный (в фикстурах есть command -v firewall-cmd)")
	}
	if !fd.Active {
		t.Error("firewalld должен быть активен (firewall-cmd --state -> running)")
	}
	if fd.Policy != "public" {
		t.Errorf("firewalld.Policy (зона по умолчанию) = %q, want %q", fd.Policy, "public")
	}

	byKind := func(zone, kind string, permanent bool) []model.FirewallRule {
		var out []model.FirewallRule
		for _, r := range res.State.Rules {
			if r.Backend != "firewalld" || r.Zone != zone || r.Permanent != permanent {
				continue
			}
			switch kind {
			case "service":
				if r.PortSpec != "" && len(r.Ports) == 0 && r.Raw == "" {
					out = append(out, r)
				}
			case "port":
				if len(r.Ports) > 0 {
					out = append(out, r)
				}
			case "rich":
				if r.Raw != "" {
					out = append(out, r)
				}
			}
		}
		return out
	}

	runtimeServices := byKind("public", "service", false)
	if len(runtimeServices) != 2 {
		t.Fatalf("public/runtime services = %+v, want 2 (dhcpv6-client, ssh)", runtimeServices)
	}
	names := map[string]bool{}
	for _, r := range runtimeServices {
		names[r.PortSpec] = true
		if r.Action != "ACCEPT" {
			t.Errorf("service rule %q: action = %q, want ACCEPT", r.PortSpec, r.Action)
		}
	}
	if !names["ssh"] || !names["dhcpv6-client"] {
		t.Errorf("public/runtime services = %v, want ssh and dhcpv6-client", names)
	}

	runtimePorts := byKind("public", "port", false)
	if len(runtimePorts) != 1 || runtimePorts[0].Ports[0] != 8443 || runtimePorts[0].Protocol != "tcp" {
		t.Errorf("public/runtime ports = %+v, want exactly [8443/tcp]", runtimePorts)
	}

	permanentPorts := byKind("public", "port", true)
	if len(permanentPorts) != 2 {
		t.Fatalf("public/permanent ports = %+v, want 2 (8443/tcp and 9090/tcp — the staged, not-yet-reloaded one)",
			permanentPorts)
	}
	var sawStaged bool
	for _, r := range permanentPorts {
		if r.Ports[0] == 9090 {
			sawStaged = true
		}
	}
	if !sawStaged {
		t.Errorf("permanent ports = %+v, want 9090/tcp present (runtime does not have it — that's the point)",
			permanentPorts)
	}

	richRuntime := byKind("public", "rich", false)
	if len(richRuntime) != 1 {
		t.Fatalf("public/runtime rich rules = %+v, want 1", richRuntime)
	}
	if richRuntime[0].Action != "ACCEPT" {
		t.Errorf("rich rule action = %q, want ACCEPT (raw: %s)", richRuntime[0].Action, richRuntime[0].Raw)
	}

	// The zone's own target becomes a FirewallPolicy row, same table the
	// "Политики цепочек" card already renders for iptables chains.
	var publicPolicy *model.FirewallPolicy
	for i := range res.State.Policies {
		if res.State.Policies[i].Backend == "firewalld" && res.State.Policies[i].Chain == "public" {
			publicPolicy = &res.State.Policies[i]
		}
	}
	if publicPolicy == nil || publicPolicy.Policy != "default" {
		t.Errorf("public zone policy = %+v, want Policy=default", publicPolicy)
	}
}

// TestFirewalldNotInstalled locks in that a host without firewall-cmd gets
// an explicit Manager("firewalld").Installed=false, the same distinction
// TestFirewallUFWNotInstalled locks in for ufw.
func TestFirewalldNotInstalled(t *testing.T) {
	res := Firewall(context.Background(), collect.NewFixtures(t.TempDir()))
	fd := res.State.Manager("firewalld")
	if fd.Installed {
		t.Error("firewalld.Installed = true на пустом хосте без firewall-cmd")
	}
	if fd.Active {
		t.Error("firewalld.Active = true без установленного firewall-cmd — так быть не может")
	}
}
