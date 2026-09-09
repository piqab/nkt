package hub

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// Имя пользователя и имена пакетов уходят в шелл-команды на чужой машине,
// поэтому проверяются до единого подключения — и проверка должна отбивать
// именно то, что в шелле опасно.
func TestBootstrapOptionsValidate(t *testing.T) {
	valid := []string{"nkt", "deploy", "_svc", "a", "user-1"}
	for _, u := range valid {
		o := BootstrapOptions{Enabled: true, User: u}
		if err := o.Validate(); err != nil {
			t.Errorf("пользователь %q отклонён: %v", u, err)
		}
	}

	invalid := []string{"Nkt", "root; rm -rf /", "user name", "-flag", "пользователь", strings.Repeat("a", 33), "u$(id)"}
	for _, u := range invalid {
		o := BootstrapOptions{Enabled: true, User: u}
		if err := o.Validate(); err == nil {
			t.Errorf("пользователь %q принят, хотя не должен", u)
		}
	}

	// Пустой список пакетов заполняется набором по умолчанию, а не
	// превращается в apt-get install без аргументов.
	o := BootstrapOptions{Enabled: true}
	if err := o.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(o.Packages) != len(BootstrapPackagesDefault) {
		t.Errorf("пакеты по умолчанию не подставлены: %v", o.Packages)
	}

	for _, pkg := range []string{"-o", "htop; reboot", "../etc", "PKG", ""} {
		bad := BootstrapOptions{Enabled: true, Packages: []string{"tmux", pkg}}
		if err := bad.Validate(); err == nil {
			t.Errorf("пакет %q принят, хотя не должен", pkg)
		}
	}

	// Выключённая подготовка не проверяется вовсе: её параметры не поедут
	// никуда.
	off := BootstrapOptions{User: "ROOT!!"}
	if err := off.Validate(); err != nil {
		t.Errorf("выключенная подготовка не должна ничего проверять: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"ssh-ed25519 AAAA nkt-hub": `'ssh-ed25519 AAAA nkt-hub'`,
		"it's":                     `'it'\''s'`,
		"$(reboot)":                `'$(reboot)'`,
		"a`b`":                     "'a`b`'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// Команды собираются строками, и именно здесь легко потерять sudo или
// подставить значение без кавычек.
func TestBootstrapCommands(t *testing.T) {
	t.Run("root не зовёт sudo", func(t *testing.T) {
		if got := sudoPrefix("root"); got != "" {
			t.Errorf("sudoPrefix(root) = %q, want пусто", got)
		}
		// Именно "sudo -n ": подстрока "sudo " встречается и внутри
		// безобидного visudo, на чём этот тест сначала и споткнулся.
		if strings.Contains(sudoersInstallCmd("", "nkt"), "sudo -n ") {
			t.Error("под root в команде появился sudo")
		}
		if strings.Contains(authorizedKeyCmd("", "root", "k"), "sudo -n ") {
			t.Error("под root установка ключа зовёт sudo")
		}
	})

	t.Run("остальные — через sudo -n", func(t *testing.T) {
		sudo := sudoPrefix("deploy")
		if sudo != "sudo -n " {
			t.Fatalf("sudoPrefix(deploy) = %q", sudo)
		}
		cmd := sudoersInstallCmd(sudo, "deploy")
		// Пароль спрашивать некому: сессия неинтерактивная, и sudo без -n
		// зависнет до таймаута вместо честной ошибки.
		if !strings.Contains(cmd, "sudo -n visudo") || !strings.Contains(cmd, "sudo -n install") {
			t.Errorf("шаги идут не через sudo -n: %s", cmd)
		}
		if !strings.Contains(cmd, "visudo -cf") {
			t.Errorf("файл sudoers ставится без проверки visudo: %s", cmd)
		}
		if !strings.Contains(cmd, sudoersDropIn) {
			t.Errorf("правило кладётся не в %s: %s", sudoersDropIn, cmd)
		}
	})

	t.Run("ключ дописывается, а не перезаписывает файл", func(t *testing.T) {
		cmd := authorizedKeyCmd("sudo -n ", "deploy", "ssh-ed25519 AAAA nkt-hub")
		if !strings.Contains(cmd, "tee -a ") {
			t.Errorf("authorized_keys перезаписывается вместо дописывания: %s", cmd)
		}
		if !strings.Contains(cmd, "/home/deploy/.ssh/authorized_keys") {
			t.Errorf("неверный путь: %s", cmd)
		}
		if !strings.Contains(cmd, "'ssh-ed25519 AAAA nkt-hub'") {
			t.Errorf("ключ подставлен без кавычек: %s", cmd)
		}
		if !strings.Contains(cmd, "chmod 0600") {
			t.Errorf("права на authorized_keys не выставляются: %s", cmd)
		}
	})

	t.Run("root пишет в /root", func(t *testing.T) {
		if !strings.Contains(authorizedKeyCmd("", "root", "k"), "/root/.ssh/authorized_keys") {
			t.Error("для root выбран неверный домашний каталог")
		}
	})
}

func TestLastLines(t *testing.T) {
	out := "строка1\nстрока2\nстрока3\nстрока4\n"
	if got := lastLines(out, 2); got != "строка3; строка4" {
		t.Errorf("lastLines = %q", got)
	}
	if got := lastLines("одна", 5); got != "одна" {
		t.Errorf("lastLines короткого вывода = %q", got)
	}
}

// Живая проверка того шага, ради которого всё и делается: пока новый ключ
// не принят настоящим sshd, способ входа у хоста не меняется. Гоняется
// против настоящего sshd — как остальные интеграционные тесты в этом
// пакете.
func TestBootstrapKeyVerificationAgainstRealSSHD(t *testing.T) {
	sshdPath := findExecutable(t, []string{"/usr/sbin/sshd", "/usr/local/sbin/sshd", "sshd"})
	sftpServer := findExecutable(t, []string{"/usr/lib/openssh/sftp-server", "/usr/libexec/openssh/sftp-server", "/usr/lib/ssh/sftp-server"})

	dir := t.TempDir()
	goodPEM, goodLine, err := generateHostKeyPair()
	if err != nil {
		t.Fatalf("генерация ключа: %v", err)
	}
	otherPEM, _, err := generateHostKeyPair()
	if err != nil {
		t.Fatalf("генерация второго ключа: %v", err)
	}

	addr, port := launchTestSSHD(t, sshdPath, sftpServer, dir, goodLine)
	user := currentUsername(t)

	// Ключ, который лёг в authorized_keys, пускает — это и есть проверка,
	// которую bootstrapHost делает отдельным соединением перед тем, как
	// переписать способ входа хоста.
	client, err := dialSSH(context.Background(), addr, port, user, store.HostAuthKey, []byte(goodPEM))
	if err != nil {
		t.Fatalf("вход по установленному ключу не работает: %v", err)
	}
	_ = client.Close()

	// А чужой — нет. Без этого «проверка» проходила бы всегда и меняла
	// способ входа на неработающий, оставив хост недоступным.
	if c, err := dialSSH(context.Background(), addr, port, user, store.HostAuthKey, []byte(otherPEM)); err == nil {
		_ = c.Close()
		t.Fatal("посторонний ключ пустили — проверка ничего не проверяет")
	}
}

func findExecutable(t *testing.T, candidates []string) string {
	t.Helper()
	for _, c := range candidates {
		if filepath.IsAbs(c) {
			if _, err := exec.LookPath(c); err == nil {
				return c
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skipf("не найдено ничего из %v — пропускаю интеграционный тест", candidates)
	return ""
}

func currentUsername(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("id", "-un").Output()
	if err != nil {
		t.Skipf("не удалось узнать имя пользователя: %v", err)
	}
	return strings.TrimSpace(string(out))
}
