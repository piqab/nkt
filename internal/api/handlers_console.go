package api

import (
	"net/http"
	"regexp"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
)

// Консоль внутри контейнера или машины — тот же PTY-мост, что у
// веб-терминала, и те же ворота: администратор, NKT_TERMINAL_ENABLED, не
// fixtures. Запуск — от root, как у логов и остальных действий с
// контейнерами, даже при заданном TerminalUser (хост под хабом): от имени
// пользователя SSH lxc упирался в сокет LXD, virsh — в личный
// qemu:///session, а членство в группах lxd/docker/libvirt всё равно
// равно root на хосте. Понижение прав остаётся у терминала самого хоста.
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

// consoleUserScript — docker/podman exec от имени пользователя: имя
// пользователя приходит переменной окружения NKT_CONSOLE_USER, программа,
// контейнер и выбор оболочки — позиционными аргументами. Сам сценарий —
// константа: ни одно значение из запроса не становится текстом команды.
// "$NKT_CONSOLE_USER" внутри слова systemd-run не подставляет (только
// ${…}), это делает sh.
const consoleUserScript = `exec "$0" exec -it -e TERM=xterm-256color -u "$NKT_CONSOLE_USER" "$1" sh -c "$2"`

// consoleArgv — команда консоли по виду объекта. name — уже имя из
// инвентаря (см. consoleTarget), withUser — войти от имени пользователя
// (оно передаётся окружением, не аргументом).
func consoleArgv(kind, name string, withUser bool) ([]string, bool) {
	if !containerNameRe.MatchString(name) {
		return nil, false
	}
	switch kind {
	case "docker", "podman":
		engine := "docker"
		if kind == "podman" {
			engine = "podman"
		}
		if withUser {
			return []string{"sh", "-c", consoleUserScript, engine, name, shellPick}, true
		}
		return []string{engine, "exec", "-it", "-e", "TERM=xterm-256color", name, "sh", "-c", shellPick}, true
	case "lxd":
		// systemd-run даёт свой короткий PATH — lxc из snap по полному пути.
		return []string{hostTool("lxc"), "exec", name, "--env", "TERM=xterm-256color", "--", "sh", "-c", shellPick}, true
	case "vm":
		return []string{"virsh", "-c", "qemu:///system", "console", name, "--force"}, true
	}
	return nil, false
}

// consoleTarget — имя объекта из снимка инвентаря, совпадающее с
// запрошенным: в команду идёт строка из инвентаря, а не из запроса.
func (s *Server) consoleTarget(r *http.Request, kind, name string) (string, bool) {
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil || snap == nil {
		return "", false
	}
	var names []string
	switch kind {
	case "docker":
		for _, c := range snap.Container {
			names = append(names, c.Name)
		}
	case "podman":
		for _, c := range snap.Podman {
			names = append(names, c.Name)
		}
	case "lxd":
		for _, in := range snap.LXD {
			names = append(names, in.Name)
		}
	case "vm":
		for _, vm := range snap.VMs {
			names = append(names, vm.Name)
		}
	}
	return trustedName(name, names)
}

// trustedName — элемент candidates, равный input: дальше идёт он, а не
// ввод пользователя.
func trustedName(input string, candidates []string) (string, bool) {
	for _, c := range candidates {
		if c == input {
			return c, true
		}
	}
	return "", false
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
	kind, name, user := q.Get("kind"), q.Get("name"), q.Get("user")
	target, found := s.consoleTarget(r, kind, name)
	if user != "" && !consoleUserRe.MatchString(user) {
		found = false
	}
	argv, ok := consoleArgv(kind, target, user != "")
	if !found || !ok {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "api.consoleBadTarget", kind, name))
		return
	}
	env := map[string]string{"TERM": "xterm-256color"}
	if user != "" {
		env["NKT_CONSOLE_USER"] = user
	}
	cmd := unrestrictedCommand(env, argv...)
	s.runPTYSession(w, r, cmd, "console", kind+":"+name, s.cfg.TerminalIdleTimeout)
}
