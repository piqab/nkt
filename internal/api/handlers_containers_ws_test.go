package api

import (
	"strings"
	"testing"
)

// Команда запуска: сам start, пауза, проверка состояния и хвост логов
// при неудаче; имя экранируется кавычками.
func TestContainerRunScriptAndLogsArgs(t *testing.T) {
	sc := containerRunScript("docker", "start", "web-1")
	for _, want := range []string{`docker start "web-1"`, "sleep 4", `docker inspect -f '{{.State.Status}}' "web-1"`, `docker logs --tail 50 "web-1"`, "exit 1"} {
		if !strings.Contains(sc, want) {
			t.Errorf("нет %q в сценарии:\n%s", want, sc)
		}
	}
	if !containerNameRe.MatchString("acme_web.1") || containerNameRe.MatchString("a b") || containerNameRe.MatchString("-x") || containerNameRe.MatchString("x;rm") {
		t.Error("containerNameRe")
	}
	argv := strings.Join(containerLogsArgs("podman", "db", 500, true, true), " ")
	if argv != "podman logs --tail 500 -f -t db" {
		t.Errorf("argv = %q", argv)
	}
	if argv := strings.Join(containerLogsArgs("docker", "db", 200, false, false), " "); argv != "docker logs --tail 200 db" {
		t.Errorf("argv = %q", argv)
	}
}
