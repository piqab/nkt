package api

import (
	"net/http"
	"os"
	"os/exec"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/msgs"
)

// handleDbusStatus reports whether this host's terminal/package-update/
// self-update sessions are currently stuck on the sandboxed fallback
// because neither escape hatch (systemd-run, which needs D-Bus; nsenter,
// which needs CAP_SYS_ADMIN — see pty_session.go) can reach outside the
// unit's own ProtectSystem=strict mount namespace right now.
//
// Needed is false — nothing to show — when nkt isn't running as a systemd
// unit at all (no INVOCATION_ID): there is no sandbox to escape in the
// first place, so a missing D-Bus is irrelevant there. CanInstall is
// false when needed is true but nsenter itself isn't usable either (no
// CAP_SYS_ADMIN on an older-deployed unit, or nsenter genuinely absent) —
// the frontend uses this to show "поставьте вручную" instead of a button
// it knows would just fail.
func (s *Server) handleDbusStatus(w http.ResponseWriter, r *http.Request) {
	sandboxed := os.Getenv("INVOCATION_ID") != ""
	// systemdRunReachable actually invokes systemd-run rather than
	// inferring reachability from which control-socket paths merely
	// accept a raw connect — see its own doc comment (pty_session.go) for
	// why that distinction matters: a stale-but-connectable socket used to
	// read as "available" here right up until the terminal itself failed
	// with "Failed to connect to bus".
	needed := sandboxed && !systemdRunReachable()
	writeJSON(w, http.StatusOK, map[string]any{
		"needed":      needed,
		"can_install": needed && needsNsenterFallback(),
	})
}

// handleDbusInstallWS runs `apt-get install -y dbus && systemctl enable
// --now dbus` on this host, streamed live — same runUpdateSession
// mechanism as the ufw/firewalld install buttons. Refuses outright when
// D-Bus is already reachable (nothing to do) or when nsenter isn't usable
// either (see handleDbusStatus) — the install itself, like every other
// escape-hatch command, goes through unrestrictedCommand, which picks
// nsenter here specifically because systemd-run is what's broken.
func (s *Server) handleDbusInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	if systemdRunReachable() {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "pkgInstall.dbusAlreadyAvailable"))
		return
	}
	if !needsNsenterFallback() {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.dbusManualOnly"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get update && apt-get install -y dbus && systemctl enable --now dbus")
	}
	s.runUpdateSession(w, r, "dbus-install", buildCmd, "system.install_dbus", "apt-get install dbus", s.cfg.TerminalIdleTimeout)
}

// handleDbusInstallStatus mirrors handleUFWInstallStatus for the
// "dbus-install" session key.
func (s *Server) handleDbusInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("dbus-install")
	writeSessionStatus(w, active, finished, exitCode)
}
