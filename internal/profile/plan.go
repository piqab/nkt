package profile

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/model"
)

// Действия плана.
const (
	ActionInstallPackage = "package.install"
	ActionEnableService  = "service.enable"
	ActionDisableService = "service.disable"
	ActionStartService   = "service.start"
	ActionStopService    = "service.stop"
	ActionWriteFile      = "file.write"
	ActionAllowPort      = "firewall.allow"
	ActionCreateUser     = "user.create"
	ActionGrantSudo      = "user.sudo"
	ActionAddKey         = "user.key"
	ActionSetHostname    = "system.hostname"
	ActionSetTimezone    = "system.timezone"
	// Стеки docker compose: сам docker, файл описания и состояние стека.
	ActionInstallDocker = "docker.install"
	ActionWriteCompose  = "compose.write"
	ActionComposeUp    = "compose.up"
	ActionComposeDown  = "compose.down"
)

// Состояния ресурса в плане. Коды, а не готовые слова: план читают и
// по-русски, и по-английски, а переводит интерфейс.
const (
	StateMissing   = "missing"   // пакета/учётки/файла нет
	StateInstalled = "installed" // пакет установлен
	StateEnabled   = "enabled"
	StateDisabled  = "disabled"
	StateRunning   = "running"
	StateStopped   = "stopped"
	StateDiffers   = "differs" // файл есть, но содержимое другое
	StateAsProfile = "as-profile"
	StateAllowed   = "allowed"
	StateBlocked   = "blocked"
	StateNoSudo    = "no-sudo"
	StateSudo      = "sudo"
	StateNoKey     = "no-key"
	StateKeyAdded  = "key-added"
	StateCreated   = "created"
	StateValue     = "value" // текущее/желаемое — само значение в Target
)

// Причины, по которым пункт помечен опасным.
const (
	RiskSSHAccess = "ssh-access"
	RiskSudoGrant = "sudo-grant"
	RiskHostname  = "hostname"
	// RiskExternalRepo — в систему добавляется сторонний репозиторий.
	RiskExternalRepo = "external-repo"
)

// Change — одно расхождение и то, чем его закрыть.
//
// Current и Desired — коды состояний (см. выше), а не готовые фразы:
// иначе план был бы всегда на одном языке, каким бы ни был интерфейс.
type Change struct {
	Action  string `json:"action"`
	Target  string `json:"target"`
	Current string `json:"current"`
	Desired string `json:"desired"`
	// Detail — то, что не влезает в строку: содержимое файла целиком,
	// например. Показывается по требованию.
	Detail string `json:"detail,omitempty"`
	// Risk — правка, о последствиях которой стоит предупредить отдельно
	// (правило SSH, смена имени машины).
	Risk string `json:"risk,omitempty"`
}

// Plan — результат сравнения.
type Plan struct {
	Profile string   `json:"profile"`
	Changes []Change `json:"changes"`
	// Unknown — то, о чём судить не удалось: не отвечает dpkg, не
	// прочитан файл. Пустой план с непустым Unknown значит «не знаю», а
	// не «всё в порядке», и путать это нельзя.
	Unknown []string `json:"unknown,omitempty"`
	TS      string   `json:"ts"`
}

// Empty отвечает, совпало ли состояние с профилем.
func (p Plan) Empty() bool { return len(p.Changes) == 0 }

// Reader — то, что план читает с хоста. Отдельный интерфейс, а не готовые
// менеджеры: сравнение так проверяется тестами без живой машины.
type Reader interface {
	// InstalledPackages отвечает, какие из перечисленных пакетов стоят.
	InstalledPackages(ctx context.Context, names []string) (map[string]bool, error)
	// ServiceState отдаёт состояние службы: установлена, включена,
	// запущена.
	ServiceState(ctx context.Context, name string) (installed, enabled, active bool, err error)
	// FileContent отдаёт содержимое файла; ok=false — файла нет.
	FileContent(ctx context.Context, path string) (content string, ok bool, err error)
	// FirewallState отдаёт текущее состояние пакетного фильтра.
	FirewallState(ctx context.Context) (model.FirewallState, error)
	// Users отдаёт системные учётки: имя → sudo и набор ключей.
	Users(ctx context.Context) (map[string]UserState, error)
	// System отдаёт имя машины и часовой пояс.
	System(ctx context.Context) (hostname, timezone string, err error)
	// ComposeRunning отвечает, сколько контейнеров стека сейчас работает.
	// Ошибка — «не знаю» (docker не отвечает), а не «ни одного».
	ComposeRunning(ctx context.Context, path string) (int, error)
	// DockerPresent отвечает, есть ли на хосте сам docker.
	DockerPresent(ctx context.Context) (bool, error)
}

// UserState — то, что известно об учётной записи на хосте.
type UserState struct {
	Sudo bool
	// Keys — публичные ключи целиком (не усечённые): сравнивать усечённые
	// нельзя, два разных ключа одного вида отличаются как раз серединой.
	Keys []string
}

// Build сравнивает профиль с состоянием хоста.
//
// Ошибка чтения одного ресурса не роняет весь план: она попадает в
// Unknown, а остальные расхождения всё равно показываются. План, который
// не строится целиком из-за недоступного dpkg, бесполезен ровно тогда,
// когда нужнее всего.
func Build(ctx context.Context, p Profile, r Reader) Plan {
	p = p.Normalize()
	// Пустой срез, а не nil: nil уезжает в JSON как null, и на стороне
	// браузера «changes.length» роняет отрисовку всей страницы. Ровно на
	// этом уже спотыкались «Диски» — там был "swap": null.
	plan := Plan{Profile: p.Name, Changes: []Change{}}

	plan.addPackages(ctx, p, r)
	plan.addServices(ctx, p, r)
	plan.addFiles(ctx, p, r)
	plan.addFirewall(ctx, p, r)
	plan.addUsers(ctx, p, r)
	plan.addSystem(ctx, p, r)
	plan.addCompose(ctx, p, r)
	return plan
}

func (plan *Plan) unknown(format string, args ...any) {
	plan.Unknown = append(plan.Unknown, fmt.Sprintf(format, args...))
}

func (plan *Plan) add(c Change) { plan.Changes = append(plan.Changes, c) }

func (plan *Plan) addPackages(ctx context.Context, p Profile, r Reader) {
	if len(p.Packages) == 0 {
		return
	}
	installed, err := r.InstalledPackages(ctx, p.Packages)
	if err != nil {
		plan.unknown("список установленных пакетов: %v", err)
		return
	}
	for _, pkg := range p.Packages {
		if installed[pkg] {
			continue
		}
		plan.add(Change{
			Action: ActionInstallPackage, Target: pkg,
			Current: StateMissing, Desired: StateInstalled,
		})
	}
}

func (plan *Plan) addServices(ctx context.Context, p Profile, r Reader) {
	names := make([]string, 0, len(p.Services))
	for name := range p.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		want := p.Services[name]
		installed, enabled, active, err := r.ServiceState(ctx, name)
		if err != nil {
			plan.unknown("состояние службы %s: %v", name, err)
			continue
		}
		if !installed {
			// Профиль просит состояние службы, которой на хосте нет.
			// Ставить пакет наугад по имени юнита нельзя — имя пакета и
			// имя службы совпадают далеко не всегда.
			plan.unknown("служба %s не найдена на хосте", name)
			continue
		}
		if want.Enabled != nil && *want.Enabled != enabled {
			action, desired := ActionEnableService, StateEnabled
			if !*want.Enabled {
				action, desired = ActionDisableService, StateDisabled
			}
			plan.add(Change{
				Action: action, Target: name,
				Current: enabledWord(enabled), Desired: desired,
			})
		}
		if want.Active != nil && *want.Active != active {
			action, desired := ActionStartService, StateRunning
			if !*want.Active {
				action, desired = ActionStopService, StateStopped
			}
			risk := ""
			if !*want.Active && isCriticalService(name) {
				risk = RiskSSHAccess
			}
			plan.add(Change{
				Action: action, Target: name,
				Current: activeWord(active), Desired: desired, Risk: risk,
			})
		}
	}
}

func (plan *Plan) addFiles(ctx context.Context, p Profile, r Reader) {
	for _, f := range p.Files {
		current, ok, err := r.FileContent(ctx, f.Path)
		if err != nil {
			plan.unknown("чтение %s: %v", f.Path, err)
			continue
		}
		if ok && current == f.Content {
			continue
		}
		what := StateDiffers
		if !ok {
			what = StateMissing
		}
		plan.add(Change{
			Action: ActionWriteFile, Target: f.Path,
			Current: what, Desired: StateAsProfile, Detail: f.Content,
			Risk: fileRisk(f.Path),
		})
	}
}

func (plan *Plan) addFirewall(ctx context.Context, p Profile, r Reader) {
	if p.Firewall == nil || len(p.Firewall.Allow) == 0 {
		return
	}
	state, err := r.FirewallState(ctx)
	if err != nil {
		plan.unknown("состояние пакетного фильтра: %v", err)
		return
	}
	for _, port := range p.Firewall.Allow {
		if portAllowed(state, port) {
			continue
		}
		plan.add(Change{
			Action: ActionAllowPort, Target: portTarget(port),
			Current: StateBlocked, Desired: StateAllowed,
		})
	}
}

func (plan *Plan) addUsers(ctx context.Context, p Profile, r Reader) {
	if len(p.Users) == 0 {
		return
	}
	users, err := r.Users(ctx)
	if err != nil {
		plan.unknown("системные учётные записи: %v", err)
		return
	}
	for _, want := range p.Users {
		have, exists := users[want.Name]
		if !exists {
			plan.add(Change{
				Action: ActionCreateUser, Target: want.Name,
				Current: StateMissing, Desired: StateCreated,
			})
		}
		if want.Sudo != nil && *want.Sudo && (!exists || !have.Sudo) {
			plan.add(Change{
				Action: ActionGrantSudo, Target: want.Name,
				Current: StateNoSudo, Desired: StateSudo, Risk: RiskSudoGrant,
			})
		}
		// Отзыв sudo профилем не делается: это разрыв доступа, который
		// легко получить опечаткой и трудно заметить.
		if want.Sudo != nil && !*want.Sudo && exists && have.Sudo {
			plan.unknown("у %s есть sudo, а профиль просит без него — отзывать права профилем нельзя, снимите вручную", want.Name)
		}
		for _, key := range want.Keys {
			if exists && hasKey(have.Keys, key) {
				continue
			}
			plan.add(Change{
				Action: ActionAddKey, Target: want.Name,
				Current: StateNoKey, Desired: StateKeyAdded, Detail: key,
			})
		}
	}
}

func (plan *Plan) addSystem(ctx context.Context, p Profile, r Reader) {
	if p.System == nil {
		return
	}
	hostname, timezone, err := r.System(ctx)
	if err != nil {
		plan.unknown("системные настройки: %v", err)
		return
	}
	if p.System.Hostname != "" && p.System.Hostname != hostname {
		// Здесь Current и Desired — сами значения, а не состояния:
		// «было web-01, станет proba-01» и есть весь смысл пункта.
		plan.add(Change{
			Action: ActionSetHostname, Target: p.System.Hostname,
			Current: hostname, Desired: p.System.Hostname, Risk: RiskHostname,
		})
	}
	if p.System.Timezone != "" && p.System.Timezone != timezone {
		plan.add(Change{
			Action: ActionSetTimezone, Target: p.System.Timezone,
			Current: timezone, Desired: p.System.Timezone,
		})
	}
}

// portAllowed отвечает, разрешает ли текущий фильтр этот порт.
//
// Сравнение нарочно грубое: правило считается подходящим, если оно
// разрешающее и упоминает тот же порт. Точное совпадение всех полей
// (интерфейсы, источники, зоны) дало бы «расхождение» на каждом хосте,
// где то же самое записано чуть иначе, — а план, который всегда красный,
// перестают читать.
// addCompose сравнивает стеки docker compose: сначала описание, потом то,
// работает ли стек.
//
// Два отдельных пункта, а не один: записать файл и поднять по нему
// контейнеры — разные по цене действия, и оператор должен видеть, что
// именно сейчас произойдёт с работающими сервисами.
func (plan *Plan) addCompose(ctx context.Context, p Profile, r Reader) {
	if len(p.Compose) == 0 {
		return
	}
	// Без docker стек неисполним, и узнать об этом надо до применения, а
	// не из «executable file not found in $PATH» посреди задания.
	// Отдельным пунктом: установка тянет сторонний репозиторий, и решать
	// это за оператора нельзя — галочку он снимет, если ставил docker
	// иначе или не хочет вовсе.
	dockerOK := true
	switch present, err := r.DockerPresent(ctx); {
	case err != nil:
		plan.unknown("docker: %v", err)
	case !present:
		dockerOK = false
		plan.add(Change{Action: ActionInstallDocker, Target: "docker",
			Current: StateMissing, Desired: StateInstalled, Risk: RiskExternalRepo})
	}

	for _, c := range p.Compose {
		path := c.FilePath()
		needWrite := false
		content, ok, err := r.FileContent(ctx, path)
		switch {
		case err != nil:
			plan.unknown("стек %s: %v", c.Name, err)
			continue
		case !ok:
			needWrite = true
			plan.add(Change{Action: ActionWriteCompose, Target: path,
				Current: StateMissing, Desired: StateAsProfile, Detail: c.Content})
		case content != c.Content:
			needWrite = true
			plan.add(Change{Action: ActionWriteCompose, Target: path,
				Current: StateDiffers, Desired: StateAsProfile, Detail: c.Content})
		}

		if !dockerOK {
			// docker ещё предстоит поставить — про работающие
			// контейнеры спрашивать нечего и некого, а поднять стек
			// после установки нужно в любом случае.
			if c.Wanted() {
				plan.add(Change{Action: ActionComposeUp, Target: path,
					Current: StateStopped, Desired: StateRunning})
			}
			continue
		}

		running, err := r.ComposeRunning(ctx, path)
		if err != nil {
			plan.unknown("стек %s: %v", c.Name, err)
			continue
		}
		switch {
		case c.Wanted() && (needWrite || running == 0):
			// Изменённое описание тоже требует подъёма: сам по себе файл
			// работающие контейнеры не трогает.
			plan.add(Change{Action: ActionComposeUp, Target: path,
				Current: composeState(running), Desired: StateRunning})
		case !c.Wanted() && running > 0:
			plan.add(Change{Action: ActionComposeDown, Target: path,
				Current: StateRunning, Desired: StateStopped})
		}
	}
}

func composeState(running int) string {
	if running > 0 {
		return StateRunning
	}
	return StateStopped
}

func portAllowed(state model.FirewallState, want Port) bool {
	proto := want.Proto
	if proto == "" {
		proto = "tcp"
	}
	for _, rule := range state.Rules {
		if !isAllowAction(rule.Action) {
			continue
		}
		if rule.Protocol != "" && !strings.EqualFold(rule.Protocol, proto) {
			continue
		}
		for _, p := range rule.Ports {
			if p == want.Port {
				return true
			}
		}
		if rule.PortSpec == strconv.Itoa(want.Port) {
			return true
		}
	}
	return false
}

func isAllowAction(action string) bool {
	switch strings.ToUpper(action) {
	case "ACCEPT", "ALLOW":
		return true
	}
	return false
}

func portTarget(p Port) string {
	proto := p.Proto
	if proto == "" {
		proto = "tcp"
	}
	if p.From != "" {
		return fmt.Sprintf("%d/%s от %s", p.Port, proto, p.From)
	}
	return fmt.Sprintf("%d/%s", p.Port, proto)
}

// hasKey сравнивает ключи по телу: комментарий в конце строки меняют
// свободно, и «ssh-ed25519 AAAA… ноутбук» и «… рабочий» — один и тот же
// ключ.
func hasKey(have []string, want string) bool {
	wantBody := keyBody(want)
	if wantBody == "" {
		return false
	}
	for _, k := range have {
		if keyBody(k) == wantBody {
			return true
		}
	}
	return false
}

func keyBody(key string) string {
	fields := strings.Fields(key)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// isCriticalService — службы, остановка которых обычно означает потерю
// доступа к машине.
func isCriticalService(name string) bool {
	switch strings.TrimSuffix(name, ".service") {
	case "ssh", "sshd", "systemd-networkd", "NetworkManager", "networking":
		return true
	}
	return false
}

func fileRisk(path string) string {
	if strings.HasPrefix(path, "/etc/ssh/") {
		return RiskSSHAccess
	}
	return ""
}

func enabledWord(v bool) string {
	if v {
		return StateEnabled
	}
	return StateDisabled
}

func activeWord(v bool) string {
	if v {
		return StateRunning
	}
	return StateStopped
}
