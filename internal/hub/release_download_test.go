package hub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindSHA256(t *testing.T) {
	sums := []byte(strings.Join([]string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  nkt-linux-amd64",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  nkt-linux-arm64",
		// sha256sum's binary-mode "*" prefix on the filename must not stop
		// it matching — the release workflow's own invocation determines
		// which mode gets used, not this code.
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc *nkt-linux-arm",
	}, "\n"))

	cases := []struct {
		asset   string
		want    string
		wantErr bool
	}{
		{"nkt-linux-amd64", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"nkt-linux-arm64", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", false},
		{"nkt-linux-arm", "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", false},
		{"nkt-linux-386", "", true},
	}
	for _, c := range cases {
		got, err := findSHA256(sums, c.asset)
		if c.wantErr {
			if err == nil {
				t.Errorf("findSHA256(%q): expected error, got %q", c.asset, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("findSHA256(%q): %v", c.asset, err)
			continue
		}
		if got != c.want {
			t.Errorf("findSHA256(%q) = %q, want %q", c.asset, got, c.want)
		}
	}
}

// TestDownloadReleaseBinaryLive actually downloads and verifies a real
// binary from this project's own GitHub Releases — the one thing the
// hermetic TestFindSHA256 above cannot cover. Skipped unless
// NKT_TEST_LIVE_RELEASE_DOWNLOAD=1, since it needs real network access and
// a published release matching the checked-out VERSION file; run it by
// hand after touching downloadReleaseBinary/findSHA256/fetchReleaseBytes.
func TestDownloadReleaseBinaryLive(t *testing.T) {
	if os.Getenv("NKT_TEST_LIVE_RELEASE_DOWNLOAD") != "1" {
		t.Skip("set NKT_TEST_LIVE_RELEASE_DOWNLOAD=1 to run (downloads a real release from GitHub)")
	}

	version := os.Getenv("NKT_TEST_LIVE_RELEASE_VERSION")
	if version == "" {
		// The checked-out VERSION file itself may be ahead of the latest
		// tagged release (bumped after the last release but not tagged
		// yet) — NKT_TEST_LIVE_RELEASE_VERSION overrides this for exactly
		// that case.
		versionBytes, err := os.ReadFile(filepath.Join("..", "..", "VERSION"))
		if err != nil {
			t.Fatalf("read VERSION: %v", err)
		}
		version = strings.TrimSpace(string(versionBytes))
	}

	m, _ := newTestManager(t)
	m.version = version
	m.cfg.HubReleaseRepo = "piqab/nkt"

	dest := filepath.Join(t.TempDir(), "nkt-linux-amd64")
	var events []string
	err := m.downloadReleaseBinary(context.Background(), "linux", "amd64", dest,
		func(key string, args ...any) { events = append(events, key) })
	if err != nil {
		t.Fatalf("downloadReleaseBinary: %v\nevents: %v", err, events)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat downloaded binary: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("downloaded binary is empty")
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("downloaded binary is not executable: mode %v", info.Mode())
	}
}
