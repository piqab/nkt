package profile

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/msgs"
)

// Применение идёт по пунктам плана, а не «всё разом»: оператор отмечает,
// что применять, и каждый пункт выполняется тем же механизмом, каким это
// делается вручную в своём разделе. Никакого второго пути к системе у
// профиля нет — иначе правки профилем и правки руками расходились бы в
// поведении, истории и откатах.

// Applier выполняет один пункт плана.
type Applier interface {
	Apply(ctx context.Context, c Change) (string, error)
}

// HostApplier применяет план на этой машине через уже существующие
// менеджеры.
type HostApplier struct {
	services  *control.ServiceManager
	configs   *control.ConfigManager
	firewall  *control.FirewallManager
	firewalld *control.FirewalldManager
	osusers   *control.OSUserManager
	sysconf   *control.SysConfigManager
	// escape — запуск команд вне песочницы юнита: apt-get ставит пакеты
	// в /usr и /var, куда изнутри юнита писать нельзя.
	escape control.PrivilegedRunner
	// user — от чьего имени пишется история и аудит.
	user string
}

// NewHostApplier строит исполнителя плана.
func NewHostApplier(user string, services *control.ServiceManager, configs *control.ConfigManager,
	firewall *control.FirewallManager, firewalld *control.FirewalldManager,
	osusers *control.OSUserManager, sysconf *control.SysConfigManager,
	escape control.PrivilegedRunner) *HostApplier {
	return &HostApplier{
		services: services, configs: configs, firewall: firewall, firewalld: firewalld,
		osusers: osusers, sysconf: sysconf, escape: escape, user: user,
	}
}

// Apply выполняет пункт и возвращает строку для журнала задания.
func (a *HostApplier) Apply(ctx context.Context, c Change) (string, error) {
	switch c.Action {
	case ActionInstallPackage:
		return a.installPackage(ctx, c.Target)
	case ActionEnableService:
		return a.serviceAction(ctx, c.Target, "enable")
	case ActionDisableService:
		return a.serviceAction(ctx, c.Target, "disable")
	case ActionStartService:
		return a.serviceAction(ctx, c.Target, "start")
	case ActionStopService:
		return a.serviceAction(ctx, c.Target, "stop")
	case ActionWriteFile:
		return a.writeFile(ctx, c)
	case ActionAllowPort:
		return a.allowPort(ctx, c.Target)
	case ActionCreateUser:
		return a.createUser(ctx, c.Target, "", false)
	case ActionGrantSudo:
		return a.createUser(ctx, c.Target, "", true)
	case ActionAddKey:
		return a.createUser(ctx, c.Target, c.Detail, false)
	case ActionSetHostname:
		return a.setHostname(ctx, c.Target)
	case ActionInstallDocker:
		return a.installDocker(ctx)
	case ActionWriteCompose:
		return a.writeCompose(ctx, c)
	case ActionComposeUp:
		return a.composeUp(ctx, c.Target)
	case ActionComposeDown:
		return a.composeDown(ctx, c.Target)
	case ActionSetTimezone:
		return a.setTimezone(ctx, c.Target)
	}
	return "", msgs.Errorf("profile.unknownAction", c.Action)
}

func (a *HostApplier) installPackage(ctx context.Context, name string) (string, error) {
	if !packageRe.MatchString(name) {
		return "", msgs.Errorf("profile.invalidPackageName", name)
	}
	if a.escape == nil {
		return "", msgs.Errorf("profile.packageInstallationUnavailableMode")
	}
	res, err := a.escape(ctx, "apt-get", "install", "-y", name)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("apt-get install %s: %s", name, lastMeaningfulLine(res.Stderr, res.Stdout))
	}
	return msgs.Tc(ctx, "profile.packageInstalled", name), nil
}

func (a *HostApplier) serviceAction(ctx context.Context, name, action string) (string, error) {
	if a.services == nil {
		return "", msgs.Errorf("profile.serviceManagementUnavailable")
	}
	res, err := a.services.Action(ctx, a.user, name, action)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("systemctl %s %s: %s", action, name, lastMeaningfulLine(res.Stderr, res.Stdout))
	}
	return msgs.Tc(ctx, "profile.service", name, action), nil
}

func (a *HostApplier) writeFile(ctx context.Context, c Change) (string, error) {
	if a.configs == nil {
		return "", msgs.Errorf("profile.configurationEditorUnavailable")
	}
	// Через тот же Write, что и ручная правка: с проверкой конфигурации
	// службы, записью в историю версий и откатом при неудачной проверке.
	res, err := a.configs.Write(ctx, msgs.FromContext(ctx), a.user, c.Target, c.Detail, msgs.Tc(ctx, "profile.applyNote"), false)
	if err != nil {
		return "", err
	}
	if res.RolledBack {
		return "", fmt.Errorf("%s: %s", c.Target, res.Message)
	}
	return msgs.Tc(ctx, "profile.fileWritten", c.Target), nil
}

// installDocker ставит docker по официальной инструкции.
//
// Не из репозитория дистрибутива: там пакет отстаёт на версии, а
// compose-плагина может не быть вовсе — без него «docker compose» просто
// не существует, и стек из профиля не поднять.
func (a *HostApplier) installDocker(ctx context.Context) (string, error) {
	var lines []string
	err := control.InstallDocker(ctx, a.escape, func(format string, args ...any) {
		lines = append(lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	if err != nil {
		if len(lines) > 0 {
			return "", fmt.Errorf("%w (%s)", err, lines[len(lines)-1])
		}
		return "", err
	}
	return strings.Join(lines, "; "), nil
}

// writeCompose кладёт описание стека на хост.
//
// Без apply: подъём стека — отдельный пункт плана, и оператор, снявший с
// него галочку, не должен получить перезапуск контейнеров как побочный
// эффект записи файла. Проверка самим docker compose при этом остаётся —
// её делает Write, как и для любого другого файла.
func (a *HostApplier) writeCompose(ctx context.Context, c Change) (string, error) {
	if a.configs == nil {
		return "", msgs.Errorf("profile.configurationEditorUnavailable")
	}
	res, err := a.configs.Write(ctx, msgs.FromContext(ctx), a.user, c.Target, c.Detail, msgs.Tc(ctx, "profile.applyStackNote"), false)
	if err != nil {
		return "", err
	}
	if res.RolledBack {
		return "", fmt.Errorf("%s: %s", c.Target, res.Message)
	}
	return msgs.Tc(ctx, "profile.stackDescriptionWritten", c.Target), nil
}

// composeUp поднимает стек — тем же вызовом, что и правка compose-файла
// в редакторе конфигураций.
func (a *HostApplier) composeUp(ctx context.Context, path string) (string, error) {
	if a.services == nil {
		return "", msgs.Errorf("profile.serviceManagementUnavailable")
	}
	if _, err := a.services.ApplyCompose(ctx, a.user, path); err != nil {
		return "", err
	}
	return msgs.Tc(ctx, "profile.stackUp", path), nil
}

// composeDown останавливает стек и убирает его контейнеры.
func (a *HostApplier) composeDown(ctx context.Context, path string) (string, error) {
	if a.services == nil {
		return "", msgs.Errorf("profile.serviceManagementUnavailable")
	}
	if _, err := a.services.ComposeDown(ctx, a.user, path); err != nil {
		return "", err
	}
	return msgs.Tc(ctx, "profile.stackStopped", path), nil
}

// allowPort открывает порт тем менеджером, который на хосте есть.
func (a *HostApplier) allowPort(ctx context.Context, target string) (string, error) {
	port, proto, from, err := parsePortTarget(target)
	if err != nil {
		return "", err
	}
	if a.firewall != nil {
		res, err := a.firewall.AddRule(ctx, a.user, control.RuleSpec{
			Action: "allow", Port: port, Protocol: proto, From: from, Comment: "nkt profile",
		})
		if err == nil && res.ExitCode == 0 {
			return msgs.Tc(ctx, "profile.portAllowedUfw", target), nil
		}
		if err == nil {
			err = fmt.Errorf("%s", lastMeaningfulLine(res.Stderr, res.Stdout))
		}
		if a.firewalld == nil {
			return "", err
		}
	}
	if a.firewalld == nil {
		return "", msgs.Errorf("profile.hostHasNeitherUfwFirewalld")
	}
	res, err := a.firewalld.AddRule(ctx, a.user, control.FirewalldPortSpec{
		Zone: "public", Port: port, Protocol: proto, Permanent: true, Runtime: true,
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("firewall-cmd: %s", lastMeaningfulLine(res.Stderr, res.Stdout))
	}
	return msgs.Tc(ctx, "profile.portAllowedFirewalld", target), nil
}

// createUser заводит учётку, выдаёт sudo или дописывает ключ — Create
// умеет всё это и не ломается на повторе: существующей учётке он просто
// добавляет недостающее.
func (a *HostApplier) createUser(ctx context.Context, name, key string, sudo bool) (string, error) {
	if a.osusers == nil {
		return "", msgs.Errorf("profile.accountManagementUnavailable")
	}
	if err := a.osusers.Create(ctx, control.CreateOptions{Name: name, Key: key, Sudo: sudo}); err != nil {
		return "", err
	}
	switch {
	case key != "":
		return msgs.Tc(ctx, "profile.keyAddedAccount", name), nil
	case sudo:
		return msgs.Tc(ctx, "profile.accountGrantedPasswordlessSudo", name), nil
	}
	return msgs.Tc(ctx, "profile.accountCreated", name), nil
}

func (a *HostApplier) setHostname(ctx context.Context, name string) (string, error) {
	if a.sysconf == nil {
		return "", msgs.Errorf("profile.systemSettingsAreUnavailable")
	}
	if err := a.sysconf.SetHostname(ctx, name); err != nil {
		return "", err
	}
	return msgs.Tc(ctx, "profile.hostnameSet", name), nil
}

func (a *HostApplier) setTimezone(ctx context.Context, zone string) (string, error) {
	if a.sysconf == nil {
		return "", msgs.Errorf("profile.systemSettingsAreUnavailable")
	}
	if err := a.sysconf.SetTimezone(ctx, zone); err != nil {
		return "", err
	}
	return msgs.Tc(ctx, "profile.timezoneSet", zone), nil
}

// parsePortTarget разбирает то, что собрал portTarget.
func parsePortTarget(target string) (port int, proto, from string, err error) {
	rest := target
	if idx := strings.Index(rest, " from "); idx >= 0 {
		from = strings.TrimSpace(rest[idx+len(" from "):])
		rest = rest[:idx]
	}
	portStr, proto, ok := strings.Cut(rest, "/")
	if !ok {
		proto = "tcp"
		portStr = rest
	}
	port, err = strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port < 1 || port > 65535 {
		return 0, "", "", msgs.Errorf("profile.couldParsePort", target)
	}
	return port, proto, from, nil
}

// lastMeaningfulLine достаёт из вывода команды строку, которую стоит
// показать: последняя непустая обычно и есть причина отказа.
func lastMeaningfulLine(streams ...string) string {
	for _, s := range streams {
		lines := strings.Split(strings.TrimSpace(s), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if line := strings.TrimSpace(lines[i]); line != "" {
				return line
			}
		}
	}
	return msgs.T(msgs.DefaultLang, "profile.commandFailed")
}
