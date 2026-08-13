package parse

import (
	"context"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
)

func TestPackagesParsesFixture(t *testing.T) {
	result, status := Packages(context.Background(), fixtureCollector(t))
	if !status.Available {
		t.Fatalf("status.Available = false, want true (fixture stubs apt-get)")
	}
	if len(result.Packages) != 2 {
		t.Fatalf("Packages = %+v, want 2 entries", result.Packages)
	}
	if result.Packages[0].Name != "openssl" || result.Packages[0].NewVersion != "3.0.2-0ubuntu1.15" ||
		result.Packages[0].OldVersion != "3.0.2-0ubuntu1.14" {
		t.Errorf("Packages[0] = %+v, unexpected parse", result.Packages[0])
	}
}

// TestPackagesUnavailableWithoutApt guards the degrade-gracefully path: a
// host with no apt-get (any non-Debian distro) must report Available=false,
// not an error — the same way podman/libvirt behave when their own tool is
// missing, since "not applicable here" and "broken" are different things
// the frontend needs to tell apart.
func TestPackagesUnavailableWithoutApt(t *testing.T) {
	result, status := Packages(context.Background(), collect.NewFixtures(t.TempDir()))
	if status.Available {
		t.Error("status.Available = true on a host with no apt-get stub, want false")
	}
	if status.Error != "" {
		t.Errorf("status.Error = %q, want empty (missing apt-get is not an error)", status.Error)
	}
	if result.Packages != nil {
		t.Errorf("Packages = %+v, want nil", result.Packages)
	}
}
