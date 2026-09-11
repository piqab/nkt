package vmnet

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
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
	subnet, bridge, err := freeSubnet(existing, NetworkRanges(existing))
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
	subnet, bridge, err = freeSubnet(nil, nil)
	if err != nil || subnet != "192.168.122.0/24" || bridge != "virbr0" {
		t.Errorf("на пустом хосте = %q, %q, %v", subnet, bridge, err)
	}
}

// Подсеть, уже занятую сетью libvirt или интерфейсом хоста, принимать
// нельзя: сеть заведётся, но не поднимется, и отказ прилетит потом — при
// создании машины, за несколько экранов от места ошибки.
func TestCheckSubnetRejectsOverlap(t *testing.T) {
	taken := append(
		NetworkRanges([]Network{{Name: "default", Address: "192.168.122.1", Netmask: "255.255.255.0"}}),
		Occupied{CIDR: "10.0.0.0/8", Where: "интерфейсом хоста eth0", Host: true},
	)

	// Та же подсеть.
	var inUse *SubnetInUse
	err := CheckSubnet("192.168.122.0/24", taken)
	if !errors.As(err, &inUse) {
		t.Fatalf("совпадение с сетью libvirt принято: %v", err)
	}
	if inUse.Overridable() {
		t.Errorf("пересечение двух сетей libvirt объявлено снимаемым")
	}

	// Вложенная — тоже пересечение, хотя строки не совпадают.
	if err := CheckSubnet("192.168.122.128/25", taken); err == nil {
		t.Errorf("вложенная подсеть принята")
	}
	// Шире занятой — пересечение с другой стороны.
	if err := CheckSubnet("192.168.0.0/16", taken); err == nil {
		t.Errorf("объемлющая подсеть принята")
	}

	// Сеть хоста — тоже отказ, но его оператор может снять осознанно.
	err = CheckSubnet("10.1.2.0/24", taken)
	if !errors.As(err, &inUse) {
		t.Fatalf("совпадение с сетью хоста принято: %v", err)
	}
	if !inUse.Overridable() {
		t.Errorf("пересечение с интерфейсом хоста объявлено неснимаемым")
	}

	if err := CheckSubnet("192.168.200.0/24", taken); err != nil {
		t.Errorf("свободная подсеть отклонена: %v", err)
	}
}

// Предлагаемая подсеть обходит не только сети libvirt, но и сети самого
// хоста: подставить в форму значение, которое заведомо не поднимется, —
// худший из возможных подсказок.
func TestFreeSubnetAvoidsHostRanges(t *testing.T) {
	existing := []Network{{Name: "default", Address: "192.168.122.1", Bridge: "virbr0"}}
	taken := append(NetworkRanges(existing), Occupied{CIDR: "192.168.123.0/24", Where: "интерфейсом хоста eth0", Host: true})
	subnet, _, err := freeSubnet(existing, taken)
	if err != nil {
		t.Fatalf("freeSubnet: %v", err)
	}
	if subnet != "192.168.124.0/24" {
		t.Errorf("подсеть = %q, сеть хоста не обойдена", subnet)
	}
}

// Причина отказа virsh стоит на второй строке: «Failed to start network
// x» сам по себе не объясняет ничего.
func TestMeaningfulLinesKeepsReason(t *testing.T) {
	out := meaningfulLines("error: Failed to start network iivirt\nerror: internal error: Network is already in use by interface virbr2\n", "")
	for _, want := range []string{"Failed to start network iivirt", "already in use by interface virbr2"} {
		if !strings.Contains(out, want) {
			t.Errorf("в %q нет %q", out, want)
		}
	}
}

// Уже поднятую сеть трогать нельзя: «virsh net-start» на работающей
// отвечает отказом, и он всплывал как невозможность создать машину —
// «сеть libvirt «iivirt»: virsh net-start: Failed to start network» —
// при том что сеть работала.
func TestEnsureNATLeavesActiveNetworkAlone(t *testing.T) {
	var argvs [][]string
	run := func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		argvs = append(argvs, argv)
		switch {
		case len(argv) > 2 && argv[1] == "net-list":
			// И среди всех, и среди активных — сеть уже работает.
			return collect.CommandResult{Stdout: "iivirt\n"}, nil
		case len(argv) > 1 && argv[1] == "net-start":
			return collect.CommandResult{ExitCode: 1, Stderr: "error: Failed to start network iivirt\nerror: internal error: Network is already in use"}, nil
		}
		return collect.CommandResult{}, nil
	}

	created, err := NewManager(run, t.TempDir()).EnsureNAT(context.Background(), "iivirt")
	if err != nil {
		t.Fatalf("EnsureNAT работающей сети: %v", err)
	}
	if created {
		t.Errorf("сеть объявлена созданной, хотя уже была")
	}
	for _, argv := range argvs {
		if len(argv) > 1 && argv[1] == "net-start" {
			t.Errorf("работающую сеть попытались поднять: %v", argv)
		}
	}
}

// Заведённую, но не поднятую — наоборот, поднимаем: именно из-за такой
// машины и не стартуют.
func TestEnsureNATStartsInactiveNetwork(t *testing.T) {
	var started bool
	run := func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		switch {
		case len(argv) > 3 && argv[1] == "net-list" && argv[3] == "--all":
			return collect.CommandResult{Stdout: "iivirt\n"}, nil
		case len(argv) > 2 && argv[1] == "net-list":
			return collect.CommandResult{}, nil // активных нет
		case len(argv) > 1 && argv[1] == "net-start":
			started = true
		}
		return collect.CommandResult{}, nil
	}
	if _, err := NewManager(run, t.TempDir()).EnsureNAT(context.Background(), "iivirt"); err != nil {
		t.Fatalf("EnsureNAT: %v", err)
	}
	if !started {
		t.Errorf("остановленную сеть не подняли")
	}
}
