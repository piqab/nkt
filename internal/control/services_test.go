package control

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/store"
)

// servicesSetup wires a ServiceManager straight against the repo's own
// fixtures/host — unlike renewSetup's copy, nothing here writes to disk
// (Logs and KillProcess are both read-only from the filesystem's point of
// view; the fixtures collector only ever simulates "kill", it never sends
// a real signal), so there is no risk to the tracked tree.
func servicesSetup(t *testing.T) *ServiceManager {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Mode: config.ModeFixtures, FixturesRoot: root, CommandTimeout: 5 * time.Second}
	c := collect.NewFixtures(root)
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewServiceManager(cfg, c, db)
}

func TestServiceActionEnableDisable(t *testing.T) {
	m := servicesSetup(t)
	for _, action := range []string{"enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			res, err := m.Action(context.Background(), "test", "nginx", action)
			if err != nil {
				t.Fatalf("Action(%q): %v", action, err)
			}
			if !res.OK() {
				t.Errorf("Action(%q).OK() = false, exit %d: %s", action, res.ExitCode, res.Output())
			}
		})
	}
}

func TestServiceActionRejectsUnknownAction(t *testing.T) {
	m := servicesSetup(t)
	if _, err := m.Action(context.Background(), "test", "nginx", "mask"); err == nil {
		t.Error("Action(nginx, mask) = nil error, want rejection — mask is not in allowedActions")
	}
}

func TestServiceLogs(t *testing.T) {
	m := servicesSetup(t)

	t.Run("known service", func(t *testing.T) {
		out, err := m.Logs(context.Background(), "test", "nginx", 50)
		if err != nil {
			t.Fatalf("Logs: %v", err)
		}
		if !strings.Contains(out, "nginx") {
			t.Errorf("Logs output = %q, want it to mention nginx (fixtures/host/.commands/journalctl.txt)", out)
		}
	})

	t.Run("unknown service", func(t *testing.T) {
		if _, err := m.Logs(context.Background(), "test", "not-a-real-service", 50); err == nil {
			t.Error("Logs(not-a-real-service) = nil error, want one")
		}
	})

	t.Run("out-of-range lines falls back to the default", func(t *testing.T) {
		// Mostly a "does not error/panic" check — the fixture always
		// returns the same canned file regardless of -n, so this can't
		// observe the clamp's effect on the actual command directly.
		if _, err := m.Logs(context.Background(), "test", "nginx", -5); err != nil {
			t.Errorf("Logs with a negative line count: %v", err)
		}
		if _, err := m.Logs(context.Background(), "test", "nginx", 999999); err != nil {
			t.Errorf("Logs with an absurdly large line count: %v", err)
		}
	})
}

func TestKillProcess(t *testing.T) {
	m := servicesSetup(t)

	t.Run("matching command: killed", func(t *testing.T) {
		// From fixtures/host/.commands/ps.txt: pid 1400, exact command line.
		const pid = 1400
		const cmd = "python3 -m http.server 8082 --directory /srv/tmp-share"
		if err := m.KillProcess(context.Background(), "test", pid, cmd, "TERM"); err != nil {
			t.Errorf("KillProcess with a matching command: %v", err)
		}
	})

	t.Run("stale command (PID reuse guard): refused", func(t *testing.T) {
		const pid = 1400
		if err := m.KillProcess(context.Background(), "test", pid, "totally different process", "TERM"); err == nil {
			t.Error("KillProcess with a mismatched command = nil error, want refusal")
		}
	})

	t.Run("process not found: refused", func(t *testing.T) {
		if err := m.KillProcess(context.Background(), "test", 999999, "anything", "TERM"); err == nil {
			t.Error("KillProcess for a nonexistent pid = nil error, want refusal")
		}
	})

	t.Run("invalid signal rejected", func(t *testing.T) {
		if err := m.KillProcess(context.Background(), "test", 1400, "x", "HUP"); err == nil {
			t.Error("KillProcess with signal=HUP = nil error, want rejection — only TERM/KILL are allowed")
		}
	})

	t.Run("invalid pid rejected", func(t *testing.T) {
		if err := m.KillProcess(context.Background(), "test", 0, "x", "TERM"); err == nil {
			t.Error("KillProcess with pid=0 = nil error, want rejection")
		}
		if err := m.KillProcess(context.Background(), "test", -1, "x", "TERM"); err == nil {
			t.Error("KillProcess with a negative pid = nil error, want rejection")
		}
	})
}
