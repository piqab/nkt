package control

import (
	"reflect"
	"testing"
)

func TestLXDUsedBy(t *testing.T) {
	got := lxdUsedBy([]string{"/1.0/profiles/default", "/1.0/instances/c1?project=x"})
	if want := []string{"instances/c1", "profiles/default"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestLXDResourceValidation(t *testing.T) {
	for ref, ok := range map[string]bool{"images:debian/12": true, "ubuntu:24.04": true, "local:x": false, "images:": false, "images:a b": false, "https://x": false} {
		if ValidLXDImageRef(ref) != ok {
			t.Errorf("ref %q", ref)
		}
	}
	for n, ok := range map[string]bool{"lxdbr1": true, "br-a": true, "Bad": false, "1br": false, "averyveryverylongname": false} {
		if lxdNetNameRe.MatchString(n) != ok {
			t.Errorf("net %q", n)
		}
	}
	for a, ok := range map[string]bool{"auto": true, "none": true, "10.0.0.1/24": true, "fd42::1/64": true, "10.0.0.1": false, "x; rm": false} {
		if lxdNetAddrRe.MatchString(a) != ok {
			t.Errorf("addr %q", a)
		}
	}
}
