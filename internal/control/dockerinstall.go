package control

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"regexp"
	"strings"
)

// Docker ставится не из репозитория дистрибутива, а из docker.com — так
// говорит официальная инструкция, и так же считает сам Docker: в Debian
// и Ubuntu пакет docker.io отстаёт на версии, а compose-плагина в нём
// может не быть вовсе. Профиль со стеком compose без docker неисполним,
// и отправлять оператора ставить его руками значило бы обрывать работу
// на полпути.
//
// Живёт здесь, а не в internal/profile: это действие над хостом, такое
// же, как установка пакета или правка файла, и пригодится не только
// профилю.

// dockerPackages — то, что ставится по официальной инструкции: движок,
// клиент, containerd и два плагина. compose-плагин здесь ключевой — это
// он и есть «docker compose».
const dockerPackages = "docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin"

// codenameRe ограничивает то, что подставляется в строку репозитория:
// значения приходят из /etc/os-release, файла с хоста, и попадают в
// команду оболочки.
var codenameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)

// OSRelease — то немногое из /etc/os-release, что нужно для выбора
// репозитория Docker.
type OSRelease struct {
	ID string
	// Codename — кодовое имя выпуска («bookworm», «noble»).
	Codename string
	// IDLike — семейство («debian» у производных вроде Raspberry Pi OS).
	IDLike string
}

// ReadOSRelease читает /etc/os-release на хосте.
func ReadOSRelease(ctx context.Context, run PrivilegedRunner) (OSRelease, error) {
	if run == nil {
		return OSRelease{}, msgs.Errorf("control.installationUnavailableMode")
	}
	// Через оболочку: os-release — это набор присваиваний, и разбирать
	// его руками незачем, когда его умеет читать сама оболочка.
	// UBUNTU_CODENAME у производных Ubuntu (Mint) содержит имя выпуска,
	// на котором они собраны, — репозиторий Docker знает именно его.
	res, err := run(ctx, "sh", "-c",
		`. /etc/os-release; echo "$ID"; echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}"; echo "$ID_LIKE"`)
	if err != nil {
		return OSRelease{}, err
	}
	if res.ExitCode != 0 {
		return OSRelease{}, msgs.Errorf("control.couldReadEtcOsRelease", strings.TrimSpace(res.Output()))
	}
	lines := strings.Split(strings.ReplaceAll(res.Stdout, "\r", ""), "\n")
	get := func(i int) string {
		if i < len(lines) {
			return strings.TrimSpace(lines[i])
		}
		return ""
	}
	return OSRelease{ID: get(0), Codename: get(1), IDLike: get(2)}, nil
}

// dockerRepoDistro выбирает ветку репозитория Docker: debian или ubuntu.
//
// Производные («Linux Mint» с ID_LIKE=ubuntu, Raspberry Pi OS с
// ID_LIKE=debian) своей ветки в docker.com не имеют и ставятся из ветки
// родителя — так же, как это делает сама официальная инструкция.
// Незнакомая система отдаёт пустую строку: для неё есть другой путь.
func dockerRepoDistro(os OSRelease) string {
	fields := append([]string{os.ID}, strings.Fields(os.IDLike)...)
	for _, f := range fields {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "ubuntu":
			return "ubuntu"
		case "debian", "raspbian":
			return "debian"
		}
	}
	return ""
}

// dockerAptScript — официальная последовательность для Debian и Ubuntu:
// ключ репозитория в /etc/apt/keyrings, сам репозиторий отдельным файлом,
// затем установка пакетов.
func dockerAptScript(distro, codename string) string {
	return strings.Join([]string{
		"set -e",
		"export DEBIAN_FRONTEND=noninteractive",
		"apt-get update",
		"apt-get install -y ca-certificates curl",
		"install -m 0755 -d /etc/apt/keyrings",
		fmt.Sprintf("curl -fsSL https://download.docker.com/linux/%s/gpg -o /etc/apt/keyrings/docker.asc", distro),
		"chmod a+r /etc/apt/keyrings/docker.asc",
		fmt.Sprintf(`echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] `+
			`https://download.docker.com/linux/%s %s stable" > /etc/apt/sources.list.d/docker.list`, distro, codename),
		"apt-get update",
		"apt-get install -y " + dockerPackages,
	}, "\n")
}

// dockerScriptFallback — официальный установочный скрипт docker.com.
//
// Для систем, у которых своей ветки репозитория нет (Alpine, Fedora,
// незнакомый дистрибутив) или не удалось узнать кодовое имя выпуска.
// Скрипт официальный, но Docker сам не советует его для боевых
// установок, поэтому он именно запасной путь, а не основной.
const dockerScriptFallback = "set -e\ncurl -fsSL https://get.docker.com -o /tmp/get-docker.sh\nsh /tmp/get-docker.sh\nrm -f /tmp/get-docker.sh"

// DockerInstallPlan — чем именно будет ставиться docker на этом хосте.
type DockerInstallPlan struct {
	// Official — ставится из репозитория docker.com по официальной
	// инструкции; false — запасным путём через get.docker.com.
	Official bool
	Distro   string
	Codename string
	Script   string
}

// PlanDockerInstall решает, как ставить docker, ничего не меняя.
func PlanDockerInstall(os OSRelease) DockerInstallPlan {
	distro := dockerRepoDistro(os)
	codename := strings.TrimSpace(os.Codename)
	if distro == "" || !codenameRe.MatchString(codename) {
		return DockerInstallPlan{Script: dockerScriptFallback}
	}
	return DockerInstallPlan{
		Official: true, Distro: distro, Codename: codename,
		Script: dockerAptScript(distro, codename),
	}
}

// InstallDocker ставит docker и поднимает его службу.
//
// Команды идут вне песочницы юнита: пишется /etc/apt, ставятся пакеты и
// запускается служба — изнутри юнита туда хода нет.
func InstallDocker(ctx context.Context, run PrivilegedRunner, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if run == nil {
		return msgs.Errorf("control.installationUnavailableMode")
	}

	osr, err := ReadOSRelease(ctx, run)
	if err != nil {
		return err
	}
	plan := PlanDockerInstall(osr)
	if plan.Official {
		logf(msgs.Tc(ctx, "control.dockerInstallOfficial", plan.Distro, plan.Codename))
	} else {
		logf(msgs.Tc(ctx, "control.dockerInstallScript", osr.ID))
	}

	res, err := run(ctx, "sh", "-c", plan.Script)
	if err != nil {
		return msgs.Errorf("control.installingDocker", err)
	}
	for _, line := range tailLines(res.Output(), 5) {
		logf("      %s", line)
	}
	if res.ExitCode != 0 {
		return msgs.Errorf("control.dockerInstallationExitedCode", res.ExitCode, firstProblem(res.Output()))
	}

	// Служба: пакет её включает сам, но на хосте, где apt настроен
	// policy-rc.d (контейнеры, образы для сборки), автозапуск не
	// срабатывает, и docker остаётся установленным, но не работающим.
	if res, err := run(ctx, "systemctl", "enable", "--now", "docker"); err == nil && res.ExitCode != 0 {
		logf(msgs.Tc(ctx, "control.dockerEnableCode", res.ExitCode))
	}

	if res, err := run(ctx, "sh", "-c", "command -v docker"); err != nil || res.ExitCode != 0 {
		return msgs.Errorf("control.dockerDidAppearPATHAfter")
	}
	logf(msgs.Tc(ctx, "control.dockerInstalled"))
	return nil
}

// tailLines отдаёт последние непустые строки вывода — то, чем команда
// закончилась, обычно и объясняет исход.
func tailLines(out string, n int) []string {
	var all []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			all = append(all, line)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// firstProblem выбирает из вывода строку, похожую на причину отказа.
func firstProblem(out string) string {
	var last string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "e:") || strings.Contains(low, "error") || strings.Contains(low, "unable to") {
			return line
		}
		last = line
	}
	if last == "" {
		return msgs.T(msgs.DefaultLang, "control.noOutput")
	}
	return last
}
