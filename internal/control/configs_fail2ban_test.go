package control

import (
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/model"
)

// TestServiceForPathRecognizesFail2banRoot confirms a path under
// Fail2banRoot resolves to ServiceFail2ban directly from the explicit
// underRoot case — not just via the snap.Files fallback (which only knows
// about a file after a scan has already discovered it), the same way
// nginx/haproxy/caddy's own roots work. A real (if scan-less) Scanner is
// still needed here: serviceForPath's own fallback dereferences m.scanner
// for the negative cases below, once the explicit switch has no match.
func TestServiceForPathRecognizesFail2banRoot(t *testing.T) {
	cfg := &config.Config{Fail2banRoot: "/etc/fail2ban"}
	scanner := inventory.New(cfg, collect.NewFixtures(t.TempDir()), nil)
	m := &ConfigManager{cfg: cfg, scanner: scanner}

	cases := map[string]string{
		"/etc/fail2ban/jail.local":             model.ServiceFail2ban,
		"/etc/fail2ban/jail.d/custom.conf":     model.ServiceFail2ban,
		"/etc/fail2ban":                        model.ServiceFail2ban,
		"/etc/fail2ban-not-actually/jail.conf": "",
		"/etc/nginx/nginx.conf":                "",
	}
	for path, want := range cases {
		if got := m.serviceForPath(path); got != want {
			t.Errorf("serviceForPath(%q) = %q, want %q", path, got, want)
		}
	}
}
