package control

import (
	"context"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// Ветка репозитория выбирается по системе, а производные ставятся из
// ветки родителя — так же, как это делает сама официальная инструкция.
func TestDockerRepoDistro(t *testing.T) {
	cases := map[string]OSRelease{
		"debian": {ID: "debian", Codename: "bookworm"},
		"ubuntu": {ID: "ubuntu", Codename: "noble"},
	}
	for want, osr := range cases {
		if got := dockerRepoDistro(osr); got != want {
			t.Errorf("%+v → %q, ожидалось %q", osr, got, want)
		}
	}
	// Производные своей ветки не имеют: Mint ставится из ubuntu,
	// Raspberry Pi OS — из debian.
	if got := dockerRepoDistro(OSRelease{ID: "linuxmint", IDLike: "ubuntu", Codename: "jammy"}); got != "ubuntu" {
		t.Errorf("Mint → %q", got)
	}
	if got := dockerRepoDistro(OSRelease{ID: "raspbian", IDLike: "debian", Codename: "bookworm"}); got != "debian" {
		t.Errorf("Raspberry Pi OS → %q", got)
	}
	if got := dockerRepoDistro(OSRelease{ID: "fedora", IDLike: "rhel"}); got != "" {
		t.Errorf("незнакомая система → %q, а должна уйти на запасной путь", got)
	}
}

// Основной путь — официальная последовательность docker.com: ключ в
// /etc/apt/keyrings, репозиторий отдельным файлом, пакеты движка и обоих
// плагинов. Без compose-плагина «docker compose» не существует, и стек из
// профиля не поднять.
func TestPlanDockerInstallOfficialApt(t *testing.T) {
	plan := PlanDockerInstall(OSRelease{ID: "debian", Codename: "bookworm"})
	if !plan.Official {
		t.Fatalf("для Debian выбран запасной путь: %+v", plan)
	}
	for _, want := range []string{
		"https://download.docker.com/linux/debian/gpg",
		"/etc/apt/keyrings/docker.asc",
		"https://download.docker.com/linux/debian bookworm stable",
		"docker-compose-plugin",
	} {
		if !strings.Contains(plan.Script, want) {
			t.Errorf("в сценарии нет %q:\n%s", want, plan.Script)
		}
	}
}

// Незнакомая система и подозрительное кодовое имя уходят на официальный
// скрипт: подставлять в строку репозитория что попало нельзя — она
// целиком попадает в команду оболочки.
func TestPlanDockerInstallFallback(t *testing.T) {
	for _, osr := range []OSRelease{
		{ID: "fedora", Codename: "thirtynine"},
		{ID: "debian", Codename: "bookworm; rm -rf /"},
		{ID: "ubuntu", Codename: ""},
	} {
		plan := PlanDockerInstall(osr)
		if plan.Official {
			t.Errorf("%+v пошла официальным путём, хотя не должна", osr)
		}
		if !strings.Contains(plan.Script, "https://get.docker.com") {
			t.Errorf("%+v: запасной путь не через get.docker.com:\n%s", osr, plan.Script)
		}
		if strings.Contains(plan.Script, "rm -rf /") {
			t.Errorf("%+v: в сценарий просочилось значение из os-release", osr)
		}
	}
}

// Debian 13: docker.io ставит один демон, клиент — в docker-cli. Когда
// dockerd на хосте уже есть, установка доставляет только клиент и
// compose-плагин из репозитория дистрибутива, а не docker-ce поверх.
func TestInstallDockerCLIOnly(t *testing.T) {
	var scripts []string
	run := func(ctx context.Context, argv ...string) (collect.CommandResult, error) {
		cmd := strings.Join(argv, " ")
		switch {
		case strings.Contains(cmd, "command -v docker >/dev/null 2>&1 && exit 1"):
			return collect.CommandResult{ExitCode: 0}, nil // dockerd есть, docker нет
		case cmd == "sh -c command -v apt-get":
			return collect.CommandResult{ExitCode: 0, Stdout: "/usr/bin/apt-get\n"}, nil
		case strings.HasPrefix(cmd, "sh -c set -e"):
			scripts = append(scripts, argv[2])
			return collect.CommandResult{ExitCode: 0}, nil
		case cmd == "sh -c command -v docker":
			return collect.CommandResult{ExitCode: 0, Stdout: "/usr/bin/docker\n"}, nil
		}
		return collect.CommandResult{ExitCode: 0}, nil
	}
	if err := InstallDocker(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || !strings.Contains(scripts[0], "apt-get install -y docker-cli docker-compose") || strings.Contains(scripts[0], "docker-ce") {
		t.Errorf("ожидалась установка только клиента, выполнено: %q", scripts)
	}
}
