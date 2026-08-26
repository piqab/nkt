package api

import (
	"net/http"
	"os/exec"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/msgs"
)

// handleUFWInstallWS runs `apt-get install -y ufw` on this host, streamed
// live over a PTY/WebSocket bridge — same runUpdateSession mechanism as
// package updates (handleUpdatesWS), under its own "ufw-install" key so it
// can never collide with an in-progress apt upgrade. -y is safe here
// (unlike the upgrade flow): installing one already-known package from the
// distro's own repos does not carry the "which services get restarted, is
// this really what I want on this system" judgment call a full upgrade
// does, so there is nothing worth pausing for a prompt over.
func (s *Server) handleUFWInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	c := s.scanner.Collector()
	if !collect.Which(r.Context(), c, "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	if collect.Which(r.Context(), c, "ufw") {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "pkgInstall.ufwAlreadyInstalled"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "apt-get", "install", "-y", "ufw")
	}
	s.runUpdateSession(w, r, "ufw-install", buildCmd, "firewall.install_ufw", "apt-get install ufw", s.cfg.TerminalIdleTimeout)
}

// handleUFWInstallStatus mirrors handleUpdatesStatus for the "ufw-install"
// session key — the Firewall page polls this independently of whether the
// install dialog is open, so a freshly loaded page (or one reopened after a
// disconnect) can show "установка выполняется — открыть" instead of racing
// a second apt-get install against one already running.
func (s *Server) handleUFWInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("ufw-install")
	writeSessionStatus(w, active, finished, exitCode)
}

// handleFirewalldInstallWS is handleUFWInstallWS's twin for firewalld —
// same apt-only assumption (firewalld ships in Debian/Ubuntu's own repos
// too, just less often pre-installed there than on RHEL-family distros,
// where it's the default and this button would rarely be needed at all).
func (s *Server) handleFirewalldInstallWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	c := s.scanner.Collector()
	if !collect.Which(r.Context(), c, "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	if collect.Which(r.Context(), c, "firewall-cmd") {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "pkgInstall.firewalldAlreadyInstalled"))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		return unrestrictedCommand(env, "apt-get", "install", "-y", "firewalld")
	}
	s.runUpdateSession(w, r, "firewalld-install", buildCmd, "firewall.install_firewalld", "apt-get install firewalld",
		s.cfg.TerminalIdleTimeout)
}

func (s *Server) handleFirewalldInstallStatus(w http.ResponseWriter, r *http.Request) {
	active, finished, exitCode := s.sessionStatus("firewalld-install")
	writeSessionStatus(w, active, finished, exitCode)
}

// sessionStatus reads a keyed session's own outcome — shared by every
// "install X" status endpoint (ufw, firewalld, ...) instead of each
// reimplementing the same lock/lookup/outcome dance handleUpdatesStatus
// established first.
func (s *Server) sessionStatus(key string) (active, finished bool, exitCode int) {
	s.sessionsMu.Lock()
	sess := s.sessions[key]
	s.sessionsMu.Unlock()
	if sess == nil {
		return false, false, -1
	}
	done, code := sess.outcome()
	return !done, done, code
}

func writeSessionStatus(w http.ResponseWriter, active, finished bool, exitCode int) {
	writeJSON(w, http.StatusOK, map[string]any{
		"active":    active,
		"finished":  finished,
		"exit_code": exitCode,
		"succeeded": finished && exitCode == 0,
	})
}
