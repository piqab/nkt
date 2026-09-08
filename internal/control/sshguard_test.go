package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/config"
)

// listener поднимает поддельный демон, который отвечает заданной строкой и
// закрывает соединение — этого достаточно, чтобы проверить обе ветки: то,
// что баннер sshd распознаётся, и то, что чужая служба на том же порту
// проверку НЕ проходит.
func fakeDaemon(t *testing.T, greeting string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if greeting != "" {
				fmt.Fprint(conn, greeting)
			}
			_ = conn.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestProbeSSHPort(t *testing.T) {
	ctx := context.Background()

	if probe := probeSSHPort(ctx, fakeDaemon(t, "SSH-2.0-OpenSSH_9.6\r\n")); !probe.OK {
		t.Errorf("настоящий баннер sshd не распознан: %+v", probe)
	} else if probe.Banner != "SSH-2.0-OpenSSH_9.6" {
		t.Errorf("banner = %q", probe.Banner)
	}

	// Чужая служба на порту sshd — проверка обязана провалиться: иначе
	// автооткат посчитал бы демона живым и оставил конфигурацию, из-за
	// которой он не поднялся.
	probe := probeSSHPort(ctx, fakeDaemon(t, "220 smtp ready\r\n"))
	if probe.OK {
		t.Error("порт, занятый не sshd, засчитан как рабочий sshd")
	}
	if !strings.Contains(probe.Error, "занят не sshd") {
		t.Errorf("ошибка не объясняет причину: %q", probe.Error)
	}

	// Закрытый порт: слушатель поднят и тут же закрыт, номер занять никто
	// не успевает.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if probe := probeSSHPort(ctx, closedPort); probe.OK {
		t.Error("закрытый порт засчитан как рабочий sshd")
	}
}

func TestSSHDPort(t *testing.T) {
	cfg := &config.Config{SSHRoot: "/etc/ssh"}

	read := func(content string) func(string) ([]byte, error) {
		return func(path string) ([]byte, error) {
			if path != "/etc/ssh/sshd_config" {
				return nil, fmt.Errorf("прочитан не тот файл: %s", path)
			}
			return []byte(content), nil
		}
	}

	cases := map[string]int{
		"#Port 22\nPort 2222\n":          2222,
		"port 2200\n":                    2200, // директивы регистронезависимы
		"#Port 2222\n":                   22,   // закомментировано — значит умолчание
		"":                               22,
		"Port not-a-number\nPort 2201\n": 2201,
		"Port 70000\nPort 2202\n":        2202, // вне диапазона портов
	}
	for content, want := range cases {
		if got := sshdPort(cfg, read(content)); got != want {
			t.Errorf("sshdPort(%q) = %d, want %d", content, got, want)
		}
	}

	// Файла нет вовсе — умолчание, а не паника.
	missing := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if got := sshdPort(cfg, missing); got != 22 {
		t.Errorf("без файла sshdPort = %d, want 22", got)
	}
}

func TestListensPublicly(t *testing.T) {
	cases := map[string]bool{
		"0.0.0.0:8077":   true,
		":8077":          true,
		"192.168.1.5:80": true,
		"[::]:8077":      true,
		"127.0.0.1:8077": false,
		"localhost:8077": false,
		"[::1]:8077":     false,
		"мусор":          false,
	}
	for addr, want := range cases {
		if got := listensPublicly(addr); got != want {
			t.Errorf("listensPublicly(%q) = %v, want %v", addr, got, want)
		}
	}
}
