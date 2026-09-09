package hub

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Свежий сервер обычно приходит в одном и том же виде: root, пароль, и
// больше ничего. Установка nkt на него формально проходит, но дальше
// половина разделов молчит — без dbus не открывается терминал и не
// работают установки пакетов из интерфейса, без iproute2 пуст список
// слушающих сокетов, без procps не привязываются процессы. А пароль root
// остаётся единственным способом входа навсегда, потому что заменить его
// на ключ вручную никто потом не возвращается.
//
// Подготовка делает ровно эту разовую работу перед установкой: ставит
// недостающие пакеты, заводит отдельного пользователя с NOPASSWD-sudo,
// кладёт ключ хаба и переводит запись хоста на ключ, а по желанию гасит
// вход по паролю. Порядок и проверки здесь важнее самих команд: каждый
// необратимый шаг делается только после того, как проверен следующий за
// ним способ входа.

// BootstrapPackagesDefault — то, что ставится по умолчанию. Первые шесть
// нужны самому nkt, последние два включают режим tmux в терминале и живой
// просмотр нагрузки, за которыми иначе придётся отдельно ходить в
// интерфейс и нажимать «установить».
var BootstrapPackagesDefault = []string{
	"dbus", "sudo", "iproute2", "procps", "ca-certificates", "curl", "tmux", "btop",
}

// BootstrapOptions описывает разовую подготовку хоста.
type BootstrapOptions struct {
	Enabled bool `json:"enabled"`
	// User — кого завести вместо работы от root. Пустая строка оставляет
	// подключение под тем пользователем, что уже указан.
	User     string   `json:"user"`
	Packages []string `json:"packages"`
	// DisablePasswordAuth выключает вход по паролю в sshd — только после
	// того, как вход по ключу проверен новым соединением.
	DisablePasswordAuth bool `json:"disable_password_auth"`
}

// bootstrapUserRe — имя пользователя уходит в команды useradd/chown и в
// путь sudoers, поэтому проверяется по тем же правилам, что принимает сам
// useradd, и ничего больше.
var bootstrapUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// bootstrapPackageRe — имя пакета уходит в apt-get install; всё, что не
// похоже на имя пакета, отбивается здесь, а не апт-ом.
var bootstrapPackageRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]*$`)

// Validate проверяет параметры до единого подключения к хосту: ошибку в
// имени пользователя лучше показать в форме, чем на середине установки.
func (o *BootstrapOptions) Validate() error {
	if !o.Enabled {
		return nil
	}
	if o.User != "" && !bootstrapUserRe.MatchString(o.User) {
		return fmt.Errorf("недопустимое имя пользователя: %q", o.User)
	}
	if len(o.Packages) == 0 {
		o.Packages = append([]string(nil), BootstrapPackagesDefault...)
	}
	for _, p := range o.Packages {
		if !bootstrapPackageRe.MatchString(p) {
			return fmt.Errorf("недопустимое имя пакета: %q", p)
		}
	}
	if o.DisablePasswordAuth && o.User == "" {
		// Выключить пароль, оставшись под root, можно — но только если ключ
		// действительно лёг root'у; это ровно то, что и произойдёт.
		return nil
	}
	return nil
}

// bootstrapResult — что подготовка изменила в записи хоста; применяется
// вызывающей стороной уже после успеха всех проверок.
type bootstrapResult struct {
	SSHUser    string
	PrivatePEM string
}

// bootstrapHost выполняет подготовку по уже открытому соединению (обычно
// root с паролем). Возвращает новые реквизиты подключения, если они
// поменялись.
func (m *Manager) bootstrapHost(ctx context.Context, client *ssh.Client, host store.Host,
	opts BootstrapOptions, report func(key string, args ...any)) (bootstrapResult, error) {

	var res bootstrapResult
	if err := checkAptHost(client); err != nil {
		return res, err
	}

	sudo := sudoPrefix(host.SSHUser)

	report("hub.bootstrapPackages", strings.Join(opts.Packages, " "))
	if out, err := runRemote(client, sudo+"env DEBIAN_FRONTEND=noninteractive apt-get update"+
		" && "+sudo+"env DEBIAN_FRONTEND=noninteractive apt-get install -y "+strings.Join(opts.Packages, " ")); err != nil {
		return res, fmt.Errorf("установка пакетов: %w: %s", err, lastLines(out, 5))
	}

	targetUser := host.SSHUser
	if opts.User != "" {
		report("hub.bootstrapUser", opts.User)
		if err := createBootstrapUser(client, sudo, opts.User); err != nil {
			return res, err
		}
		targetUser = opts.User
	}

	report("hub.bootstrapKey", targetUser)
	privatePEM, authorizedKey, err := generateHostKeyPair()
	if err != nil {
		return res, err
	}
	if err := installAuthorizedKey(client, sudo, targetUser, authorizedKey); err != nil {
		return res, err
	}

	// Проверка отдельным соединением: только новое подключение доказывает,
	// что ключ принят — текущая сессия открыта по паролю и переживёт любую
	// ошибку в authorized_keys.
	report("hub.bootstrapVerifyKey", targetUser)
	verify, err := dialSSH(ctx, host.Addr, host.SSHPort, targetUser, store.HostAuthKey, []byte(privatePEM))
	if err != nil {
		return res, fmt.Errorf("вход по ключу под %s не работает, пароль оставлен как есть: %w", targetUser, err)
	}
	defer verify.Close()

	if targetUser != "root" {
		if out, err := runRemote(verify, "sudo -n true"); err != nil {
			return res, fmt.Errorf("sudo без пароля для %s не работает: %w: %s", targetUser, err, lastLines(out, 3))
		}
	}

	res.SSHUser, res.PrivatePEM = targetUser, privatePEM

	if opts.DisablePasswordAuth {
		report("hub.bootstrapDisablePassword")
		if err := disablePasswordAuth(ctx, verify, host, targetUser, privatePEM); err != nil {
			// Не срываем подготовку целиком: ключ уже работает, а вход по
			// паролю просто остался включённым — это ровно то состояние, в
			// котором хост был до сих пор.
			report("hub.bootstrapDisablePasswordFailed", err.Error())
		}
	}
	return res, nil
}

// sudoPrefix — под root ничего не нужно, под остальными всё идёт через
// sudo -n: пароль запрашивать некому, сессия неинтерактивная.
func sudoPrefix(user string) string {
	if user == "root" {
		return ""
	}
	return "sudo -n "
}

// checkAptHost отказывается работать там, где apt нет: подготовка написана
// под Debian/Ubuntu, и молча пропустить установку пакетов было бы хуже,
// чем сказать об этом сразу.
func checkAptHost(client *ssh.Client) error {
	if _, err := runRemote(client, "command -v apt-get"); err != nil {
		return fmt.Errorf("подготовка хоста поддерживает только Debian/Ubuntu-подобные системы: apt-get на хосте не найден")
	}
	if _, err := runRemote(client, "test -d /run/systemd/system"); err != nil {
		return fmt.Errorf("на хосте не запущен systemd — nkt устанавливается как systemd-юнит")
	}
	return nil
}

// createBootstrapUser заводит пользователя и даёт ему NOPASSWD-sudo тем же
// файлом, который nkt умеет потом убрать (sudoersDropIn).
func createBootstrapUser(client *ssh.Client, sudo, user string) error {
	if out, err := runRemote(client, fmt.Sprintf(
		"id -u %[1]s >/dev/null 2>&1 || %[2]suseradd --create-home --shell /bin/bash %[1]s", user, sudo)); err != nil {
		return fmt.Errorf("создание пользователя %s: %w: %s", user, err, lastLines(out, 3))
	}
	// visudo -cf проверяет файл ДО того, как он попадёт в /etc/sudoers.d:
	// синтаксически неверный файл там ломает sudo для всех сразу.
	if out, err := runRemote(client, sudoersInstallCmd(sudo, user)); err != nil {
		return fmt.Errorf("настройка sudo для %s: %w: %s", user, err, lastLines(out, 3))
	}
	return nil
}

// installAuthorizedKey кладёт публичный ключ хаба пользователю, не трогая
// то, что там уже есть: на хосте может быть чужой рабочий доступ.
func installAuthorizedKey(client *ssh.Client, sudo, user, authorizedKey string) error {
	if out, err := runRemote(client, authorizedKeyCmd(sudo, user, authorizedKey)); err != nil {
		return fmt.Errorf("установка ключа для %s: %w: %s", user, err, lastLines(out, 3))
	}
	return nil
}

// sudoersInstallCmd — правило NOPASSWD пишется во временный файл, проверяется
// visudo и только потом ставится на место: синтаксически неверный файл в
// /etc/sudoers.d ломает sudo сразу для всех, включая того, кто его положил.
func sudoersInstallCmd(sudo, user string) string {
	rule := fmt.Sprintf("%s ALL=(ALL) NOPASSWD: ALL", user)
	return fmt.Sprintf(
		"printf '%%s\n' %[1]s > /tmp/nkt-sudoers && %[2]svisudo -cf /tmp/nkt-sudoers"+
			" && %[2]sinstall -m 0440 -o root -g root /tmp/nkt-sudoers %[3]s; rc=$?; rm -f /tmp/nkt-sudoers; exit $rc",
		shellQuote(rule), sudo, sudoersDropIn)
}

// authorizedKeyCmd дописывает ключ, а не переписывает файл: на хосте почти
// наверняка уже есть чей-то рабочий доступ, и отобрать его подготовка не
// должна.
func authorizedKeyCmd(sudo, user, authorizedKey string) string {
	home := "/root"
	if user != "root" {
		home = "/home/" + user
	}
	return fmt.Sprintf(
		"%[1]sinstall -d -m 0700 -o %[2]s -g %[2]s %[3]s/.ssh"+
			" && printf '%%s\n' %[4]s | %[1]stee -a %[3]s/.ssh/authorized_keys >/dev/null"+
			" && %[1]schown %[2]s:%[2]s %[3]s/.ssh/authorized_keys"+
			" && %[1]schmod 0600 %[3]s/.ssh/authorized_keys",
		sudo, user, home, shellQuote(authorizedKey))
}

// nktSSHDropIn — файл, которым подготовка гасит вход по паролю. Отдельный
// drop-in, а не правка sshd_config: своё легко убрать, чужое не тронуто.
const nktSSHDropIn = "/etc/ssh/sshd_config.d/99-nkt-no-password.conf"

// disablePasswordAuth выключает парольный вход и проверяет, что после
// перезагрузки sshd вход по ключу всё ещё работает. Если нет — drop-in
// удаляется и sshd перезагружается обратно.
func disablePasswordAuth(ctx context.Context, client *ssh.Client, host store.Host, user, privatePEM string) error {
	sudo := sudoPrefix(user)
	// Include в основном конфиге есть не всегда: в старых образах
	// sshd_config.d просто не подключён, и файл там был бы бесполезен.
	if _, err := runRemote(client, "grep -qs '^Include /etc/ssh/sshd_config.d/' /etc/ssh/sshd_config"); err != nil {
		return fmt.Errorf("в sshd_config нет Include для sshd_config.d — вход по паролю оставлен включённым")
	}
	write := fmt.Sprintf(
		"%[1]sinstall -d -m 0755 /etc/ssh/sshd_config.d"+
			" && printf '%%s\\n' 'PasswordAuthentication no' 'KbdInteractiveAuthentication no'"+
			" | %[1]stee %[2]s >/dev/null",
		sudo, nktSSHDropIn)
	if out, err := runRemote(client, write); err != nil {
		return fmt.Errorf("запись %s: %w: %s", nktSSHDropIn, err, lastLines(out, 3))
	}

	rollback := func() {
		_, _ = runRemote(client, sudo+"rm -f "+nktSSHDropIn+" && "+sudo+"systemctl reload ssh 2>/dev/null || "+sudo+"systemctl reload sshd")
	}
	if out, err := runRemote(client, sudo+"sshd -t"); err != nil {
		rollback()
		return fmt.Errorf("sshd отклонил конфигурацию: %w: %s", err, lastLines(out, 3))
	}
	if out, err := runRemote(client, sudo+"systemctl reload ssh 2>/dev/null || "+sudo+"systemctl reload sshd"); err != nil {
		rollback()
		return fmt.Errorf("перезагрузка sshd: %w: %s", err, lastLines(out, 3))
	}
	// Ещё одно новое соединение: reload прошёл, но принимает ли демон
	// подключения — вопрос отдельный, и уже открытая сессия на него не
	// отвечает.
	check, err := dialSSH(ctx, host.Addr, host.SSHPort, user, store.HostAuthKey, []byte(privatePEM))
	if err != nil {
		rollback()
		return fmt.Errorf("после выключения пароля вход по ключу перестал работать, изменение отменено: %w", err)
	}
	_ = check.Close()
	return nil
}

// applyBootstrapResult переводит запись хоста на ключ и нового
// пользователя — уже после того, как оба проверены живым подключением.
func (m *Manager) applyBootstrapResult(ctx context.Context, hostID int64, host store.Host, res bootstrapResult) error {
	if res.PrivatePEM == "" {
		return nil
	}
	secretEnc, err := secretbox.Encrypt(m.key, []byte(res.PrivatePEM))
	if err != nil {
		return fmt.Errorf("шифрование ключа: %w", err)
	}
	if err := m.db.SetHostSecret(ctx, hostID, store.HostAuthKey, secretEnc); err != nil {
		return err
	}
	if res.SSHUser != host.SSHUser {
		if err := m.db.UpdateHost(ctx, hostID, host.Name, host.Addr, host.SSHPort, res.SSHUser, store.HostAuthKey); err != nil {
			return err
		}
	}
	return nil
}

// shellQuote заворачивает строку в одинарные кавычки — единственная форма,
// внутри которой шелл не интерпретирует ничего вообще. Сама кавычка
// закрывается и вставляется отдельно ('\”), как принято.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// lastLines оставляет от вывода команды хвост: apt печатает сотни строк, а
// в сообщении об ошибке нужны последние.
func lastLines(out string, n int) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "; "))
}
