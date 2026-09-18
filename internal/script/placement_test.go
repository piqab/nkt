package script

import (
	"strings"
	"testing"
)

func TestParsePlacement(t *testing.T) {
	rows, err := ParsePlacement(`hv1: cp 1, w 2; hv2: w 2, bridge br0; hv3: host w`)
	if err != nil {
		t.Fatal(err)
	}
	want := []PlacementRow{
		{Host: "hv1", Role: "control-plane", Kind: "vm", Count: 1},
		{Host: "hv1", Role: "worker", Kind: "vm", Count: 2},
		{Host: "hv2", Role: "worker", Kind: "vm", Count: 2, Bridge: "br0"},
		{Host: "hv3", Role: "worker", Kind: "host", Count: 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d: %+v, want %+v", i, rows[i], want[i])
		}
	}
	for _, bad := range []string{"", "hv1", "hv1: cp", "hv1: cp x", "hv1: cp 0", "hv1: host", "hv1: host boss", "hv1: master 1", "h v: cp 1", "hv1: bridge br0"} {
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
	for text, want := range map[string]string{
		"on hv1 k8s create lab nodes \"hv1: cp 1\" network default": "badK8sNetworkMode",
		"on hv1 k8s create lab nodes \"hv1: boss 1\"":               "badPlacementItem",
		"on hv1 k8s create lab nodes \"hv1: cp 1\" bridge \"a b\"":  "badK8sBridge",
	} {
		_, issues := Parse(text, nil)
		if len(issues) == 0 || !strings.Contains(issueKey(issues[0]), want) {
			t.Errorf("%q: %v, ожидалось %s", text, issues, want)
		}
	}
}
