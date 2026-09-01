package parse

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// TestFirewallNeverReturnsNilSlices guards against a real production crash:
// a host where iptables-save/ip6tables-save fail and ufw is inactive (e.g.
// no root, or neither installed) must still get real empty slices back, not
// nil — encoding/json marshals a nil slice as `null` (none of these fields
// have `omitempty`), and the frontend crashes calling .filter/.map on
// `null`. An empty fixtures root (no .commands/index.json at all) makes
// every command fail, exercising exactly that path.
func TestFirewallNeverReturnsNilSlices(t *testing.T) {
	res := Firewall(context.Background(), collect.NewFixtures(t.TempDir()))
	if res.State.Backends == nil {
		t.Error("Backends = nil, ожидался непустой (даже если пустой) срез")
	}
	if res.State.Policies == nil {
		t.Error("Policies = nil, ожидался непустой (даже если пустой) срез")
	}
	if res.State.Rules == nil {
		t.Error("Rules = nil, ожидался непустой (даже если пустой) срез")
	}
}

// TestFirewallUFWNotInstalled locks in that a host without the ufw binary
// gets an explicit Manager("ufw").Installed=false rather than looking
// identical to one where ufw is merely inactive (Active=false with
// Installed=true would suggest "just turn it on"; Installed=false is the
// "install it first" case the Firewall page needs to tell apart).
func TestFirewallUFWNotInstalled(t *testing.T) {
	res := Firewall(context.Background(), collect.NewFixtures(t.TempDir()))
	ufw := res.State.Manager("ufw")
	if ufw.Installed {
		t.Error("ufw.Installed = true на пустом хосте без ufw")
	}
	if ufw.Active {
		t.Error("ufw.Active = true без установленного ufw — так быть не может")
	}
}

func TestFirewallParsesFixture(t *testing.T) {
	res := Firewall(context.Background(), fixtureCollector(t))
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}

	policy := func(backend, table, chain string) *model.FirewallPolicy {
		for i := range res.State.Policies {
			p := res.State.Policies[i]
			if p.Backend == backend && p.Table == table && p.Chain == chain {
				return &res.State.Policies[i]
			}
		}
		return nil
	}
	in := policy("iptables", "filter", "INPUT")
	if in == nil {
		t.Fatal("политика filter/INPUT не найдена")
	}
	if in.Policy != "DROP" {
		t.Errorf("INPUT policy = %q, ожидалось DROP", in.Policy)
	}
	if in.Packets != 1024 {
		t.Errorf("INPUT счётчик пакетов = %d, ожидалось 1024", in.Packets)
	}

	var https, dnat, stale *model.FirewallRule
	for i := range res.State.Rules {
		r := &res.State.Rules[i]
		switch {
		case r.Backend == "iptables" && r.Chain == "INPUT" && len(r.Ports) == 1 && r.Ports[0] == 443:
			https = r
		case r.Backend == "iptables" && r.Action == "DNAT" && len(r.Ports) == 1 && r.Ports[0] == 6379:
			dnat = r
		case r.Backend == "iptables" && r.Chain == "INPUT" && len(r.Ports) == 1 && r.Ports[0] == 25:
			stale = r
		}
	}
	if https == nil {
		t.Fatal("правило INPUT для 443 не найдено")
	}
	if https.Action != "ACCEPT" || https.Protocol != "tcp" {
		t.Errorf("правило 443: action=%q proto=%q", https.Action, https.Protocol)
	}
	if https.Bytes == 0 {
		t.Error("правило 443: счётчик байт должен быть прочитан из iptables-save -c")
	}
	if dnat == nil {
		t.Fatal("DNAT-правило docker для 6379 не найдено")
	}
	if dnat.ManagedBy != "docker" {
		t.Errorf("DNAT 6379: managed_by=%q, ожидалось docker", dnat.ManagedBy)
	}
	if dnat.DNATTo != "172.19.0.4:6379" {
		t.Errorf("DNAT 6379: to=%q", dnat.DNATTo)
	}
	if stale == nil || stale.Packets != 0 {
		t.Error("правило для 25/tcp с нулевым счётчиком должно быть распознано")
	}

	ufw := res.State.Manager("ufw")
	if !ufw.Installed {
		t.Error("ufw должен быть определён как установленный (в фикстурах есть command -v ufw)")
	}
	if !ufw.Active {
		t.Error("ufw должен быть активен")
	}
	if ufw.Policy == "" {
		t.Error("политика ufw по умолчанию не прочитана")
	}
	ufwPorts := map[int]string{}
	for _, r := range res.State.Rules {
		if r.Backend != "ufw" {
			continue
		}
		for _, p := range r.Ports {
			ufwPorts[p] = r.Source
		}
	}
	if ufwPorts[8443] != "10.10.0.0/24" {
		t.Errorf("ufw 8443: source=%q, ожидалось 10.10.0.0/24 (все: %v)", ufwPorts[8443], ufwPorts)
	}
	if _, ok := ufwPorts[22]; !ok {
		t.Error("ufw: правило для 22/tcp не найдено")
	}
}

func TestListenersParseFixture(t *testing.T) {
	listeners, status := Listeners(context.Background(), fixtureCollector(t))
	if status.Error != "" {
		t.Fatalf("ss вернул ошибку: %s", status.Error)
	}
	byPort := map[int]model.Listener{}
	for _, l := range listeners {
		byPort[l.Port] = l
	}

	if got := byPort[443]; got.Process != "nginx" || !got.Public() {
		t.Errorf("443: process=%q addr=%q", got.Process, got.Address)
	}
	if got := byPort[6379]; got.Process != "docker-proxy" || !got.Public() {
		t.Errorf("6379 должен быть опубликован docker-proxy на всех интерфейсах: %+v", got)
	}
	if got := byPort[8080]; got.Address != "127.0.0.1" {
		t.Errorf("8080: адрес=%q, ожидалось 127.0.0.1", got.Address)
	}
	if got := byPort[5432]; got.Process != "haproxy" || got.Address != "10.10.0.2" {
		t.Errorf("5432: %+v", got)
	}
	if _, ok := byPort[11211]; !ok {
		t.Error("memcached на 11211 должен попасть в список слушателей")
	}
}
