package vmnet

import (
	"strings"
	"testing"
)

func TestXMLForEachMode(t *testing.T) {
	nat, err := XML(Spec{Name: "vmnet", Mode: ModeNAT, Bridge: "virbr10", Subnet: "192.168.100.0/24", DHCP: true})
	if err != nil {
		t.Fatalf("NAT: %v", err)
	}
	for _, want := range []string{
		"<name>vmnet</name>",
		"<forward mode='nat'/>",
		"bridge name='virbr10'",
		// Хосту достаётся первый адрес подсети, раздача начинается со
		// второго и кончается перед широковещательным.
		"address='192.168.100.1' netmask='255.255.255.0'",
		"range start='192.168.100.2' end='192.168.100.254'",
	} {
		if !strings.Contains(nat, want) {
			t.Errorf("в описании NAT нет %q:\n%s", want, nat)
		}
	}

	// Мост — на существующий интерфейс хоста: своей подсети и DHCP у
	// такой сети нет, адреса раздаёт сеть, в которую мост включён.
	bridged, err := XML(Spec{Name: "lan", Mode: ModeBridge, Bridge: "br0"})
	if err != nil {
		t.Fatalf("мост: %v", err)
	}
	if !strings.Contains(bridged, "<forward mode='bridge'/>") || strings.Contains(bridged, "<dhcp>") {
		t.Errorf("описание моста неверно:\n%s", bridged)
	}

	// Изолированная — без forward: наружу из неё хода нет.
	isolated, err := XML(Spec{Name: "lab", Mode: ModeIsolated, Subnet: "10.10.10.0/24", DHCP: true})
	if err != nil {
		t.Fatalf("изолированная: %v", err)
	}
	if strings.Contains(isolated, "<forward") {
		t.Errorf("у изолированной сети есть выход наружу:\n%s", isolated)
	}
	if !strings.Contains(isolated, "address='10.10.10.1'") {
		t.Errorf("изолированная без адреса хоста:\n%s", isolated)
	}
}

// Подсеть меньше /24 тоже должна считаться правильно: раздача кончается
// перед широковещательным адресом, а не на «.254» вслепую.
func TestDHCPRangeRespectsMask(t *testing.T) {
	doc, err := XML(Spec{Name: "small", Mode: ModeNAT, Subnet: "192.168.50.0/29", DHCP: true})
	if err != nil {
		t.Fatalf("XML: %v", err)
	}
	if !strings.Contains(doc, "range start='192.168.50.2' end='192.168.50.6'") {
		t.Errorf("границы раздачи неверны:\n%s", doc)
	}
}

func TestSpecValidate(t *testing.T) {
	bad := map[string]Spec{
		"имя с пробелом":    {Name: "моя сеть", Mode: ModeNAT, Subnet: "192.168.1.0/24"},
		"имя с кавычкой":    {Name: "net'/><x", Mode: ModeNAT, Subnet: "192.168.1.0/24"},
		"чужой вид":         {Name: "net", Mode: "vepa", Subnet: "192.168.1.0/24"},
		"мост без имени":    {Name: "net", Mode: ModeBridge},
		"подсеть не CIDR":   {Name: "net", Mode: ModeNAT, Subnet: "192.168.1.1"},
		"подсеть IPv6":      {Name: "net", Mode: ModeNAT, Subnet: "fd00::/64"},
		"слишком узкая /31": {Name: "net", Mode: ModeNAT, Subnet: "192.168.1.0/31"},
	}
	for name, spec := range bad {
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: принято без ошибки", name)
		}
	}
	if err := (Spec{Name: "ok-net", Mode: ModeNAT, Subnet: "192.168.77.0/24", DHCP: true}).Validate(); err != nil {
		t.Errorf("верное описание отклонено: %v", err)
	}
}

func TestFillFromXML(t *testing.T) {
	doc := `<network>
  <name>default</name>
  <forward mode='nat'/>
  <bridge name='virbr0' stp='on' delay='0'/>
  <ip address='192.168.122.1' netmask='255.255.255.0'>
    <dhcp><range start='192.168.122.2' end='192.168.122.254'/></dhcp>
  </ip>
</network>`
	var n Network
	fillFromXML(&n, doc)
	if n.Mode != "nat" || n.Bridge != "virbr0" || n.Address != "192.168.122.1" || !n.DHCP {
		t.Errorf("разобрано = %+v", n)
	}
}

// Подсеть подбирается свободная: занятая чужой сетью просто не
// поднимется, а совпадение с домашней сетью оператора сломает ему
// маршрутизацию.
func TestFreeSubnetAvoidsTaken(t *testing.T) {
	existing := []Network{
		{Name: "default", Address: "192.168.122.1", Bridge: "virbr0"},
		{Name: "lab", Address: "192.168.123.1", Bridge: "virbr1"},
	}
	subnet, bridge, err := freeSubnet(existing)
	if err != nil {
		t.Fatalf("freeSubnet: %v", err)
	}
	if subnet != "192.168.124.0/24" {
		t.Errorf("подсеть = %q, занятые пропущены неверно", subnet)
	}
	if bridge != "virbr2" {
		t.Errorf("мост = %q, занятые пропущены неверно", bridge)
	}

	// На пустом хосте берётся первая же — та, что привычна по libvirt.
	subnet, bridge, err = freeSubnet(nil)
	if err != nil || subnet != "192.168.122.0/24" || bridge != "virbr0" {
		t.Errorf("на пустом хосте = %q, %q, %v", subnet, bridge, err)
	}
}
