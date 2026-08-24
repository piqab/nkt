package parse

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
)

func TestManifestUnavailableWithoutDpkgStatus(t *testing.T) {
	m := Manifest(collect.NewFixtures(t.TempDir()))
	if m.Available {
		t.Error("Available = true on a host with no /var/lib/dpkg/status, want false")
	}
	if m.DpkgStatus != "" {
		t.Errorf("DpkgStatus = %q, want empty", m.DpkgStatus)
	}
}

func TestManifestReadsDpkgAndOSFiles(t *testing.T) {
	root := t.TempDir()
	writeAt(t, root, "var/lib/dpkg/status", "Package: bash\nVersion: 5.2.15-2+b7\n")
	writeAt(t, root, "etc/os-release", "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")
	writeAt(t, root, "etc/debian_version", "12.5\n")

	m := Manifest(collect.NewFixtures(root))
	if !m.Available {
		t.Fatal("Available = false, want true")
	}
	if m.DpkgStatus == "" || m.OSRelease == "" || m.DebianVersion == "" {
		t.Errorf("Manifest = %+v, want all three fields populated", m)
	}
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
