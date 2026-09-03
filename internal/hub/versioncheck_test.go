package hub

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.8.42", "1.8.41", true},
		{"1.8.41", "1.8.41", false},
		{"1.8.40", "1.8.41", false},
		{"2.0.0", "1.99.99", true},
		{"1.9.0", "1.8.99", true},
		// The hub itself already newer than whatever "latest" GitHub
		// reports (a dev build ahead of the last tagged release) must
		// never be reported as updatable — that would only downgrade it.
		{"1.8.30", "1.8.42", false},
		// Unparseable strings ("dev" builds, a bare git hash) only ever
		// compare as "differs", never guessed at.
		{"dev", "1.8.41", true},
		{"1.8.41", "dev", true},
		{"dev", "dev", false},
	}
	for _, c := range cases {
		if got := isNewerVersion(c.latest, c.current); got != c.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestVersionStatusReflectsRecordedCheck(t *testing.T) {
	m, _ := newTestManager(t)
	m.version = "1.8.41"

	// Before any check: no latest known, so never "update available" no
	// matter what — a stale cache must never claim an update exists.
	status := m.VersionStatus()
	if status.UpdateAvailable {
		t.Errorf("UpdateAvailable = true before any check ever ran")
	}
	if status.Current != "1.8.41" {
		t.Errorf("Current = %q, want %q", status.Current, "1.8.41")
	}

	m.recordVersionCheck("1.8.42", nil)
	status = m.VersionStatus()
	if !status.UpdateAvailable {
		t.Error("UpdateAvailable = false after recording a newer latest version")
	}
	if status.Latest != "1.8.42" {
		t.Errorf("Latest = %q, want %q", status.Latest, "1.8.42")
	}
	if status.CheckedAt.IsZero() {
		t.Error("CheckedAt still zero after a recorded check")
	}
	if status.CheckError != "" {
		t.Errorf("CheckError = %q, want empty after a successful check", status.CheckError)
	}

	// A subsequent failed check must not throw away the last known-good
	// latest version — only the error/timestamp should change, exactly
	// like recordUnreachable's own doc comment for hostOverview.
	m.recordVersionCheck("", errNetworkDown)
	status = m.VersionStatus()
	if status.Latest != "1.8.42" {
		t.Errorf("Latest = %q after a failed check, want the last known-good %q preserved", status.Latest, "1.8.42")
	}
	if status.CheckError == "" {
		t.Error("CheckError empty after recording a failed check")
	}
}

// errNetworkDown is a stand-in error for TestVersionStatusReflectsRecordedCheck.
var errNetworkDown = &testError{"network down"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestApplyUpdateRefusesWithoutAKnownNewerVersion(t *testing.T) {
	m, _ := newTestManager(t)
	m.version = "1.8.41"

	if err := m.ApplyUpdate(context.Background()); err == nil {
		t.Fatal("ApplyUpdate accepted with no version ever checked")
	}

	// Even with a check recorded, refuse when it isn't actually newer —
	// applying it would be a no-op at best, a downgrade at worst if the
	// hub is ahead of GitHub's latest tag (a dev build).
	m.recordVersionCheck("1.8.41", nil)
	if err := m.ApplyUpdate(context.Background()); err == nil {
		t.Fatal("ApplyUpdate accepted when latest == current")
	}
}

// TestCheckLatestVersionLive hits the real GitHub API — the one thing the
// hermetic tests above cannot cover. Skipped unless
// NKT_TEST_LIVE_VERSION_CHECK=1; run it by hand after touching
// checkLatestVersion/githubLatestRelease.
func TestCheckLatestVersionLive(t *testing.T) {
	if os.Getenv("NKT_TEST_LIVE_VERSION_CHECK") != "1" {
		t.Skip("set NKT_TEST_LIVE_VERSION_CHECK=1 to run (hits the real GitHub API)")
	}

	m, _ := newTestManager(t)
	m.cfg.HubReleaseRepo = "piqab/nkt"
	m.version = "0.0.0" // guarantees UpdateAvailable=true regardless of what's actually latest

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status := m.CheckNow(ctx)

	if status.CheckError != "" {
		t.Fatalf("CheckNow reported an error: %s", status.CheckError)
	}
	if status.Latest == "" {
		t.Fatal("CheckNow found no latest version")
	}
	if !status.UpdateAvailable {
		t.Errorf("UpdateAvailable = false with Current=0.0.0 and Latest=%q", status.Latest)
	}
	t.Logf("latest release: v%s", status.Latest)
}
