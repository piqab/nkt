package control

import (
	"encoding/json"
	"testing"
)

func TestLXDConfigBody(t *testing.T) {
	body, err := lxdConfigBody("architecture: x86_64\nconfig:\n  limits.cpu: 2\n  boot.autostart: true\n  limits.memory: 2GiB\n  x:\ndevices: {}\nprofiles:\n- default\n")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Config   map[string]any `json:"config"`
		Profiles []string       `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"limits.cpu": "2", "boot.autostart": "true", "limits.memory": "2GiB", "x": ""} {
		if doc.Config[k] != want {
			t.Errorf("%s = %#v, want %q", k, doc.Config[k], want)
		}
	}
	if len(doc.Profiles) != 1 {
		t.Errorf("profiles %v", doc.Profiles)
	}
	for _, bad := range []string{"", "config: [unclosed", "- a\n- b\n"} {
		if _, err := lxdConfigBody(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
