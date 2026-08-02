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

// The session cookie must be safe by default: an operator who forgets to set
// NKT_COOKIE_SECURE should get an HTTPS-only cookie, not a silently insecure
// one. Opting into plain HTTP (an SSH tunnel, say) is an explicit override.
func TestCookieSecureDefaultsToTrue(t *testing.T) {
	t.Setenv("NKT_MODE", string(ModeFixtures))
	t.Setenv("NKT_DATA_DIR", t.TempDir())
	t.Setenv("NKT_COOKIE_SECURE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure по умолчанию должен быть true")
	}
}
