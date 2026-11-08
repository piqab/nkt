package vmcreate

import (
	"context"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// fakeIP отвечает на «ip -d -o link show dev …» ровно так, как отвечает
// iproute2: строкой устройства, когда оно есть, и ненулевым кодом, когда
// его нет.
func fakeIP(line string, exit int) Runner {
	return func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		res := collect.CommandResult{Argv: argv, ExitCode: exit}
		if exit == 0 {
			res.Stdout = line
		} else {
			res.Stderr = `Device "eth9" does not exist.`
		}
		return res, nil
	}
}

// Устройство для macvtap должно быть живым: опущенный интерфейс,
// loopback или чужая машина дают отказ до создания диска, а не
// непонятную ошибку virsh на пятом шаге.
func TestCheckDirectDevice(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		exit    int
		wantErr bool
	}{
		{
			name: "поднятый физический интерфейс",
			line: `2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UP mode DEFAULT group default qlen 1000\    link/ether 00:15:5d:45:3d:0a brd ff:ff:ff:ff:ff:ff`,
		},
		{
			name:    "кабель вытащен",
			line:    `2: eth0: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 qdisc mq state DOWN mode DEFAULT group default qlen 1000\    link/ether 00:15:5d:45:3d:0a brd ff:ff:ff:ff:ff:ff`,
			wantErr: true,
		},
		{
			name:    "выключено административно",
			line:    `2: eth0: <BROADCAST,MULTICAST> mtu 1500 qdisc noop state DOWN mode DEFAULT group default qlen 1000\    link/ether 00:15:5d:45:3d:0a brd ff:ff:ff:ff:ff:ff`,
			wantErr: true,
		},
		{
			name:    "loopback",
			line:    `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN mode DEFAULT group default qlen 1000\    link/loopback 00:00:00:00:00:00 brd ff:ff:ff:ff:ff:ff`,
			wantErr: true,
		},
		{
			name:    "уже macvtap",
			line:    `7: macvtap0@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noop state UP mode DEFAULT group default qlen 1000\    link/ether 66:77:88:99:aa:bb brd ff:ff:ff:ff:ff:ff\    macvtap mode bridge passthru`,
			wantErr: true,
		},
		{
			name:    "устройства нет",
			exit:    1,
			wantErr: true,
		},
	}
	for _, c := range cases {
		err := CheckDirectDevice(context.Background(), fakeIP(c.line, c.exit), "eth0")
		if c.wantErr && err == nil {
			t.Errorf("%s: принято без ошибки", c.name)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: отклонено: %v", c.name, err)
		}
		// Отказ обязан называть устройство: список интерфейсов хоста их
		// содержит несколько, и «устройство не подходит» без имени
		// ничего не объясняет.
		if err != nil && !strings.Contains(err.Error(), "eth0") {
			t.Errorf("%s: в отказе нет имени устройства: %v", c.name, err)
		}
	}
}

// Имя устройства попадает в команду и в XML домена — форма проверяется та
// же, что у моста и сети.
func TestSpecValidateDirect(t *testing.T) {
	s := validSpec()
	s.Direct = "eth0"
	if err := s.Validate(); err != nil {
		t.Fatalf("верное описание с macvtap отклонено: %v", err)
	}
	for name, mutate := range map[string]func(*Spec){
		"имя с кавычкой": func(x *Spec) { x.Direct = "eth0'/><script>" },
		"имя с пробелом": func(x *Spec) { x.Direct = "eth 0" },
		"мост и macvtap": func(x *Spec) { x.Direct, x.Bridge = "eth0", "br0" },
		"сеть и macvtap": func(x *Spec) { x.Direct, x.Network = "eth0", "prod" },
	} {
		s := validSpec()
		s.Direct = "eth0"
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: принято без ошибки", name)
		}
	}

	// XML: third вариант источника — устройство, а не мост и не сеть.
	s = validSpec()
	s.Direct = "eth0"
	xml := DomainXML(s, "d", "s")
	if !strings.Contains(xml, "<interface type='direct'>") ||
		!strings.Contains(xml, "<source dev='eth0' mode='bridge'/>") {
		t.Errorf("прямого подключения нет в XML:\n%s", xml)
	}
	if strings.Contains(xml, "<source network=") || strings.Contains(xml, "<source bridge=") {
		t.Errorf("macvtap уживается с мостом или сетью libvirt:\n%s", xml)
	}
	// Полоса применяется к любому виду подключения — проверка на месте.
	s.NetMbit = 100
	if xml := DomainXML(s, "d", "s"); !strings.Contains(xml, "<inbound average='12500'/>") {
		t.Errorf("поток полосы не подставлен в direct-интерфейс:\n%s", xml)
	}
}
