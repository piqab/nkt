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

func TestShellQuote(t *testing.T) {
	if shellQuote("a'b c") != `'a'\''b c'` {
		t.Error(shellQuote("a'b c"))
	}
}

func TestParseAptStatus(t *testing.T) {
	l, p, ok := ParsePercent("pmstatus:curl:45.4545:Unpacking curl (amd64)")
	if !ok || p != 45 || l != "Unpacking curl (amd64)" {
		t.Errorf("%q %d %v", l, p, ok)
	}
	if _, p, ok := ParsePercent("dlstatus:3:62.5:Retrieving file 3 of 8"); !ok || p != 62 {
		t.Errorf("dl %d %v", p, ok)
	}
}
