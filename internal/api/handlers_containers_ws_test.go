package api

import (
	"context"
	"github.com/coder/websocket"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// Номер экрана VNC → адрес подключения.
func TestParseVNCDisplay(t *testing.T) {
	for in, want := range map[string]string{"127.0.0.1:0\n": "127.0.0.1:5900", ":1": "127.0.0.1:5901", "[::1]:2": "[::1]:5902", "0.0.0.0:3": "127.0.0.1:5903"} {
		if got, ok := parseVNCDisplay(in); !ok || got != want {
			t.Errorf("%q → %q, %v", in, got, ok)
		}
	}
	if _, ok := parseVNCDisplay("error: no graphics"); ok {
		t.Error("мусор принят")
	}
}

// Мост WebSocket ↔ TCP: байты ходят в обе стороны без искажений.
func TestProxyWSToTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("RFB 003.008\n"))
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		_, _ = c.Write(append([]byte("echo:"), buf[:n]...))
	}()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyWSToTCP(w, r, ln.Addr().String(), time.Minute)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_, msg, err := conn.Read(ctx)
	if err != nil || string(msg) != "RFB 003.008\n" {
		t.Fatalf("приветствие: %q %v", msg, err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	_, msg, err = conn.Read(ctx)
	if err != nil || string(msg) != "echo:\x01\x02\x03" {
		t.Fatalf("эхо: %q %v", msg, err)
	}
}
