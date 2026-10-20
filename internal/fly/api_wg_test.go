package fly

import "testing"

func TestWGFailure(t *testing.T) {
	for text, want := range map[string]string{
		"[#] wg set nkt-fly private-key /etc/nkt-edge/fly/wg.key\nfopen: Permission denied": "apparmor",
		"RTNETLINK answers: Operation not supported":                                        "kernel",
		"Error: Unknown device type.":                                                       "kernel",
		"bind: Address already in use":                                                      "port",
		"something else":                                                                    "",
	} {
		if got := WGFailure(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}
