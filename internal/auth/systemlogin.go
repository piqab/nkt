package auth

import (
	"context"
	"errors"
	"github.com/piqab/nkt/internal/msgs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// На своей машине заводить отдельный пароль для панели управления
// странно: у пользователя уже есть системная учётная запись, и именно ей
// он входит на этот компьютер. Второй способ входа проверяет пароль
// системного пользователя и, если тот в группе sudo, открывает сессию.
//
// Проверка идёт через unix_chkpwd — тот самый вспомогательный бинарник,
// которым это делает сам PAM. Он умеет ровно одно: прочитать /etc/shadow
// (для чего у него setgid shadow) и сравнить пароль. Своей реализации
// сравнения здесь нет и быть не должно: в Debian давно yescrypt, дальше
// будет что-то ещё, и повторять это в Go — способ однажды перестать
// пускать половину пользователей.
//
// Пароль передаётся на stdin и нигде не логируется. Ни в аудит, ни в
// сообщение об ошибке он не попадает — туда идёт только имя.

// unixChkpwdPaths — где лежит помощник PAM в разных дистрибутивах.
var unixChkpwdPaths = []string{
	"/usr/sbin/unix_chkpwd",
	"/sbin/unix_chkpwd",
	"/usr/lib/unix_chkpwd",
}

// systemLoginTimeout — проверка локальная и мгновенная; таймаут нужен на
// случай, если помощник по какой-то причине завис.
const systemLoginTimeout = 5 * time.Second

// sudoGroups — членство в любой из них считается признаком «этому
// человеку и так можно всё на этой машине». Пускать в панель кого угодно
// с учёткой на хосте нельзя: обычный пользователь машины и администратор
// машины — разные роли.
var sudoGroups = map[string]bool{"sudo": true, "wheel": true, "admin": true}

// SystemLoginAvailable сообщает, можно ли вообще проверить системный
// пароль на этой машине.
func SystemLoginAvailable() bool { return unixChkpwdPath() != "" }

func unixChkpwdPath() string {
	for _, p := range unixChkpwdPaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// verifySystemPassword возвращает nil, если пароль подходит.
func verifySystemPassword(ctx context.Context, username, password string) error {
	helper := unixChkpwdPath()
	if helper == "" {
		return msgs.Errorf("auth.unixChkpwdInstalledHostLogging")
	}
	ctx, cancel := context.WithTimeout(ctx, systemLoginTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, helper, username, "nullok")
	cmd.Stdin = strings.NewReader(password + "\n")
	if err := cmd.Run(); err != nil {
		// Коды unix_chkpwd различают «неверный пароль» и «не разрешено
		// спрашивать про этого пользователя» (так бывает, когда nkt
		// работает не от root). Пользователю в обоих случаях говорится
		// одно и то же, но в журнал попадает различимое.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return msgs.Errorf("auth.unixChkpwdRejectedLoginCode", exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// systemUserInSudoGroup читает /etc/group и отвечает, состоит ли
// пользователь в одной из административных групп.
//
// Читается файл, а не вызывается `id -nG`: это дешевле, не зависит от
// PATH и одинаково работает в песочнице юнита.
func systemUserInSudoGroup(username string) (bool, error) {
	group, err := os.ReadFile("/etc/group")
	if err != nil {
		return false, err
	}
	passwd, _ := os.ReadFile("/etc/passwd")
	return userIsAdmin(string(passwd), string(group), username), nil
}

// userIsAdmin — сама проверка, отделённая от чтения файлов: это ворота, за
// которыми чужой человек с учёткой на машине получает или не получает
// панель управления ею, и проверяться она должна тестом, а не на живой
// системе.
func userIsAdmin(passwd, group, username string) bool {
	if username == "" {
		return false
	}
	if primary := primaryGroupName(passwd, group, username); sudoGroups[primary] {
		return true
	}
	for _, line := range strings.Split(group, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) < 4 || !sudoGroups[fields[0]] {
			continue
		}
		for _, member := range strings.Split(fields[3], ",") {
			if strings.TrimSpace(member) == username {
				return true
			}
		}
	}
	return false
}

// primaryGroupName находит основную группу пользователя: у члена группы
// wheel в RHEL она бывает основной, и в списке членов группы его тогда
// нет вовсе.
func primaryGroupName(passwd, group, username string) string {
	gid := ""
	for _, line := range strings.Split(passwd, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) < 4 || fields[0] != username {
			continue
		}
		gid = fields[3]
		break
	}
	if gid == "" {
		return ""
	}
	for _, line := range strings.Split(group, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) < 3 || fields[2] != gid {
			continue
		}
		return fields[0]
	}
	return ""
}

// LoginSystem открывает сессию по паролю системной учётной записи.
//
// root — особый случай: его основная группа называется root, и в списке
// членов sudo он не значится, но именно он и есть администратор машины.
func (s *Service) LoginSystem(ctx context.Context, username, password, userAgent string) (string, time.Time, store.User, error) {
	if !s.cfg.SystemLogin {
		return "", time.Time{}, store.User{}, ErrInvalidCredentials
	}
	if !s.attempts.Allow(username) {
		return "", time.Time{}, store.User{}, ErrTooManyAttempts
	}

	if username != "root" {
		inSudo, err := systemUserInSudoGroup(username)
		if err != nil || !inSudo {
			s.attempts.Fail(username)
			return "", time.Time{}, store.User{}, ErrInvalidCredentials
		}
	}
	if err := verifySystemPassword(ctx, username, password); err != nil {
		s.attempts.Fail(username)
		return "", time.Time{}, store.User{}, ErrInvalidCredentials
	}
	s.attempts.Clear(username)

	user, err := s.ensureSystemUser(ctx, username)
	if err != nil {
		return "", time.Time{}, store.User{}, err
	}

	token, err := NewToken()
	if err != nil {
		return "", time.Time{}, store.User{}, err
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.db.CreateSession(ctx, token, user.ID, expires, userAgent); err != nil {
		return "", time.Time{}, store.User{}, err
	}
	_ = s.db.TouchLogin(ctx, user.ID)
	return token, expires, user, nil
}

// ensureSystemUser находит или заводит запись для системного
// пользователя. Пароль в этой записи не хранится вовсе — вместо хеша
// кладётся заведомо непроверяемое значение: войти по нему обычным путём
// нельзя, и подобрать его тоже нельзя, потому что сравнивать не с чем.
func (s *Service) ensureSystemUser(ctx context.Context, username string) (store.User, error) {
	user, err := s.db.UserByName(ctx, username)
	if err == nil {
		if user.Disabled {
			return store.User{}, ErrInvalidCredentials
		}
		return user, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, err
	}

	// Случайный хеш вместо пароля: формат тот же, совпасть не может ни с
	// чем, что можно ввести.
	unusable, err := HashPassword(strconv.FormatInt(time.Now().UnixNano(), 36) + "-system-login-only")
	if err != nil {
		return store.User{}, err
	}
	if _, err := s.db.CreateUser(ctx, username, unusable, store.RoleAdmin); err != nil {
		return store.User{}, err
	}
	return s.db.UserByName(ctx, username)
}
