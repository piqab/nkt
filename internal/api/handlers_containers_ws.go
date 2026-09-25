package api

import (
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/msgs"
)

// Запуск контейнера и его логи — в стандартном окне выполнения nkt.
//
// Через Docker API «start» отвечает 204 и молчит о том, что контейнер
// через секунду упал: причина остаётся в его логах. Поэтому запуск и
// перезапуск идут командой в WS-сессии: вывод docker, потом проверка
// состояния через несколько секунд и, если контейнер не running, хвост
// его логов — там обычно и написано, почему.

// containerNameRe — имя контейнера/сервиса compose: буквы, цифры,
// точка, дефис, подчёркивание. Всё остальное в командную строку не
// попадает.
var containerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// containerRunScript — команда запуска с проверкой и хвостом логов.
func containerRunScript(engine, action, name string) string {
	return fmt.Sprintf(`set -o pipefail
%[1]s %[2]s %[3]q; rc=$?
if [ $rc -ne 0 ]; then exit $rc; fi
sleep 4
state=$(%[1]s inspect -f '{{.State.Status}}' %[3]q 2>/dev/null || echo unknown)
echo "state: $state"
if [ "$state" != running ]; then
  echo '--- %[1]s logs --tail 50 ---'
  %[1]s logs --tail 50 %[3]q 2>&1
  exit 1
fi`, engine, action, name)
}

// containerLogsArgs — argv для просмотра логов: хвост, слежение, метки.
func containerLogsArgs(engine, name string, tail int, follow, timestamps bool) []string {
	argv := []string{engine, "logs", "--tail", strconv.Itoa(tail)}
	if follow {
		argv = append(argv, "-f")
	}
	if timestamps {
		argv = append(argv, "-t")
	}
	return append(argv, name)
}

func (s *Server) containerEngine(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/podman/") || strings.Contains(r.URL.Path, "/podman/containers/") {
		return "podman"
	}
	return "docker"
}

// handleContainerRunWS — GET /containers/{name}/run/ws?action=start|restart
// (и /podman/containers/…): docker start|restart в сессии обновления, с
// проверкой состояния и логами при неудаче.
func (s *Server) handleContainerRunWS(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	action := r.URL.Query().Get("action")
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "pkgInstall.fixturesDisabled"))
		return
	}
	if !containerNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "control.invalidContainerName", name))
		return
	}
	if action != "start" && action != "restart" {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "control.invalidActionContainer", action))
		return
	}
	engine := s.containerEngine(r)
	// В сценарий идёт имя из инвентаря, а не строка запроса.
	kind := "docker"
	if engine == "podman" {
		kind = "podman"
	}
	trusted, found := s.consoleTarget(r, kind, name)
	if !found {
		writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "control.invalidContainerName", name))
		return
	}
	act := "start"
	if action == "restart" {
		act = "restart"
	}
	buildCmd := func() *exec.Cmd {
		return unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, "bash", "-c", containerRunScript(engine, act, trusted))
	}
	s.runUpdateSession(w, r, "container-run:"+engine+":"+name, buildCmd, "container."+action, name, s.cfg.TerminalIdleTimeout)
}

// handleContainerRunStatus — итог последнего запуска для окна.
func (s *Server) handleContainerRunStatus(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	active, finished, exitCode := s.sessionStatus("container-run:" + s.containerEngine(r) + ":" + name)
	writeSessionStatus(w, active, finished, exitCode)
}

// handleContainerLogsWS — GET /containers/{name}/logs/ws?tail=200&follow=1&timestamps=1:
// docker logs в PTY-сессии — процесс живёт, пока открыто окно.
func (s *Server) handleContainerLogsWS(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "terminal.fixturesDisabled"))
		return
	}
	if !containerNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "control.invalidContainerName", name))
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 || tail > 10000 {
		tail = 200
	}
	follow := r.URL.Query().Get("follow") == "1"
	timestamps := r.URL.Query().Get("timestamps") == "1"
	engine := s.containerEngine(r)
	argv := containerLogsArgs(engine, name, tail, follow, timestamps)
	cmd := unrestrictedCommand(map[string]string{"TERM": "xterm-256color"}, argv...)
	s.runPTYSession(w, r, cmd, "container-logs", engine+":"+name, s.cfg.TerminalIdleTimeout)
}
