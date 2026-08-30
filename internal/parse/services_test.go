package parse

import "testing"

// TestInstallTarget locks in the three services whose real apt/snap
// package doesn't match the logical/unit Name InstallTarget would
// otherwise fall back to, plus one that does — a regression here would
// mean handleServiceInstallWS either fails outright (bare "docker" and
// "libvirt" aren't real apt packages) or tries apt-get against a package
// that current Debian/Ubuntu don't ship (LXD, snap-only upstream).
func TestInstallTarget(t *testing.T) {
	cases := []struct {
		service     string
		wantMethod  ServiceInstallMethod
		wantPackage string
	}{
		{"docker", InstallViaAPT, "docker.io"},
		{"lxd", InstallViaSnap, "lxd"},
		{"libvirt", InstallViaAPT, "libvirt-daemon-system"},
		{"nginx", InstallViaAPT, "nginx"}, // no override: Name already is the real apt package
	}
	for _, c := range cases {
		t.Run(c.service, func(t *testing.T) {
			info, ok := InstallTarget(c.service)
			if !ok {
				t.Fatalf("InstallTarget(%q) ok = false, want true", c.service)
			}
			if info.Method != c.wantMethod {
				t.Errorf("Method = %q, want %q", info.Method, c.wantMethod)
			}
			if info.Package != c.wantPackage {
				t.Errorf("Package = %q, want %q", info.Package, c.wantPackage)
			}
			if info.Binary == "" {
				t.Error("Binary is empty — the already-installed check has nothing to collect.Which against")
			}
		})
	}

	t.Run("unknown service refused", func(t *testing.T) {
		if _, ok := InstallTarget("rm-rf-everything"); ok {
			t.Error("InstallTarget on an unmanaged name returned ok = true, want the allowlist to refuse it")
		}
	})
}
