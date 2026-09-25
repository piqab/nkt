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

// Установка через snap: snapd ставится, если его нет, ждётся его
// готовность; LXD после установки инициализируется.
func TestSnapInstallScript(t *testing.T) {
	sc := snapInstallScript("lxd")
	for _, want := range []string{"command -v snap", "apt-get install -y snapd", "snap wait system seed.loaded", "snap install lxd", "lxd init --auto"} {
		if !strings.Contains(sc, want) {
			t.Errorf("нет %q:\n%s", want, sc)
		}
	}
	if strings.Contains(snapInstallScript("other"), "lxd init") {
		t.Error("lxd init только для lxd")
	}
}

// Сценарии, уходящие одной строкой «bash -c» через systemd-run, не должны
// содержать ${…}: systemd подставляет такие выражения в аргументах
// ExecStart сам, и до bash они доходят пустыми (так ломался бэкап).
func TestInlineScriptsHaveNoBraceExpansion(t *testing.T) {
	for name, sc := range map[string]string{
		"containerRun": containerRunScript("docker", "start", "web"),
		"snapInstall":  snapInstallScript("lxd"),
	} {
		if strings.Contains(sc, "${") {
			t.Errorf("%s содержит ${…} — systemd-run его съест", name)
		}
	}
}

// Команды консоли: по виду объекта, имя и пользователь проверены,
// без ${…}.
func TestConsoleArgv(t *testing.T) {
	for kind, want := range map[string]string{
		"docker": "docker exec -it -e TERM=xterm-256color -u app web sh -c",
		"podman": "podman exec -it -e TERM=xterm-256color -u app web sh -c",
		"vm":     "virsh console web --force",
	} {
		argv, ok := consoleArgv(kind, "web", map[bool]string{true: "app", false: ""}[kind != "vm"])
		if !ok || !strings.HasPrefix(strings.Join(argv, " "), want) {
			t.Errorf("%s: %v", kind, argv)
		}
		if strings.Contains(strings.Join(argv, " "), "${") {
			t.Errorf("%s: ${…} в команде", kind)
		}
	}
	if argv, ok := consoleArgv("lxd", "c1", ""); !ok || !strings.Contains(strings.Join(argv, " "), "exec c1 --env TERM=xterm-256color -- sh -c") {
		t.Errorf("lxd: %v", argv)
	}
	for _, bad := range [][3]string{{"docker", "a;b", ""}, {"docker", "web", "root;x"}, {"kvm", "web", ""}} {
		if _, ok := consoleArgv(bad[0], bad[1], bad[2]); ok {
			t.Errorf("принято %v", bad)
		}
	}
}
