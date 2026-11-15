package script

import (
	"strings"
	"testing"
)

func TestParsePlacement(t *testing.T) {
	rows, err := ParsePlacement(`hv1: cp 1, w 2; hv2: w 2, bridge br0; hv3: host w, endpoint 10.0.0.7; hv4: cp 1, nic eth0`)
	if err != nil {
		t.Fatal(err)
	}
	want := []PlacementRow{
		{Host: "hv1", Role: "control-plane", Kind: "vm", Count: 1},
		{Host: "hv1", Role: "worker", Kind: "vm", Count: 2},
		{Host: "hv2", Role: "worker", Kind: "vm", Count: 2, Bridge: "br0"},
		{Host: "hv3", Role: "worker", Kind: "host", Count: 1, Endpoint: "10.0.0.7"},
		// Устройство для macvtap доезжает до каждой строки этого хоста —
		// на каждом хосте оно своё.
		{Host: "hv4", Role: "control-plane", Kind: "vm", Count: 1, Nic: "eth0"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d: %+v, want %+v", i, rows[i], want[i])
		}
	}
	for _, bad := range []string{"", "hv1", "hv1: cp", "hv1: cp x", "hv1: cp 0", "hv1: host", "hv1: host boss", "hv1: master 1", "h v: cp 1", "hv1: bridge br0", "hv1: nic eth0", "hv1: cp 1, nic", "hv1: cp 1, nic eth0/1", "hv1: cp 1, endpoint 10.0.0.7:51820"} {
		if _, err := ParsePlacement(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestParseK8sPlacementStep(t *testing.T) {
	sc, issues := Parse("on hv1 k8s create lab nodes \"hv1: cp 1, w 2; hv2: w 2; hv3: host w\" network wireguard cni cilium expose api 16443", nil)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	st := sc.Steps[0]
	if st.Kind != KindK8sCreate || st.Name != "lab" || !IsPlacement(st.Args["nodes"]) || st.Args["network"] != "wireguard" || st.Args["api"] != "16443" {
		t.Errorf("step: %+v", st)
	}
	// macvtap — режим размещения, устройство — или общим аргументом, или
	// в строке хоста.
	sc, issues = Parse("on hv1 k8s create lab nodes \"hv1: cp 1, nic eth0\" network macvtap nic eth1", nil)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if sc.Steps[0].Args["network"] != "macvtap" || sc.Steps[0].Args["nic"] != "eth1" {
		t.Errorf("macvtap: %+v", sc.Steps[0].Args)
	}
	for text, want := range map[string]string{
		"on hv1 k8s create lab nodes \"hv1: cp 1\" network default": "badK8sNetworkMode",
		"on hv1 k8s create lab nodes \"hv1: boss 1\"":               "badPlacementItem",
		"on hv1 k8s create lab nodes \"hv1: cp 1\" bridge \"a b\"":  "badK8sBridge",
		"on hv1 k8s create lab nodes \"hv1: cp 1\" nic \"a b\"":     "badK8sNic",
	} {
		_, issues := Parse(text, nil)
		if len(issues) == 0 || !strings.Contains(issueKey(issues[0]), want) {
			t.Errorf("%q: %v, ожидалось %s", text, issues, want)
		}
	}
}

// Записи размещения со своими параметрами и роль lb — соответствие
// таблице раздела «Кластеры» (размеры/образ у каждой строки, машина
// встроенного балансировщика).
func TestParsePlacementEntryKeys(t *testing.T) {
	rows, err := ParsePlacement(`hv1: cp 1 cpu 4 mem 8192 disk 60 image debian-13 network labnet, w 2; hv2: lb cpu 1 mem 1024 disk 10 image debian-13; hv3: lb, bridge br0`)
	if err != nil {
		t.Fatal(err)
	}
	want := []PlacementRow{
		{Host: "hv1", Role: "control-plane", Kind: "vm", Count: 1, VCPUs: 4, MemMB: 8192, DiskGB: 60, ImageID: "debian-13", Network: "labnet"},
		{Host: "hv1", Role: "worker", Kind: "vm", Count: 2},
		{Host: "hv2", Role: "load-balancer", Kind: "vm", Count: 1, VCPUs: 1, MemMB: 1024, DiskGB: 10, ImageID: "debian-13"},
		{Host: "hv3", Role: "load-balancer", Kind: "vm", Count: 1, Bridge: "br0"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d: %+v, want %+v", i, rows[i], want[i])
		}
	}
	for _, bad := range []string{
		"hv1: cp 1 cpu",         // ключ без значения
		"hv1: cp 1 cpu x",       // не число
		"hv1: cp 1 cpu 0",       // вне диапазона
		"hv1: cp 1 mem 64",      // меньше 128 МБ
		"hv1: cp 1 bad 2",       // неизвестный ключ
		"hv1: cp 1 image a b",   // не бывает: tokenize/Fields
		"hv1: lb 3",             // больше двух LB
		"hv1: lb x",             // не число
		"hv1: host lb",          // балансировщик не бывает хостом
		"hv1: cp 1 network a b", //
	} {
		if _, err := ParsePlacement(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}
