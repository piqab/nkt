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
//
// ModeLocal and ModeHub must resolve to DIFFERENT directories — a shared
// default would mean a hub started without NKT_DATA_DIR explicitly set
// (any invocation that skips hub.env) silently reuses a plain local nkt's
// own data directory on any machine running both, up to and including
// `nkt hub delete` shredding the wrong install's data.
func TestDefaultDataDirDependsOnMode(t *testing.T) {
	if got := defaultDataDir(ModeLocal, "/home/user/nkt/dist"); got != "/var/lib/netknownsthat" {
		t.Errorf("каталог данных для local = %q, ожидался /var/lib/netknownsthat", got)
	}
	if got := defaultDataDir(ModeHub, "/home/user/nkt/dist"); got != "/var/lib/netknownsthat-hub" {
		t.Errorf("каталог данных для hub = %q, ожидался /var/lib/netknownsthat-hub", got)
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

// TestTerminalEnabledDefaultDependsOnMode: the "localhost" row a hub shows
// is the machine already running the hub process itself, so gating its
// terminal behind the same opt-in a *remote* managed host needs grants
// nothing there — on by default only for ModeHub, unchanged (off) for a
// plain single-host nkt. An explicit NKT_TERMINAL_ENABLED, either way,
// still always wins over the mode-based default.
func TestTerminalEnabledDefaultDependsOnMode(t *testing.T) {
	for _, tc := range []struct {
		mode Mode
		want bool
	}{
		{ModeFixtures, false},
		{ModeLocal, false},
		{ModeHub, true},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			t.Setenv("NKT_MODE", string(tc.mode))
			t.Setenv("NKT_DATA_DIR", t.TempDir())
			t.Setenv("NKT_TERMINAL_ENABLED", "")

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.TerminalEnabled != tc.want {
				t.Errorf("TerminalEnabled для %s = %v, ожидался %v", tc.mode, cfg.TerminalEnabled, tc.want)
			}
		})
	}

	t.Run("explicit false overrides the ModeHub default", func(t *testing.T) {
		t.Setenv("NKT_MODE", string(ModeHub))
		t.Setenv("NKT_DATA_DIR", t.TempDir())
		t.Setenv("NKT_TERMINAL_ENABLED", "false")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.TerminalEnabled {
			t.Error("явный NKT_TERMINAL_ENABLED=false должен побеждать дефолт для ModeHub")
		}
	})
}
