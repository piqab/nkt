package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/althq/netknownsthat/internal/config"
)

// TestParseDpkgQueryOutput locks in the "last field, not substring" status
// check — the regression risk being "not-installed" itself ending in the
// characters "installed", which a naive strings.Contains/HasSuffix check
// would misreport as installed.
func TestParseDpkgQueryOutput(t *testing.T) {
	stdout := "neovim install ok installed\n" +
		"tmux unknown ok not-installed\n" +
		"gnupg install ok installed\n" +
		"curl deinstall ok config-files\n"

	got := parseDpkgQueryOutput(stdout)

	cases := map[string]bool{
		"neovim": true,
		"tmux":   false,
		"gnupg":  true,
		"curl":   false,
	}
	for pkg, want := range cases {
		if got[pkg] != want {
			t.Errorf("installed[%q] = %v, want %v (full map: %+v)", pkg, got[pkg], want, got)
		}
	}
	if _, ok := got["git"]; ok {
		t.Error("git was never in the input, should not appear in the output map at all")
	}
}

// TestParsePackageNames confirms the ?pkgs= query is validated against
// commonPackages (an unknown name is refused, not silently dropped or
// passed through) and maps logical names to their real apt package.
func TestParsePackageNames(t *testing.T) {
	t.Run("valid selection maps to real package names", func(t *testing.T) {
		pkgs, _, ok := parsePackageNames("nvim, tmux ,ssh")
		if !ok {
			t.Fatal("ok = false for a fully valid selection")
		}
		want := []string{"neovim", "tmux", "openssh-server"}
		if len(pkgs) != len(want) {
			t.Fatalf("pkgs = %v, want %v", pkgs, want)
		}
		for i := range want {
			if pkgs[i] != want[i] {
				t.Errorf("pkgs[%d] = %q, want %q", i, pkgs[i], want[i])
			}
		}
	})

	t.Run("unknown name refused, not silently dropped", func(t *testing.T) {
		_, unknown, ok := parsePackageNames("nvim,rm-rf-everything")
		if ok {
			t.Fatal("ok = true with an unknown package name in the selection")
		}
		if unknown != "rm-rf-everything" {
			t.Errorf("unknown = %q, want %q", unknown, "rm-rf-everything")
		}
	})

	t.Run("empty selection", func(t *testing.T) {
		pkgs, _, ok := parsePackageNames("")
		if !ok || len(pkgs) != 0 {
			t.Errorf("pkgs=%v ok=%v, want empty slice and ok=true (caller checks len==0 separately)", pkgs, ok)
		}
	})
}

// TestHandleCommonPackagesInstallWSGates covers the guard clauses that
// must return before ever spawning apt-get.
func TestHandleCommonPackagesInstallWSGates(t *testing.T) {
	t.Run("refused in fixtures mode", func(t *testing.T) {
		s := newVNCTestServer(t, &config.Config{Mode: config.ModeFixtures})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/system/packages/install/ws?pkgs=nvim", nil)
		s.handleCommonPackagesInstallWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("refused without apt-get", func(t *testing.T) {
		s := newVNCTestServer(t, &config.Config{Mode: config.ModeLocal})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/system/packages/install/ws?pkgs=nvim", nil)
		s.handleCommonPackagesInstallWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}

// TestHandleServiceInstallWSGates mirrors TestHandleVNCInstallWSGates for
// the per-service install route — fixtures mode and an unknown/unmanaged
// service name (the allowlist check) both refuse before ever reaching
// parse.InstallTarget's resolved package.
func TestHandleServiceInstallWSGates(t *testing.T) {
	t.Run("refused in fixtures mode", func(t *testing.T) {
		s := newVNCTestServer(t, &config.Config{Mode: config.ModeFixtures})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/services/nginx/install/ws", nil)
		s.handleServiceInstallWS(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}
