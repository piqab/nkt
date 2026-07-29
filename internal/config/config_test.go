package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

// A bare invocation must do the right thing on the platform it is on: inspect
// the real host on Linux, fall back to the snapshot everywhere else.
func TestDefaultModeFollowsPlatform(t *testing.T) {
	got := defaultMode()
	want := ModeFixtures
	if runtime.GOOS == "linux" {
		want = ModeLocal
	}
	if got != want {
		t.Errorf("режим по умолчанию на %s = %q, ожидался %q", runtime.GOOS, got, want)
	}
}

// Production state belongs in the system location the systemd unit uses, so a
// manually started `nkt tui` reads the same database the service writes.
func TestDefaultDataDirDependsOnMode(t *testing.T) {
	if got := defaultDataDir(ModeLocal, "/home/user/nkt/dist"); got != "/var/lib/netknownsthat" {
		t.Errorf("каталог данных для local = %q, ожидался /var/lib/netknownsthat", got)
	}
	want := filepath.Join("/repo", "data")
	if got := defaultDataDir(ModeFixtures, "/repo"); got != want {
		t.Errorf("каталог данных для fixtures = %q, ожидался %q", got, want)
	}
}
