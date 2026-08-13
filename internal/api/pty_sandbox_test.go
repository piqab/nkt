package api

import (
	"strings"
	"testing"
)

// TestUsingSystemdSandbox locks in the two conditions that must both hold
// before terminal/update sessions get routed through systemd-run: running
// as a systemd unit (INVOCATION_ID) and systemd-run actually being
// resolvable on PATH. Getting either wrong either silently drops the
// escape hatch when it's needed (a real regression, hard to notice since
// the plain fallback still runs a shell, just a sandboxed one) or forces
// every dev/test invocation through systemd-run where none exists.
func TestUsingSystemdSandbox(t *testing.T) {
	t.Run("false without INVOCATION_ID", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "")
		if usingSystemdSandbox() {
			t.Error("usingSystemdSandbox() = true without INVOCATION_ID set")
		}
	})

	t.Run("false when systemd-run is not on PATH, even with INVOCATION_ID", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "deadbeef")
		t.Setenv("PATH", t.TempDir()) // empty dir: LookPath("systemd-run") must fail
		if usingSystemdSandbox() {
			t.Error("usingSystemdSandbox() = true with systemd-run absent from PATH")
		}
	})
}

// TestSystemdRunArgs is a pure check of the flags themselves — no systemd
// system is needed to run this. Missing --pty would silently turn an
// interactive shell/apt-get session into a fire-and-forget detached one;
// missing any of the -p overrides would leave the new transient unit
// still constrained by whatever systemd's own compiled-in or
// distro-supplied defaults happen to be, defeating the entire point of
// this escape hatch without any obviously-broken symptom (it would just
// silently start working "sometimes", depending on the host).
func TestSystemdRunArgs(t *testing.T) {
	args := systemdRunArgs(map[string]string{"TERM": "xterm-256color"}, "bash", "-l")
	joined := " " + strings.Join(args, " ") + " "

	for _, want := range []string{
		" --pty ", " --collect ", " --quiet ",
		" -p ProtectSystem=no ",
		" -p ProtectHome=no ",
		" -p PrivateTmp=no ",
		" -p NoNewPrivileges=no ",
		" -p RestrictSUIDSGID=no ",
		" -p RestrictNamespaces=no ",
		" -p CapabilityBoundingSet=~ ",
		" --setenv=TERM=xterm-256color ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("systemdRunArgs() missing %q in %v", strings.TrimSpace(want), args)
		}
	}

	// The target command must come last, after a bare "--" separator, so
	// systemd-run can't misinterpret "bash -l" as more of its own flags.
	if len(args) < 2 || args[len(args)-2] != "bash" || args[len(args)-1] != "-l" {
		t.Errorf("systemdRunArgs() target command not at the end: %v", args)
	}
	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
		}
	}
	if dashIdx == -1 || dashIdx != len(args)-3 {
		t.Errorf("systemdRunArgs() \"--\" separator not immediately before the target command: %v", args)
	}
}

// TestUnrestrictedCommandFallsBackOutsideSystemd confirms the common case
// (dev machine, test suite, a host not running netknownsthat.service) is
// unaffected by any of this: no INVOCATION_ID means a plain exec.Command,
// same as before this escape hatch existed.
func TestUnrestrictedCommandFallsBackOutsideSystemd(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")

	cmd := unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, "echo", "hi")

	if got := cmd.Args; len(got) != 2 || got[0] != "echo" || got[1] != "hi" {
		t.Errorf("cmd.Args = %v, want [echo hi]", got)
	}
	found := false
	for _, kv := range cmd.Env {
		if kv == "TERM=xterm-256color" {
			found = true
		}
	}
	if !found {
		t.Errorf("cmd.Env missing TERM=xterm-256color: %v", cmd.Env)
	}
}
