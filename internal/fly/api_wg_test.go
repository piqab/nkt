package fly

import "testing"

func TestWGFailure(t *testing.T) {
	for text, want := range map[string]string{
		"[#] wg set nkt-fly private-key /etc/nkt-edge/fly/wg.key\nfopen: Permission denied": "apparmor",
		"RTNETLINK answers: Operation not supported":                                        "kernel",
		"Error: Unknown device type.":                                                       "kernel",
		"bind: Address already in use":                                                      "port",
		`nkt-fly1: sysctl: permission denied on key "net.ipv4.ip_forward", ignoring [#] nft -f /etc/wireguard/nkt-fly1.nft internal:0:0-0: Error: Could not open file "/etc/wireguard/nkt-fly1.nft": Permission denied`: "apparmor-old",
		`apparmor="DENIED" operation="open" profile="wg-quick//nft" name="/etc/wireguard/nkt-fly1.nft" Could not open file "/etc/wireguard/nkt-fly1.nft": Permission denied`:                                            "apparmor-old",
		"something else": "",
	} {
		if got := WGFailure(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}
