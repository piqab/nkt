package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/althq/netknownsthat/internal/auth"
)

// unrestrictedCommand builds argv the way a caller who wants to run it
// under nkt's own restrictions normally would (exec.Command(argv[0],
// argv[1:]...), env applied to cmd.Env) — except when nkt is itself
// running as netknownsthat.service, whose deliberately narrow
// ProtectSystem=strict / CapabilityBoundingSet= would otherwise apply to
// every child process this spawns, including a login shell someone opened
// specifically to administer the box, or apt-get's own internal privilege
// drop to _apt (which needs CAP_SETUID/CAP_SETGID nkt's unit does not
// grant, and a writable /var/lib/apt the unit does not expose). Neither is
// something the daemon's own hardening should have ever constrained — that
// hardening exists to limit what a compromised *nkt process* could do on
// its own, not to limit an operator who has already authenticated as
// admin and explicitly asked for a root shell or a package upgrade.
//
// systemd-run --pty asks PID 1 — which is not itself sandboxed — to start
// a brand-new, separately-sandboxed transient unit for just this one
// command. Sandboxing directives are a property of a unit's own cgroup and
// exec context, not something inherited through that D-Bus request, so
// the new unit starts with systemd's normal (unrestricted) defaults; the
// -p overrides below just make that explicit rather than relying on
// nkt's own unit never changing its defaults in a way that would
// otherwise leak through. This is the same trick "systemd-run --pty bash"
// is commonly used for: escaping a hardened unit's sandbox for one
// interactive command, from inside that very unit.
func unrestrictedCommand(env map[string]string, argv ...string) *exec.Cmd {
	if usingSystemdSandbox() {
		return exec.Command("systemd-run", systemdRunArgs(env, argv...)...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

// systemdRunArgs builds the argv systemd-run needs to run argv in a fresh,
// unrestricted transient unit — split out from unrestrictedCommand as a
// pure function so the flags themselves are unit-testable without an
// actual systemd system to run them against.
func systemdRunArgs(env map[string]string, argv ...string) []string {
	args := []string{"--pty", "--collect", "--quiet",
		"-p", "ProtectSystem=no",
		"-p", "ProtectHome=no",
		"-p", "PrivateTmp=no",
		"-p", "NoNewPrivileges=no",
		"-p", "RestrictSUIDSGID=no",
		"-p", "RestrictNamespaces=no",
		"-p", "CapabilityBoundingSet=~",
	}
	for k, v := range env {
		args = append(args, "--setenv="+k+"="+v)
	}
	args = append(args, "--")
	return append(args, argv...)
}

// usingSystemdSandbox reports whether this nkt process is itself running
// as a systemd unit — INVOCATION_ID is set by systemd for every unit it
// starts and never by a plain shell/test invocation, which is exactly the
// distinction that matters here: running outside of any systemd unit (a
// dev machine, the test suite, a host where nkt was started by hand) has
// no sandbox to escape from in the first place, and forcing every
// terminal/update session through systemd-run there would just break
// something that currently works for no benefit.
//
// Also requires systemdControlSocket: a real, observed failure mode is a
// host that runs systemd as PID 1 (so INVOCATION_ID is set) but never
// installed or started dbus — some minimal cloud VPS images do this.
// systemd-run itself still gets exec'd there, but immediately fails with
// "Failed to connect to bus: No such file or directory" printed as the
// *terminal's own output*, not a clean API error — from inside the PTY it
// just started, nothing here catches or explains it. Checking first lets
// unrestrictedCommand fall back to the plain (still unit-sandboxed) shell
// instead, which at least works.
func usingSystemdSandbox() bool {
	if os.Getenv("INVOCATION_ID") == "" {
		return false
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false
	}
	return systemdControlSocket()
}

// systemdControlSocketPaths are the sockets that make systemd-run able to
// reach the manager — a var, not a const, so tests can point it at a temp
// directory instead of needing root access to the real /run paths.
// /run/systemd/private lets systemd-run talk to PID 1 directly, no
// dbus-daemon involved at all — systemd's own designed fallback for
// exactly this reason (early boot, minimal containers); /run/dbus/
// system_bus_socket is the classic D-Bus route, relevant only on setups
// where the direct socket isn't exposed.
var systemdControlSocketPaths = []string{"/run/systemd/private", "/run/dbus/system_bus_socket"}

func systemdControlSocket() bool {
	for _, p := range systemdControlSocketPaths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// unrestrictedBackgroundCommand is unrestrictedCommand's non-interactive
// counterpart: no --pty, no controlling terminal expected. For a
// fire-and-forget command (see handleSelfUpdate) that is meant to outlive
// the request handling it — including, deliberately, outliving *this very
// process* when the command itself restarts the unit this process belongs
// to — rather than an interactive session tied to a WebSocket someone is
// watching live.
func unrestrictedBackgroundCommand(argv ...string) *exec.Cmd {
	if usingSystemdSandbox() {
		return exec.Command("systemd-run", systemdRunBackgroundArgs(argv...)...)
	}
	return exec.Command(argv[0], argv[1:]...)
}

// systemdRunBackgroundArgs mirrors systemdRunArgs' sandbox-escape overrides
// without --pty — split out the same way, for the same reason: a pure
// function the flags themselves can be unit-tested against without an
// actual systemd system to run them on.
func systemdRunBackgroundArgs(argv ...string) []string {
	args := []string{"--collect", "--quiet",
		"-p", "ProtectSystem=no",
		"-p", "ProtectHome=no",
		"-p", "PrivateTmp=no",
		"-p", "NoNewPrivileges=no",
		"-p", "RestrictSUIDSGID=no",
		"-p", "RestrictNamespaces=no",
		"-p", "CapabilityBoundingSet=~",
		"--",
	}
	return append(args, argv...)
}

// ptyControl is the shape of a WebSocket TEXT frame — raw keystrokes and
// process output travel as BINARY frames instead, so a text/binary split
// (rather than an envelope around every byte) is enough to tell the one
// control message (resize) apart from actual terminal traffic.
type ptyControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// runPTYSession accepts a WebSocket upgrade, starts cmd under a PTY and
// bridges the two until either side closes — shared by handleTerminalWS (an
// arbitrary login shell) and handleUpdatesWS (a fixed apt-get command).
// Both need the same framing, idle timeout, audit-log shape and cleanup;
// only what gets exec'd and what it's logged as differ, which is exactly
// what the caller already decided before reaching here.
//
// auditTarget is what shows up in the audit log next to
// auditAction+".start"/".end" (e.g. the shell path, or "apt-get upgrade") —
// callers must have already applied whatever gating is specific to them
// (NKT_TERMINAL_ENABLED, ModeFixtures, apt-get present) before calling
// this; it does not repeat those checks.
func (s *Server) runPTYSession(w http.ResponseWriter, r *http.Request, cmd *exec.Cmd, auditAction, auditTarget string, idleTimeout time.Duration) {
	// Accept's default (no AcceptOptions) already rejects cross-origin
	// upgrade requests — exactly what a feature this powerful needs, with
	// nothing further to configure.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the response.
	}
	defer conn.CloseNow()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "не удалось запустить команду: "+err.Error())
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	user := auth.Username(r.Context())
	started := time.Now()
	s.db.Audit(r.Context(), user, auditAction+".start", auditTarget, "ok", nil)
	defer func() {
		// context.Background(), not r.Context(): the request context is
		// already on its way out by the time this runs, and the audit
		// entry for a session ending must not be lost just because the
		// HTTP request that started it is too.
		s.db.Audit(context.Background(), user, auditAction+".end", auditTarget, "ok",
			map[string]any{"duration_s": int(time.Since(started).Seconds())})
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Idle timeout: the safety net for a tab left open and forgotten — the
	// process otherwise runs until someone closes it by hand. Any traffic
	// in either direction resets it.
	var resetIdle func()
	if idleTimeout > 0 {
		timer := time.AfterFunc(idleTimeout, cancel)
		resetIdle = func() { timer.Reset(idleTimeout) }
	} else {
		resetIdle = func() {}
	}

	// ptmx -> WebSocket: process output. Exits once ptmx is closed by the
	// cleanup above (a blocking os-level Read is not context-aware, so
	// closing the file descriptor — not cancelling ctx — is what actually
	// unblocks it when the session ends from the other side).
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				resetIdle()
				if wErr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); wErr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				cancel()
				return
			}
		}
	}()

	// WebSocket -> ptmx: keystrokes (binary frames) and control messages
	// (text frames, currently just resize).
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		resetIdle()
		switch msgType {
		case websocket.MessageBinary:
			_, _ = ptmx.Write(data)
		case websocket.MessageText:
			var ctrl ptyControl
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(ctrl.Cols), Rows: uint16(ctrl.Rows)})
			}
		}
	}
}
