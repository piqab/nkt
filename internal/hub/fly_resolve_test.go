package hub

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/store"
)

// Адрес цели для машины хаб берёт не только из снимка хоста: узел кластера и
// любая машина, заведённая хабом, стоит у него в списке хостов со своим
// адресом, и адрес этот живой — по нему ходит SSH. Снимок же хоста видит
// адрес только через аренду DHCP или ARP, а обе вещи протухают.
func TestFlyMachineAddrFromHubRecord(t *testing.T) {
	s, _, hosts := listenerFixture(t, "192.0.2.31", "192.0.2.32")
	ctx := context.Background()
	add := func(name, addr string, parent int64) {
		t.Helper()
		id, err := s.db.CreateHost(ctx, name, addr, 22, "nkt", store.HostAuthKey, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.db.SetHostParent(ctx, id, parent); err != nil {
			t.Fatal(err)
		}
	}
	add("lab-w-1", "192.168.122.57", hosts[0])
	add("lab-w-2", "lab-w-2.lan", hosts[0])
	add("moved", "192.168.122.58", hosts[1])

	tests := []struct {
		desc string
		t    FlyTarget
		want string
	}{
		{"запись хаба даёт адрес", FlyTarget{Kind: "vm", Ref: "lab-w-1", HostID: hosts[0]}, "192.168.122.57"},
		{"имя вместо адреса — не адрес цели", FlyTarget{Kind: "vm", Ref: "lab-w-2", HostID: hosts[0]}, ""},
		{"машина уехала на другой хост — старому она не интересна", FlyTarget{Kind: "vm", Ref: "moved", HostID: hosts[0]}, ""},
		{"обычный хост машиной не считается", FlyTarget{Kind: "vm", Ref: "hv1", HostID: hosts[0]}, ""},
	}
	for _, tc := range tests {
		got, ok := s.flyMachineAddr(ctx, tc.t)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("%s: flyMachineAddr = %q, %v; ждали %q", tc.desc, got, ok, tc.want)
		}
	}
}

// Отказ обязан различать «машины нет в списке» и «машина есть, адреса нет»:
// у первой причины список хостов и имя, у второй — состояние машины, чем
// хост искал адрес и когда снимал список.
func TestFlyVMListFind(t *testing.T) {
	list := flyVMList{
		VMs: []model.VirtualMachine{
			{Name: "with-ip", State: "running", Networks: []model.VMNetIface{{IP: "192.168.122.46"}}},
			{Name: "no-ip", State: "shut off"},
			{Name: "only-v6", State: "running", Networks: []model.VMNetIface{{IP: "fe80::1"}}},
		},
		TS:          "2026-11-07T10:00:00Z",
		AddrSources: []string{"lease", "arp"},
	}
	if ip, state, listed := list.find("with-ip"); ip != "192.168.122.46" || state != "running" || !listed {
		t.Errorf("with-ip: %q, %q, %v", ip, state, listed)
	}
	if ip, state, listed := list.find("no-ip"); ip != "" || state != "shut off" || !listed {
		t.Errorf("no-ip: %q, %q, %v — машину нашли, адреса нет: ждали состояние", ip, state, listed)
	}
	if ip, _, listed := list.find("only-v6"); ip != "" || !listed {
		t.Errorf("only-v6: %q, %v — IPv6 адресом цели не является", ip, listed)
	}
	if ip, state, listed := list.find("absent"); ip != "" || state != "" || listed {
		t.Errorf("absent: %q, %q, %v — машины нет в списке", ip, state, listed)
	}
}
