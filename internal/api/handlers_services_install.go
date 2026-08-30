package api

import (
	"net/http"
	"os/exec"

	"github.com/go-chi/chi/v5"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/msgs"
	"github.com/althq/netknownsthat/internal/parse"
)

// handleServiceInstallWS installs a known managed service's underlying
// package — the "неактивные" chip on the Services page for one that isn't
// installed at all becomes clickable specifically for this, so an operator
// doesn't have to know or type the actual apt/snap package name (docker's
// own default-repo package is docker.io, not "docker"; LXD ships only as a
// snap upstream — see parse.InstallTarget's own comment on the handful of
// mismatches).
//
// {name} is validated against parse.InstallTarget's own allowlist
// (parse.DefaultServiceSpecs()) before it ever reaches a command — the same
// role ServiceManager.Action's specs[service] lookup already plays for
// start/stop/restart, just for this one extra action.
func (s *Server) handleServiceInstallWS(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	target, ok := parse.InstallTarget(name)
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "pkgInstall.unknownService", name))
		return
	}
	if target.Method == parse.InstallViaAPT && !collect.Which(r.Context(), s.scanner.Collector(), "apt-get") {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.aptGetMissing"))
		return
	}
	if collect.Which(r.Context(), s.scanner.Collector(), target.Binary) {
		writeError(w, http.StatusConflict, msgs.T(msgs.LangFromRequest(r), "pkgInstall.serviceAlreadyInstalled", name))
		return
	}

	buildCmd := func() *exec.Cmd {
		env := map[string]string{"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive"}
		var argv []string
		if target.Method == parse.InstallViaSnap {
			argv = []string{"bash", "-c", "snap install " + target.Package}
		} else {
			argv = []string{"bash", "-c", "apt-get update && apt-get install -y " + target.Package}
		}
		return unrestrictedCommand(env, argv...)
	}
	s.runUpdateSession(w, r, "service-install:"+name, buildCmd, "services.install", target.Package, s.cfg.TerminalIdleTimeout)
}

// handleServiceInstallStatus mirrors handleTmuxInstallStatus for the
// per-service "service-install:<name>" session key.
func (s *Server) handleServiceInstallStatus(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	active, finished, exitCode := s.sessionStatus("service-install:" + name)
	writeSessionStatus(w, active, finished, exitCode)
}
