package monitor

import "testing"

func TestParseTopPods(t *testing.T) {
	got := ParseTopPods("shop   api-1   48m   182Mi\nkube-system  x  2  1Gi\nbad line\n")
	if len(got) != 2 || got[0].name != "shop/api-1" || got[0].cpuPct != 4.8 || got[0].memBytes != 182<<20 || got[1].cpuPct != 200 || got[1].memBytes != 1<<30 {
		t.Fatalf("%+v", got)
	}
}
