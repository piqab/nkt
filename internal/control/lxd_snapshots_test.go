package control

import "testing"

func TestParseLXDSnapshots(t *testing.T) {
	data := `[
 {"name":"snap0","created_at":"2026-09-20T10:00:00Z","expires_at":"0001-01-01T00:00:00Z","stateful":false,"size":1024},
 {"name":"c1/before-upgrade","created_at":"2026-09-24T10:00:00Z","expires_at":"2026-10-01T00:00:00Z","stateful":true,"size":-1}
]`
	list, err := parseLXDSnapshots([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "before-upgrade" || !list[0].Stateful || list[0].ExpiresAt == nil {
		t.Fatalf("first: %+v", list)
	}
	if list[1].ExpiresAt != nil || list[1].Size != 1024 {
		t.Fatalf("second: %+v", list[1])
	}
	if l, err := parseLXDSnapshots(nil); err != nil || len(l) != 0 {
		t.Fatalf("empty: %v %v", l, err)
	}
}

func TestLXDSnapshotNames(t *testing.T) {
	for _, n := range []string{"snap0", "before-upgrade", "v1.2_x"} {
		if !lxdSnapshotNameRe.MatchString(n) {
			t.Errorf("%q rejected", n)
		}
	}
	for _, n := range []string{"", "-x", "a/b", "a b", "$(x)"} {
		if lxdSnapshotNameRe.MatchString(n) {
			t.Errorf("%q accepted", n)
		}
	}
}
