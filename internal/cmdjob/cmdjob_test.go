package cmdjob

import "testing"

func TestParsePercent(t *testing.T) {
	cases := map[string][2]any{
		"Retrieving image: rootfs: 45% (12.3MB/s)": {"Retrieving image: rootfs", 45},
		"Progress: [ 67%]":                         {"Progress", 67},
		"Unpacking image: 100%":                    {"Unpacking image", 100},
		"Exporting the backup: 5.5%":               {"Exporting the backup", 5},
	}
	for in, want := range cases {
		l, p, ok := ParsePercent(in)
		if !ok || l != want[0] || p != want[1] {
			t.Errorf("%q → %q %d %v", in, l, p, ok)
		}
	}
	for _, in := range []string{"Setting up curl (8.5.0-2) ...", "disk 120%"} {
		if _, _, ok := ParsePercent(in); ok {
			t.Errorf("%q распознан", in)
		}
	}
}
