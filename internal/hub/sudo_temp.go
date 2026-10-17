package hub

import (
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/msgs"
)

// Временный полный sudo для операций, которых нет среди hub-sudo
// (установка nkt-edge): пароль sudo пользователя вводится один раз, хаб
// кладёт в sudoers.d/nkt-hub правило «NOPASSWD: ALL», а после операции
// возвращает файл, каким он был, — узкое правило, отсутствие правила или
// что угодно ещё. Прежний файл лежит рядом с точкой в имени: такие файлы
// sudo из sudoers.d не читает.
const (
	sudoTempBackup = sudoersDropIn + ".nkt-bak"
	sudoTempNone   = sudoersDropIn + ".nkt-none"
)

// fullSudoCommand — что выполнить на хосте самому (под root или с паролем
// sudo), чтобы дать пользователю полный sudo без пароля тем же файлом,
// который потом перепишет «Сузить sudo». Временный файл — mktemp, а не
// предсказуемое имя в /tmp.
func fullSudoCommand(user string) string {
	if !validAdminUser.MatchString(user) {
		return ""
	}
	return `sudo sh -c 't=$(mktemp) && echo "` + user + ` ALL=(ALL) NOPASSWD: ALL" > "$t" && visudo -cf "$t" >/dev/null && install -m 440 "$t" ` +
		sudoersDropIn + `; rm -f "$t"'`
}

// grantTempSudoScript — под sudo -S: запомнить прежнее правило (если его
// уже не запомнил прошлый, недоведённый запуск) и положить полное.
func grantTempSudoScript(user string) string {
	return `set -e
f=` + sudoersDropIn + `; b=` + sudoTempBackup + `; n=` + sudoTempNone + `
if [ ! -e "$b" ] && [ ! -e "$n" ]; then
  if [ -e "$f" ]; then cp -p "$f" "$b"; else : > "$n"; fi
fi
t=$(mktemp)
echo "` + user + ` ALL=(ALL) NOPASSWD: ALL" > "$t"
visudo -cf "$t" >/dev/null
chmod 440 "$t"
mv "$t" "$f"`
}

// restoreSudoScript — вернуть запомненное; нечего возвращать — ничего.
const restoreSudoScript = `f=` + sudoersDropIn + `; b=` + sudoTempBackup + `; n=` + sudoTempNone + `
if [ -e "$b" ]; then mv "$b" "$f"; elif [ -e "$n" ]; then rm -f "$f" "$n"; fi
echo restored`

// grantTempFullSudo — полный sudo на время операции по паролю sudo
// пользователя. Пароль идёт только на stdin (sudo -S), не в командную
// строку; вызывающий затирает его сам.
func grantTempFullSudo(client *ssh.Client, user string, password []byte) error {
	if user == "root" || !validAdminUser.MatchString(user) {
		return msgs.Errorf("hub.narrowSudoUser", user)
	}
	in := append(append([]byte(nil), password...), '\n')
	defer wipe(in)
	// sudo -k: пароль проверяется всегда, а не берётся из недавнего входа.
	out, err := runRemoteStdin(client, "sudo -k; sudo -S -p '' sh -c "+shellQuote(grantTempSudoScript(user)), in)
	if err != nil {
		switch {
		case strings.Contains(out, "incorrect password") || strings.Contains(out, "Sorry, try again"):
			return msgs.Errorf("edge.sudoPasswordWrong", user)
		case strings.Contains(out, "not in the sudoers") || strings.Contains(out, "may not run sudo") || strings.Contains(out, "is not allowed"):
			return msgs.Errorf("edge.sudoNotAllowed", user)
		}
		return msgs.Errorf("edge.sudoTempFailed", user, lastLines(strings.TrimSpace(out), 3))
	}
	if out, err := runRemote(client, "sudo -n true"); err != nil {
		return msgs.Errorf("edge.sudoTempFailed", user, lastLines(strings.TrimSpace(out), 3))
	}
	return nil
}

// restoreTempSudo — вернуть правило, бывшее до grantTempFullSudo (полный
// sudo ещё действует — им и возвращаем).
func restoreTempSudo(client *ssh.Client) error {
	out, err := runRemote(client, "sudo -n sh -c "+shellQuote(restoreSudoScript))
	if err != nil || !strings.Contains(out, "restored") {
		return msgs.Errorf("edge.sudoRestoreFailed", lastLines(strings.TrimSpace(out), 3))
	}
	return nil
}

// fullSudoRestoreHint — вернуть правило вручную на хосте.
func fullSudoRestoreHint() string {
	return "sudo sh -c " + shellQuote(restoreSudoScript)
}
