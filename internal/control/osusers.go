package control

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/collect"
)

// Учётные записи самой операционной системы — не те, что заводятся в
// разделе «Пользователи» веб-интерфейса. Нужны ровно для одного: дать
// человеку вход на хост по SSH-ключу, не выдавая пароль root и не заходя
// на сервер вручную. Отсюда и объём: список людей, у кого какие ключи, и
// форма «имя плюс ключ».

// osUserMinUID — граница между служебными учётками и людьми. Debian и
// Ubuntu раздают людям uid начиная с 1000; всё, что ниже, заведено
// пакетами (www-data, systemd-*, sshd) и в этом списке только мешало бы.
const osUserMinUID = 1000

// OSUser — учётная запись на хосте.
type OSUser struct {
	Name  string `json:"name"`
	UID   int    `json:"uid"`
	Home  string `json:"home"`
	Shell string `json:"shell"`
	// Sudo — состоит в группе sudo/wheel или имеет собственное правило в
	// /etc/sudoers.d. Показывается, потому что «добавить ключ» и «дать
	// права root» — совершенно разные по последствиям вещи.
	Sudo bool `json:"sudo"`
	// Keys — ключи из authorized_keys, по одной строке, уже усечённые:
	// сама база ключа никому на экране не нужна, а комментарий и тип
	// нужны, чтобы понять, чей это ключ.
	Keys []OSUserKey `json:"keys"`
	// KeysError объясняет, почему ключи прочитать не удалось (нет файла —
	// это не ошибка, а обычное состояние учётки без ключей).
	KeysError string `json:"keys_error,omitempty"`
}

// OSUserKey — одна строка authorized_keys в виде, пригодном для показа.
type OSUserKey struct {
	Type    string `json:"type"`
	Comment string `json:"comment"`
	// Fingerprint — первые и последние символы самой базы ключа: по ним
	// человек узнаёт свой ключ, не видя его целиком.
	Fingerprint string `json:"fingerprint"`
}

// PrivilegedRunner выполняет команду вне песочницы собственного юнита.
// Передаётся снаружи (cmd/nkt подставляет api.RunUnrestricted): весь код
// выхода из песочницы живёт в internal/api, а импортировать его отсюда
// нельзя — зависимость идёт в другую сторону.
type PrivilegedRunner func(ctx context.Context, argv ...string) (collect.CommandResult, error)

// OSUserManager читает и заводит учётные записи хоста.
type OSUserManager struct {
	c      collect.Collector
	escape PrivilegedRunner
}

// NewOSUserManager строит менеджер. escape может быть nil — тогда команды
// идут обычным путём (fixtures-режим, тесты), и на настоящем хосте под
// systemd они упрутся в read-only /etc.
func NewOSUserManager(c collect.Collector, escape PrivilegedRunner) *OSUserManager {
	return &OSUserManager{c: c, escape: escape}
}

// run выполняет команду, меняющую систему: через выход из песочницы, если
// он доступен.
//
// Без него useradd отвечает «cannot lock /etc/passwd; try again later» —
// сообщение про занятый файл, хотя на самом деле каталог /etc открыт
// только на чтение (ProtectSystem=strict), и создать /etc/passwd.lock
// невозможно в принципе.
func (m *OSUserManager) run(ctx context.Context, argv ...string) (collect.CommandResult, error) {
	if m.escape != nil {
		return m.escape(ctx, argv...)
	}
	return m.c.Run(ctx, argv[0], argv[1:]...)
}

// osUserNameRe — те же правила, что принимает useradd. Имя уходит в
// команды и в путь домашнего каталога, поэтому проверяется здесь.
var osUserNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// authorizedKeyRe — строка authorized_keys: тип, база и необязательный
// комментарий. Опции перед типом (command=, from=) сознательно не
// принимаются: они меняют смысл записи, и вставлять их из формы, не
// объясняя последствий, опаснее, чем не поддерживать вовсе.
var authorizedKeyRe = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com)\s+([A-Za-z0-9+/=]+)(\s+(.*))?$`)

// ParseAuthorizedKey проверяет строку ключа и разбирает её для показа.
func ParseAuthorizedKey(line string) (OSUserKey, error) {
	line = strings.TrimSpace(line)
	m := authorizedKeyRe.FindStringSubmatch(line)
	if m == nil {
		return OSUserKey{}, msgs.Errorf("control.lineDoesLookLikeSSH")
	}
	body := m[2]
	short := body
	if len(body) > 16 {
		short = body[:8] + "…" + body[len(body)-8:]
	}
	return OSUserKey{Type: m[1], Comment: strings.TrimSpace(m[4]), Fingerprint: short}, nil
}

// List возвращает учётные записи людей вместе с их ключами.
func (m *OSUserManager) List(ctx context.Context) ([]OSUser, error) {
	raw, err := m.c.ReadFile("/etc/passwd")
	if err != nil {
		return nil, msgs.Errorf("control.readingEtcPasswd", err)
	}
	sudoers := m.sudoGroupMembers(ctx)

	// Пустой список, а не nil: см. DiskManager.Overview — null вместо
	// массива роняет интерфейс на первом же .length.
	out := []OSUser{}
	for _, line := range strings.Split(string(raw), "\n") {
		u, ok := parsePasswdLine(line)
		if !ok {
			continue
		}
		u.Sudo = sudoers[u.Name] || m.hasSudoersDropIn(ctx, u.Name)
		u.Keys, u.KeysError = m.readKeys(u.Home)
		out = append(out, u)
	}
	return out, nil
}

// parsePasswdLine разбирает строку /etc/passwd и отсеивает служебные
// учётки: и по uid, и по оболочке — учётка с nologin/false заведена не для
// входа, каким бы ни был её uid.
func parsePasswdLine(line string) (OSUser, bool) {
	fields := strings.Split(strings.TrimSpace(line), ":")
	if len(fields) < 7 {
		return OSUser{}, false
	}
	uid, err := strconv.Atoi(fields[2])
	if err != nil {
		return OSUser{}, false
	}
	shell := fields[6]
	isRoot := uid == 0
	if !isRoot && uid < osUserMinUID {
		return OSUser{}, false
	}
	if strings.HasSuffix(shell, "nologin") || strings.HasSuffix(shell, "/false") {
		return OSUser{}, false
	}
	// nobody раздаётся с uid 65534 и человеком не является.
	if uid == 65534 {
		return OSUser{}, false
	}
	return OSUser{Name: fields[0], UID: uid, Home: fields[5], Shell: shell}, true
}

// sudoGroupMembers — состав групп sudo и wheel одним чтением /etc/group.
func (m *OSUserManager) sudoGroupMembers(context.Context) map[string]bool {
	out := map[string]bool{}
	raw, err := m.c.ReadFile("/etc/group")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) < 4 || (fields[0] != "sudo" && fields[0] != "wheel" && fields[0] != "admin") {
			continue
		}
		for _, name := range strings.Split(fields[3], ",") {
			if name = strings.TrimSpace(name); name != "" {
				out[name] = true
			}
		}
	}
	return out
}

// hasSudoersDropIn — есть ли у пользователя собственное правило в
// /etc/sudoers.d. Читается содержимое, а не только имя файла: правило
// может лежать в файле с любым именем.
func (m *OSUserManager) hasSudoersDropIn(ctx context.Context, user string) bool {
	entries, err := m.c.ListDir("/etc/sudoers.d")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		raw, err := m.c.ReadFile(e.Path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if fields := strings.Fields(line); len(fields) > 0 && fields[0] == user {
				return true
			}
		}
	}
	return false
}

// readKeys читает authorized_keys. Отсутствие файла — не ошибка: у учётки
// просто нет ключей.
func (m *OSUserManager) readKeys(home string) ([]OSUserKey, string) {
	if home == "" {
		return nil, ""
	}
	raw, err := m.c.ReadFile(strings.TrimSuffix(home, "/") + "/.ssh/authorized_keys")
	if err != nil {
		return nil, ""
	}
	var keys []OSUserKey
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, err := ParseAuthorizedKey(line)
		if err != nil {
			// Строку с опциями или чужого формата показываем как есть, но
			// без разбора: скрывать существующий доступ нельзя.
			keys = append(keys, OSUserKey{Type: "?", Comment: firstWords(line, 6)})
			continue
		}
		keys = append(keys, key)
	}
	return keys, ""
}

func firstWords(line string, n int) string {
	fields := strings.Fields(line)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

// CreateOptions описывает добавление учётной записи.
type CreateOptions struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	Sudo bool   `json:"sudo"`
}

// Create заводит пользователя (если его ещё нет) и дописывает ему ключ.
//
// Дописывает, а не перезаписывает: у учётки может быть свой рабочий
// доступ, и отобрать его добавлением ещё одного ключа было бы неожиданно.
func (m *OSUserManager) Create(ctx context.Context, opts CreateOptions) error {
	if !osUserNameRe.MatchString(opts.Name) {
		return msgs.Errorf("control.invalidUserName", opts.Name)
	}
	if _, err := ParseAuthorizedKey(opts.Key); err != nil {
		return err
	}

	line := strings.TrimSpace(opts.Key)
	home := "/home/" + opts.Name
	if opts.Name == "root" {
		home = "/root"
	}

	steps := []struct {
		what string
		argv []string
	}{
		{msgs.Tc(ctx, "control.userStepCreate"), []string{"sh", "-c",
			fmt.Sprintf("id -u %[1]s >/dev/null 2>&1 || useradd --create-home --shell /bin/bash %[1]s", opts.Name)}},
		{msgs.Tc(ctx, "control.userStepSSHDir"), []string{"install", "-d", "-m", "0700", "-o", opts.Name, "-g", opts.Name, home + "/.ssh"}},
		{msgs.Tc(ctx, "control.userStepKey"), []string{"sh", "-c",
			fmt.Sprintf("printf '%%s\\n' %s >> %s/.ssh/authorized_keys", shellSingleQuote(line), home)}},
		{msgs.Tc(ctx, "control.userStepKeyPerms"), []string{"sh", "-c",
			fmt.Sprintf("chown %[1]s:%[1]s %[2]s/.ssh/authorized_keys && chmod 0600 %[2]s/.ssh/authorized_keys", opts.Name, home)}},
	}
	for _, step := range steps {
		res, err := m.run(ctx, step.argv...)
		if err != nil {
			return fmt.Errorf("%s: %w", step.what, err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s: %s", step.what, strings.TrimSpace(res.Output()))
		}
	}

	if opts.Sudo {
		if err := m.grantSudo(ctx, opts.Name); err != nil {
			return err
		}
	}
	return nil
}

// grantSudo кладёт правило NOPASSWD отдельным файлом, предварительно
// проверив его visudo: неверный файл в /etc/sudoers.d ломает sudo сразу
// для всех, включая того, кто его положил.
func (m *OSUserManager) grantSudo(ctx context.Context, user string) error {
	rule := fmt.Sprintf("%s ALL=(ALL) NOPASSWD: ALL", user)
	target := "/etc/sudoers.d/nkt-" + user
	cmd := fmt.Sprintf(
		"printf '%%s\\n' %s > /tmp/nkt-osuser && visudo -cf /tmp/nkt-osuser"+
			" && install -m 0440 -o root -g root /tmp/nkt-osuser %s; rc=$?; rm -f /tmp/nkt-osuser; exit $rc",
		shellSingleQuote(rule), target)
	res, err := m.run(ctx, "sh", "-c", cmd)
	if err != nil {
		return msgs.Errorf("control.sudoSetup", err)
	}
	if res.ExitCode != 0 {
		return msgs.Errorf("control.sudoSetup2", strings.TrimSpace(res.Output()))
	}
	return nil
}

// shellSingleQuote — единственная форма, внутри которой шелл не
// интерпретирует ничего; сама кавычка закрывается и вставляется отдельно.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
