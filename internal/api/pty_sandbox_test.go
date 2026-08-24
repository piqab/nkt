package api

import (
	"context"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"testing"
)

// listenUnixSocket starts a real Unix-domain listener at path and closes it
// on test cleanup — systemdControlSocket dials each candidate path rather
// than just os.Stat'ing it (a stale regular file must not read as "D-Bus
// available"), so tests standing in for a live control socket need an
// actual listener behind the path, not merely a file with that name.
func listenUnixSocket(t *testing.T, path string) {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen unix %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

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

	// A real observed failure mode: systemd as PID 1 (INVOCATION_ID set)
	// and systemd-run on PATH, but no dbus ever installed/started — the
	// exact combination that used to make it into systemd-run's own
	// "Failed to connect to bus" error inside the terminal instead of a
	// clean fallback.
	t.Run("false with INVOCATION_ID and systemd-run on PATH but no control socket", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "deadbeef")
		withFakeSystemdRunOnPath(t)
		withSystemdControlSocketPaths(t, t.TempDir()+"/private", t.TempDir()+"/system_bus_socket")
		if usingSystemdSandbox() {
			t.Error("usingSystemdSandbox() = true with neither control socket present")
		}
	})

	t.Run("true with INVOCATION_ID, systemd-run on PATH, and a control socket present", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "deadbeef")
		withFakeSystemdRunOnPath(t)
		dir := t.TempDir()
		socket := dir + "/private"
		listenUnixSocket(t, socket)
		withSystemdControlSocketPaths(t, socket, dir+"/system_bus_socket")
		if !usingSystemdSandbox() {
			t.Error("usingSystemdSandbox() = false with a control socket present")
		}
	})
}

// withFakeSystemdRunOnPath points PATH at a directory containing an
// executable named systemd-run, so exec.LookPath("systemd-run") succeeds
// without needing the real thing installed.
func withFakeSystemdRunOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/systemd-run"
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create fake systemd-run: %v", err)
	}
	t.Setenv("PATH", dir)
}

// withSystemdControlSocketPaths swaps systemdControlSocketPaths for the
// duration of the test and restores it afterward.
func withSystemdControlSocketPaths(t *testing.T, paths ...string) {
	t.Helper()
	orig := systemdControlSocketPaths
	systemdControlSocketPaths = paths
	t.Cleanup(func() { systemdControlSocketPaths = orig })
}

// withFailingSystemdRunOnPath points PATH at a directory containing an
// executable named systemd-run that always exits non-zero — stands in for
// the real thing failing to reach the bus (e.g. "Failed to connect to bus:
// Connection refused"), without needing an actually-broken D-Bus to test
// against.
func withFailingSystemdRunOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/systemd-run", []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("create failing fake systemd-run: %v", err)
	}
	t.Setenv("PATH", dir)
}

// TestSystemdControlSocket is a narrower, direct check of the fallback
// systemdControlSocket applies: present at either candidate path is enough.
func TestSystemdControlSocket(t *testing.T) {
	t.Run("neither path exists", func(t *testing.T) {
		dir := t.TempDir()
		withSystemdControlSocketPaths(t, dir+"/private", dir+"/system_bus_socket")
		if systemdControlSocket() {
			t.Error("systemdControlSocket() = true with neither path present")
		}
	})

	t.Run("only the direct systemd socket exists", func(t *testing.T) {
		dir := t.TempDir()
		direct := dir + "/private"
		listenUnixSocket(t, direct)
		withSystemdControlSocketPaths(t, direct, dir+"/system_bus_socket")
		if !systemdControlSocket() {
			t.Error("systemdControlSocket() = false with the direct socket present")
		}
	})

	t.Run("only the classic dbus socket exists", func(t *testing.T) {
		dir := t.TempDir()
		classic := dir + "/system_bus_socket"
		listenUnixSocket(t, classic)
		withSystemdControlSocketPaths(t, dir+"/private", classic)
		if !systemdControlSocket() {
			t.Error("systemdControlSocket() = false with the classic dbus socket present")
		}
	})

	t.Run("path exists but nothing is listening (stale file)", func(t *testing.T) {
		dir := t.TempDir()
		stale := dir + "/private"
		if err := os.WriteFile(stale, nil, 0o600); err != nil {
			t.Fatalf("create stale file: %v", err)
		}
		withSystemdControlSocketPaths(t, stale, dir+"/system_bus_socket")
		if systemdControlSocket() {
			t.Error("systemdControlSocket() = true for a stale file with nothing listening behind it")
		}
	})
}

// TestSystemdRunReachable locks in the exact bug this function exists to
// fix: a real, observed case where systemctl stop dbus left something
// still accepting raw connections on a control-socket path (so
// systemdControlSocket alone read "available"), while systemd-run itself
// still failed with "Failed to connect to bus: Connection refused" —
// visible as the terminal's own broken output, with the status badge
// claiming everything was fine. systemdRunReachable must actually invoke
// systemd-run and trust its real exit code over the socket check alone.
func TestSystemdRunReachable(t *testing.T) {
	t.Run("false when neither control socket accepts a connection", func(t *testing.T) {
		withSystemdControlSocketPaths(t, t.TempDir()+"/private", t.TempDir()+"/system_bus_socket")
		if systemdRunReachable() {
			t.Error("systemdRunReachable() = true with neither control socket present")
		}
	})

	t.Run("false when a socket accepts a connection but systemd-run itself fails", func(t *testing.T) {
		dir := t.TempDir()
		socket := dir + "/private"
		listenUnixSocket(t, socket)
		withSystemdControlSocketPaths(t, socket, dir+"/system_bus_socket")
		withFailingSystemdRunOnPath(t)
		if systemdRunReachable() {
			t.Error("systemdRunReachable() = true even though the real systemd-run invocation failed — exactly the stale-socket case this exists to catch")
		}
	})

	t.Run("true when a socket accepts a connection and systemd-run succeeds", func(t *testing.T) {
		dir := t.TempDir()
		socket := dir + "/private"
		listenUnixSocket(t, socket)
		withSystemdControlSocketPaths(t, socket, dir+"/system_bus_socket")
		withFakeSystemdRunOnPath(t)
		if !systemdRunReachable() {
			t.Error("systemdRunReachable() = false with a live socket and a succeeding systemd-run")
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

// TestNeedsNsenterFallback locks in the two conditions gating the nsenter
// escape hatch (see needsNsenterFallback's own doc comment): a real
// systemd unit context (INVOCATION_ID) and nsenter actually on PATH.
func TestNeedsNsenterFallback(t *testing.T) {
	t.Run("false without INVOCATION_ID", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "")
		t.Setenv("PATH", withFakeBinaryOnPath(t, "nsenter"))
		if needsNsenterFallback() {
			t.Error("needsNsenterFallback() = true without INVOCATION_ID set")
		}
	})

	t.Run("false when nsenter is not on PATH, even with INVOCATION_ID", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "deadbeef")
		t.Setenv("PATH", t.TempDir())
		if needsNsenterFallback() {
			t.Error("needsNsenterFallback() = true with nsenter absent from PATH")
		}
	})

	t.Run("true with INVOCATION_ID and nsenter on PATH", func(t *testing.T) {
		t.Setenv("INVOCATION_ID", "deadbeef")
		t.Setenv("PATH", withFakeBinaryOnPath(t, "nsenter"))
		if !needsNsenterFallback() {
			t.Error("needsNsenterFallback() = false with nsenter present and INVOCATION_ID set")
		}
	})
}

// withFakeBinaryOnPath creates an executable file named name in a fresh
// temp dir and returns that dir, for pointing PATH at so
// exec.LookPath(name) succeeds without needing the real binary installed.
func withFakeBinaryOnPath(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/"+name, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create fake %s: %v", name, err)
	}
	return dir
}

// TestNsenterArgs is a pure check of the flags — missing --mount or the
// wrong --target would either fail outright or (worse) silently enter the
// wrong namespace; the env-passing trick is exercised separately since
// nsenter itself has no --setenv equivalent, unlike systemd-run.
func TestNsenterArgs(t *testing.T) {
	t.Run("no env", func(t *testing.T) {
		args := nsenterArgs(nil, "bash", "-l")
		want := []string{"--target", "1", "--mount", "--", "bash", "-l"}
		if strings.Join(args, "|") != strings.Join(want, "|") {
			t.Errorf("nsenterArgs(nil, ...) = %v, want %v", args, want)
		}
	})

	t.Run("with env: wrapped in coreutils env", func(t *testing.T) {
		args := nsenterArgs(map[string]string{"TERM": "xterm-256color"}, "bash", "-l")
		joined := " " + strings.Join(args, " ") + " "
		for _, want := range []string{" --target 1 ", " --mount ", " -- env ", " TERM=xterm-256color "} {
			if !strings.Contains(joined, want) {
				t.Errorf("nsenterArgs() missing %q in %v", strings.TrimSpace(want), args)
			}
		}
		// The target command must still come last.
		if len(args) < 2 || args[len(args)-2] != "bash" || args[len(args)-1] != "-l" {
			t.Errorf("nsenterArgs() target command not at the end: %v", args)
		}
	})
}

// TestUnrestrictedCommandFallsBackToNsenter confirms the three-tier
// priority: systemd-run first when usable, nsenter next when the unit
// context is real but systemd-run isn't (no control socket — exactly the
// no-dbus case), plain exec only when there's no unit context at all.
func TestUnrestrictedCommandFallsBackToNsenter(t *testing.T) {
	t.Setenv("INVOCATION_ID", "deadbeef")
	t.Setenv("PATH", withFakeBinaryOnPath(t, "nsenter")) // no systemd-run on this PATH
	withSystemdControlSocketPaths(t, t.TempDir()+"/private", t.TempDir()+"/system_bus_socket")

	cmd := unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, "bash", "-l")

	if cmd.Path == "" || !strings.HasSuffix(cmd.Path, "nsenter") {
		t.Fatalf("unrestrictedCommand() cmd.Path = %q, want it to resolve to the fake nsenter", cmd.Path)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--mount") || !strings.Contains(joined, "bash -l") {
		t.Errorf("unrestrictedCommand() args = %v, want nsenter args wrapping bash -l", cmd.Args)
	}
}

// TestSystemdRunArgsAsUser is the User= counterpart of TestSystemdRunArgs.
// It must NOT carry CapabilityBoundingSet=~ — that override has no effect
// on a non-root UID (which needs AmbientCapabilities= to keep any
// capability, not a wide bounding set) and its presence here would just be
// misleading dead weight in the transient unit's properties.
func TestSystemdRunArgsAsUser(t *testing.T) {
	args := systemdRunArgsAsUser(map[string]string{"TERM": "xterm-256color"}, "deploy", "bash", "-l")
	joined := " " + strings.Join(args, " ") + " "

	for _, want := range []string{
		" --pty ", " --collect ", " --quiet ",
		" -p ProtectSystem=no ",
		" -p ProtectHome=no ",
		" -p PrivateTmp=no ",
		" -p NoNewPrivileges=no ",
		" -p RestrictSUIDSGID=no ",
		" -p RestrictNamespaces=no ",
		" -p User=deploy ",
		" --setenv=TERM=xterm-256color ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("systemdRunArgsAsUser() missing %q in %v", strings.TrimSpace(want), args)
		}
	}
	if strings.Contains(joined, "CapabilityBoundingSet") {
		t.Errorf("systemdRunArgsAsUser() must not set CapabilityBoundingSet, got %v", args)
	}

	if len(args) < 2 || args[len(args)-2] != "bash" || args[len(args)-1] != "-l" {
		t.Errorf("systemdRunArgsAsUser() target command not at the end: %v", args)
	}
}

// TestNsenterArgsAsUser is the runuser-wrapping counterpart of
// TestNsenterArgs — nsenter itself has no notion of switching users, so the
// user switch has to happen via runuser once already inside the target
// mount namespace, before env/the target command.
func TestNsenterArgsAsUser(t *testing.T) {
	t.Run("no env", func(t *testing.T) {
		args := nsenterArgsAsUser(nil, "deploy", "bash", "-l")
		want := []string{"--target", "1", "--mount", "--", "runuser", "-u", "deploy", "--", "bash", "-l"}
		if strings.Join(args, "|") != strings.Join(want, "|") {
			t.Errorf("nsenterArgsAsUser(nil, ...) = %v, want %v", args, want)
		}
	})

	t.Run("with env: wrapped in coreutils env after runuser", func(t *testing.T) {
		args := nsenterArgsAsUser(map[string]string{"TERM": "xterm-256color"}, "deploy", "bash", "-l")
		joined := " " + strings.Join(args, " ") + " "
		for _, want := range []string{
			" --target 1 ", " --mount ", " -- runuser -u deploy -- env ", " TERM=xterm-256color ",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("nsenterArgsAsUser() missing %q in %v", strings.TrimSpace(want), args)
			}
		}
		if len(args) < 2 || args[len(args)-2] != "bash" || args[len(args)-1] != "-l" {
			t.Errorf("nsenterArgsAsUser() target command not at the end: %v", args)
		}
	})
}

// TestSystemdRunQuietArgs is the no-PTY counterpart of TestSystemdRunArgs —
// used for output-capturing calls (tmux list-windows/select-window, ...)
// that have no controlling terminal to attach --pty to. --pipe+--wait is
// what makes cmd.Output() work for these the same way it would for a plain
// exec.Command.
func TestSystemdRunQuietArgs(t *testing.T) {
	args := systemdRunQuietArgs(map[string]string{"TERM": "xterm-256color"}, "tmux", "list-windows")
	joined := " " + strings.Join(args, " ") + " "

	for _, want := range []string{
		" --pipe ", " --wait ", " --collect ", " --quiet ",
		" -p ProtectSystem=no ",
		" -p CapabilityBoundingSet=~ ",
		" --setenv=TERM=xterm-256color ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("systemdRunQuietArgs() missing %q in %v", strings.TrimSpace(want), args)
		}
	}
	if strings.Contains(joined, " --pty ") {
		t.Errorf("systemdRunQuietArgs() must not request a pty (no controlling terminal to attach it to), got %v", args)
	}
	if len(args) < 2 || args[len(args)-2] != "tmux" || args[len(args)-1] != "list-windows" {
		t.Errorf("systemdRunQuietArgs() target command not at the end: %v", args)
	}
}

// TestSystemdRunQuietArgsAsUser is systemdRunQuietArgs' User= counterpart —
// same no-CapabilityBoundingSet reasoning as systemdRunArgsAsUser.
func TestSystemdRunQuietArgsAsUser(t *testing.T) {
	args := systemdRunQuietArgsAsUser(map[string]string{"TERM": "xterm-256color"}, "deploy", "tmux", "list-windows")
	joined := " " + strings.Join(args, " ") + " "

	for _, want := range []string{" --pipe ", " --wait ", " -p User=deploy "} {
		if !strings.Contains(joined, want) {
			t.Errorf("systemdRunQuietArgsAsUser() missing %q in %v", strings.TrimSpace(want), args)
		}
	}
	if strings.Contains(joined, " --pty ") || strings.Contains(joined, "CapabilityBoundingSet") {
		t.Errorf("systemdRunQuietArgsAsUser() must not have --pty or CapabilityBoundingSet, got %v", args)
	}
}

// TestUnrestrictedQuietCommandAsUserRejectsUnknownUser mirrors
// TestUnrestrictedCommandAsUserRejectsUnknownUser for the quiet variant.
func TestUnrestrictedQuietCommandAsUserRejectsUnknownUser(t *testing.T) {
	_, err := unrestrictedQuietCommandAsUser(context.Background(), nil, "no-such-user-xyz123", "tmux", "list-windows")
	if err == nil {
		t.Fatal("unrestrictedQuietCommandAsUser() with an unknown username: expected an error, got nil")
	}
}

// TestUnrestrictedQuietCommandCapturesOutput confirms the plain-exec
// fallback (outside any systemd unit) actually returns output through
// cmd.Output() the way the tmux control handlers rely on — unlike
// unrestrictedCommand, which is only ever run under a PTY.
func TestUnrestrictedQuietCommandCapturesOutput(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")

	cmd := unrestrictedQuietCommand(context.Background(), nil, "echo", "hello")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("cmd.Output(): %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("cmd.Output() = %q, want %q", out, "hello")
	}
}

// TestUnrestrictedCommandAsUserRejectsUnknownUser confirms the error path
// (a typo'd or stale ssh_user in the host's NKT_TERMINAL_USER) surfaces as
// a clear message instead of a cryptic downstream exec failure.
func TestUnrestrictedCommandAsUserRejectsUnknownUser(t *testing.T) {
	_, err := unrestrictedCommandAsUser(nil, "no-such-user-xyz123", "bash", "-l")
	if err == nil {
		t.Fatal("unrestrictedCommandAsUser() with an unknown username: expected an error, got nil")
	}
}

// TestUnrestrictedCommandAsUserPlainExecSetsCredential confirms the
// outside-systemd fallback (dev machine, test suite) resolves the target
// user's uid/gid and sets them via SysProcAttr.Credential, plus injects
// HOME/USER/LOGNAME — the three vars systemd-run's own User= would resolve
// automatically via NSS but plain exec.Command has no equivalent for.
func TestUnrestrictedCommandAsUserPlainExecSetsCredential(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")

	me, err := user.Current()
	if err != nil {
		t.Skipf("user.Current(): %v", err)
	}

	cmd, err := unrestrictedCommandAsUser(map[string]string{"TERM": "xterm-256color"}, me.Username, "echo", "hi")
	if err != nil {
		t.Fatalf("unrestrictedCommandAsUser: %v", err)
	}

	if got := cmd.Args; len(got) != 2 || got[0] != "echo" || got[1] != "hi" {
		t.Errorf("cmd.Args = %v, want [echo hi]", got)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		t.Fatal("cmd.SysProcAttr.Credential not set")
	}
	if got := strconv.FormatUint(uint64(cmd.SysProcAttr.Credential.Uid), 10); got != me.Uid {
		t.Errorf("cmd.SysProcAttr.Credential.Uid = %s, want %s", got, me.Uid)
	}

	wantEnv := map[string]string{
		"TERM":    "xterm-256color",
		"HOME":    me.HomeDir,
		"USER":    me.Username,
		"LOGNAME": me.Username,
	}
	for k, v := range wantEnv {
		found := false
		for _, kv := range cmd.Env {
			if kv == k+"="+v {
				found = true
			}
		}
		if !found {
			t.Errorf("cmd.Env missing %s=%s: %v", k, v, cmd.Env)
		}
	}
}
