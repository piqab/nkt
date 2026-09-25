package api

import (
	"net/http"
	"os/exec"
	"regexp"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
)

// Консоль внутри контейнера или машины — тот же PTY-мост, что у
// веб-терминала, и те же ворота: NKT_TERMINAL_ENABLED, не fixtures, а
// при заданном TerminalUser (хост под хабом) — от его имени: без членства
// в группах docker/lxd/libvirt войти не выйдет, и это правильно.
//
//	docker/podman — ENGINE exec -it [-u USER] NAME, bash или sh;
//	lxd           — lxc exec NAME -- bash или sh;
//	vm            — virsh console NAME --force (последовательная консоль
//	                гостя; выход — Ctrl+]).
//
// Команды — без ${…}: строка уходит через systemd-run, который такие
// выражения подставляет сам.

var consoleUserRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}$`)

const shellPick = `if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi`

// consoleArgv — команда консоли по виду объекта.
func consoleArgv(kind, name, user string) ([]string, bool) {
	if !containerNameRe.MatchString(name) || (user != "" && !consoleUserRe.MatchString(user)) {
		return nil, false
	}
	switch kind {
	case "docker", "podman":
		argv := []string{kind, "exec", "-it", "-e", "TERM=xterm-256color"}
		if user != "" {
			argv = append(argv, "-u", user)
		}
		return append(argv, name, "sh", "-c", shellPick), true
	case "lxd":
		// systemd-run даёт свой короткий PATH — lxc из snap по полному пути.
		lxc := "lxc"
		if p, err := exec.LookPath("lxc"); err == nil {
			lxc = p
		}
		return []string{lxc, "exec", name, "--env", "TERM=xterm-256color", "--", "sh", "-c", shellPick}, true
	case "vm":
		return []string{"virsh", "console", name, "--force"}, true
	}
	return nil, false
}

// handleConsoleWS — GET /console/ws?kind=docker|podman|lxd|vm&name=…&user=….
func (s *Server) handleConsoleWS(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TerminalEnabled {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.disabled"))
		return
	}
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return
	}
	q := r.URL.Query()
	kind, name := q.Get("kind"), q.Get("name")
	argv, ok := consoleArgv(kind, name, q.Get("user"))
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "api.consoleBadTarget", kind, name))
		return
	}
	env := map[string]string{"TERM": "xterm-256color"}
	var cmd *exec.Cmd
	if s.cfg.TerminalUser != "" {
		c, err := unrestrictedCommandAsUser(env, s.cfg.TerminalUser, argv...)
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		cmd = c
	} else {
		cmd = unrestrictedCommand(env, argv...)
	}
	s.runPTYSession(w, r, cmd, "console", kind+":"+name, s.cfg.TerminalIdleTimeout)
}
