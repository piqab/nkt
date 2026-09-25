package api

import (
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/guestcred"
	"github.com/piqab/nkt/internal/msgs"
)

// Операции установки и обновления (apt, snap/flatpak, движки, btop…)
// исторически шли живым выводом через runUpdateSession. С ?job=1 та же
// операция становится фоновым заданием «выполнить команды»: окно журнала,
// «Задания», отмена и повтор — на хосте и через хаб. Команду обработчик
// собирает прежним buildCmd; её исходные argv и окружение запоминает
// unrestrictedCommand — сама собранная команда не запускается.

type cmdOrigin struct {
	env  map[string]string
	argv []string
}

var (
	originsMu sync.Mutex
	origins   = map[*exec.Cmd]cmdOrigin{}
)

func rememberOrigin(cmd *exec.Cmd, env map[string]string, argv []string) {
	originsMu.Lock()
	defer originsMu.Unlock()
	// Держатся только до разбора: сессия, запущенная как обычно, свою
	// запись не забирает — чистим старое, чтобы карта не росла.
	if len(origins) > 64 {
		origins = map[*exec.Cmd]cmdOrigin{}
	}
	origins[cmd] = cmdOrigin{env: env, argv: append([]string{}, argv...)}
}

func takeOrigin(cmd *exec.Cmd) (cmdOrigin, bool) {
	originsMu.Lock()
	defer originsMu.Unlock()
	o, ok := origins[cmd]
	delete(origins, cmd)
	return o, ok
}

// originScript — команда сценарием bash: окружение export-ами, «bash -c
// S» — самим S. Сценарий идёт файлом: systemd-run подставляет $… в
// аргументах. apt-get получает Status-Fd — проценты для полосы задания, а
// обновление системы — -y и сохранение своих конфигов (спросить в
// задании некого).
func originScript(o cmdOrigin) string {
	var b strings.Builder
	keys := make([]string, 0, len(o.env))
	for k := range o.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("export " + k + "=" + guestcred.ShellQuote(o.env[k]) + "\n")
	}
	var body string
	if len(o.argv) == 3 && (o.argv[0] == "bash" || o.argv[0] == "sh") && o.argv[1] == "-c" {
		body = o.argv[2]
	} else {
		argv := o.argv
		if len(argv) > 0 && argv[0] == "apt-get" {
			argv = append([]string{"apt-get", "-o", "APT::Status-Fd=1"}, argv[1:]...)
		}
		q := make([]string, len(argv))
		for i, a := range argv {
			q[i] = guestcred.ShellQuote(a)
		}
		body = strings.Join(q, " ")
	}
	if strings.Contains(body, "dist-upgrade") && !strings.Contains(body, "dist-upgrade -y") {
		if _, ok := o.env["DEBIAN_FRONTEND"]; !ok {
			b.WriteString("export DEBIAN_FRONTEND=noninteractive\n")
		}
		body = strings.ReplaceAll(body, "apt-get dist-upgrade", "apt-get dist-upgrade -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold")
	}
	body = strings.ReplaceAll(body, "apt-get ", "apt-get -o APT::Status-Fd=1 ")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}

// updateJobTitle — название задания по действию аудита.
func updateJobTitle(action, target string) (string, []any) {
	switch {
	case action == "packages.upgrade":
		return "cmdjob.titleUpgrade", nil
	case strings.HasPrefix(action, "packages.remove"):
		return "cmdjob.titleRemove", []any{target}
	case strings.HasPrefix(action, "packages.install"), strings.HasPrefix(action, "system.install_"),
		strings.HasPrefix(action, "firewall.install_"), action == "services.install":
		return "cmdjob.titleInstall", []any{target}
	case strings.HasPrefix(action, "sandbox_pkg."):
		return "cmdjob.titleSandbox", []any{strings.TrimPrefix(action, "sandbox_pkg."), strings.TrimSpace(target)}
	case strings.HasPrefix(action, "container."):
		return "cmdjob.titleContainer", []any{strings.TrimPrefix(action, "container."), target}
	case action == "lxd.imageCopy":
		return "cmdjob.titleLXDImage", []any{target}
	}
	return "cmdjob.titleGeneric", []any{action, target}
}

// startUpdateJob — вместо живой сессии: задание из той же команды.
func (s *Server) startUpdateJob(w http.ResponseWriter, r *http.Request, key string, buildCmd func() *exec.Cmd, auditAction, auditTarget string) {
	o, ok := takeOrigin(buildCmd())
	if !ok {
		// Команда собрана не через unrestrictedCommand (установка dbus идёт
		// через nsenter) — заданием её не выполнить; интерфейс откатится
		// на живой вывод.
		writeError(w, http.StatusNotImplemented, msgs.Tc(r.Context(), "cmdjob.notSupported"))
		return
	}
	title, args := updateJobTitle(auditAction, auditTarget)
	s.startCmdJob(w, r, title, args, "session:"+key,
		cmdjob.Params{Commands: []cmdjob.Command{{Script: originScript(o), StepKey: title, StepArgs: args}}, Refresh: true},
		auditAction, auditTarget)
}
