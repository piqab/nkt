package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/auth"
	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/msgs"
)

// vncPort is the fixed RFB port handleVNCStart's x11vnc always listens on
// — a single well-known port (VNC's own traditional default), not
// whatever x11vnc happens to auto-pick, so handleVNCStatus can report it
// without having to parse x11vnc's own startup log for the actual bound
// port.
const vncPort = 5900

// vncControlTimeout bounds every command below — local process control
// (pgrep/pkill, and x11vnc's own -bg startup handshake), not a network
// call, so this is a backstop against something hanging, not a realistic
// budget.
const vncControlTimeout = 10 * time.Second

// handleVNCStatus reports whether x11vnc is installed and, separately,
// currently running — the Terminal page uses this to decide which of
// "Установить"/"Запустить"/"Остановить" to show. One session per host, so
// unlike the terminal/tmux/update sessions there is no in-memory job to
// track: running is always re-derived from a live process check, correct
// across nkt restarts without any extra bookkeeping.
func (s *Server) handleVNCStatus(w http.ResponseWriter, r *http.Request) {
	installed := collect.Which(r.Context(), s.scanner.Collector(), "x11vnc")
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": installed,
		"running":   installed && vncRunning(r.Context()),
		"port":      vncPort,
	})
}

// vncRunning checks for a live x11vnc process by exact name. Always as
// root (not TerminalUser): root can see and signal a process regardless
// of which account handleVNCStart happened to run it as, so there is
// nothing to track separately to make this or vncStop work.
func vncRunning(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, vncControlTimeout)
	defer cancel()
	return unrestrictedQuietCommand(ctx, nil, "pgrep", "-x", "x11vnc").Run() == nil
}

// handleVNCInstallWS runs `apt-get install -y x11vnc`, streamed live —
// same runUpdateSession mechanism as the dbus/tmux/ufw/firewalld install
// buttons, and deliberately not gated on TerminalEnabled the way
// handleVNCStart is below: installing the package is no more consequential
// than any other apt-get install this app already offers, the actual
// screen/input exposure only happens once someone presses "Запустить".
func (s *Server) handleVNCInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	if collect.Which(r.Context(), s.scanner.Collector(), "x11vnc") {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "pkgInstall.x11vncAlreadyInstalled"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "bash", "-c", "apt-get update && apt-get install -y x11vnc")
	}
	s.runUpdateSession(w, r, "vnc-install", buildCmd, "system.install_x11vnc", "apt-get install x11vnc", s.cfg.TerminalIdleTimeout)
}

// handleVNCInstallStatus mirrors handleTmuxInstallStatus for the
// "vnc-install" session key.
func (s *Server) handleVNCInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("vnc-install")
	writeSessionStatus(w, active, finished, exitCode)
}

// vncStartResponse.Password travels exactly once, at the moment x11vnc
// actually starts — like the hub's own bootstrap admin password, nkt never
// stores it anywhere it could show again; losing it means Остановить then
// Запустить again (a fresh password) is the only way back in.
type vncStartResponse struct {
	Password string `json:"password"`
	Port     int    `json:"port"`
}

// handleVNCStart launches x11vnc against this host's existing X11 desktop
// (display :0 — there is no virtual display of its own to set up, and one
// real desktop is what this is for) with a freshly generated password, so
// the operator connects with their own VNC client afterwards. Gated on
// TerminalEnabled, the same extra opt-in handleTerminalWS itself requires
// beyond RequireAdmin+AllowMutations: full desktop screen/input access is
// at least as consequential as a root shell, not a lesser action like the
// install button above.
//
// Runs as VNCUser when configured, else TerminalUser, else root — see
// config.Config.VNCUser's own comment on why this needs a setting separate
// from TerminalUser: X11 access control is per-account (via
// ~/.Xauthority, which resolveUserEnv already points HOME at for the
// right user), so x11vnc has to run as whichever account actually owns
// the running desktop session it's attaching to, or it simply won't be
// able to open the display at all — confirmed directly: running it by
// hand as root fails silently (no diagnostic text anywhere x11vnc's own
// -bg/-o redirection reaches), running it as the desktop's own user works.
func (s *Server) handleVNCStart(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TerminalEnabled {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "vnc.disabled"))
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "vnc.fixturesDisabled"))
		return
	}
	if !collect.Which(r.Context(), s.scanner.Collector(), "x11vnc") {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "vnc.notInstalled"))
		return
	}
	if vncRunning(r.Context()) {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "vnc.alreadyRunning"))
		return
	}

	vncUser := s.cfg.VNCUser
	if vncUser == "" {
		vncUser = s.cfg.TerminalUser
	}

	password, err := auth.GeneratePassword()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// nkt itself normally runs as root — os.CreateTemp below therefore
	// creates both files root-owned, 0600. When x11vnc is about to run as
	// vncUser instead (the branch below, and the reason this matters: see
	// this function's own doc comment on why it has to), that account has
	// no access to a root-owned 0600 file at all: not to read the
	// password, not to write its own log. Without this, x11vnc fails
	// immediately trying to open the password file — a "Permission denied"
	// that used to be swallowed just like the empty-output case below,
	// surfacing as a bare "exit status 1" with no explanation.
	uid, gid := -1, -1
	if vncUser != "" {
		u32, g32, _, err := resolveUserEnv(nil, vncUser)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		uid, gid = int(u32), int(g32)
	}

	// A file, not -passwd on the command line — the latter would sit in
	// plain sight to any other account on the host via `ps aux` or
	// /proc/<pid>/cmdline. Removed immediately after x11vnc has started:
	// it reads the file once at startup and keeps the password in memory
	// from then on, so nothing needs it to stick around on disk.
	pwFile, err := os.CreateTemp("", "nkt-x11vnc-passwd-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pwPath := pwFile.Name()
	defer os.Remove(pwPath)
	if _, err := pwFile.WriteString(password + "\n"); err != nil {
		pwFile.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := pwFile.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Chmod(pwPath, 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if uid != -1 {
		if err := os.Chown(pwPath, uid, gid); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// x11vnc's own -bg redirects its stderr/stdout to -o's logfile the
	// moment it backgrounds (its man page is explicit: "Messages to stderr
	// are lost unless -o is used") — a failure right around that handoff
	// often leaves the invoking process's own captured output (below)
	// empty or just a bare exit status, with the actual reason ending up
	// in this logfile instead. A fresh temp path per attempt, not a fixed
	// one, so a failed run's diagnostics can never be confused with a
	// stale one from an earlier attempt.
	logFile, err := os.CreateTemp("", "nkt-x11vnc-log-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logPath := logFile.Name()
	logFile.Close()
	defer os.Remove(logPath)
	if uid != -1 {
		if err := os.Chown(logPath, uid, gid); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	argv := []string{
		"x11vnc",
		"-display", ":0",
		"-rfbport", strconv.Itoa(vncPort),
		"-passwdfile", pwPath,
		"-bg",
		"-forever",
		"-noxdamage",
		"-o", logPath,
	}

	ctx, cancel := context.WithTimeout(r.Context(), vncControlTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if vncUser != "" {
		cmd, err = unrestrictedQuietCommandAsUser(ctx, nil, vncUser, argv...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		cmd = unrestrictedQuietCommand(ctx, nil, argv...)
	}
	// -bg only returns once x11vnc has finished its own startup handshake
	// (opened the display, bound the port) and actually daemonized — the
	// same reason ensureTmuxSession's `tmux new-session -d` is safe to
	// treat as synchronous, see startWSKeepalive's neighbours in
	// pty_session.go for the systemd KillMode=process fix this depends on
	// to let the now-detached x11vnc process outlive this quiet command.
	out, err := cmd.CombinedOutput()
	if err != nil {
		// out alone is frequently empty or just a bare "exit status 1" —
		// see logPath's own comment above: x11vnc's real diagnostic text
		// usually goes there instead once -bg hands off. Best-effort: a
		// failure to read it (permission, vncUser owns it not us,
		// x11vnc never got far enough to create it) just means detail
		// falls through to whatever out/err already had.
		detail := strings.TrimSpace(string(out))
		if logContent, logErr := os.ReadFile(logPath); logErr == nil {
			if logText := strings.TrimSpace(string(logContent)); logText != "" && logText != detail {
				if detail != "" {
					detail += "\n" + logText
				} else {
					detail = logText
				}
			}
		}
		// Both out and the logfile can still be empty — the command never
		// actually ran at all (x11vnc missing from PATH inside whatever
		// sandbox/user context it was just invoked under: systemd-run/
		// nsenter/TerminalUser can all resolve PATH differently than the
		// scanner's own collect.Which check above), or vncControlTimeout
		// firing before anything was written anywhere. Falling back to
		// err's own text there is the difference between a message and a
		// bare trailing colon with nothing after it.
		if detail == "" {
			detail = err.Error()
		}
		writeError(w, http.StatusInternalServerError, msgs.T(msgs.LangFromRequest(r), "vnc.startFailed", detail))
		return
	}

	s.db.Audit(r.Context(), auth.Username(r.Context()), "vnc.start", "x11vnc", "ok", map[string]any{"port": vncPort})
	writeJSON(w, http.StatusOK, vncStartResponse{Password: password, Port: vncPort})
}

// handleVNCStop kills the running x11vnc process by exact name. No
// PID+command verification (unlike ServiceManager.KillProcess, which has
// to because it acts on a PID some other page merely observed and might
// be stale) — this handler runs its own pgrep/pkill back to back, on a
// process name specific enough (x11vnc, not a generic "the process at
// this PID") that a PID-reuse race in the few milliseconds between them
// resulting in something OTHER than x11vnc is not a realistic concern.
func (s *Server) handleVNCStop(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), vncControlTimeout)
	defer cancel()
	if err := unrestrictedQuietCommand(ctx, nil, "pkill", "-x", "x11vnc").Run(); err != nil {
		writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "vnc.notRunning"))
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "vnc.stop", "x11vnc", "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
