package control

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
)

// На сервере весь софт ставится через apt, и раздел «Пакеты» показывает
// полную картину. На рабочей машине это уже не так: браузер, редактор и
// половина остального приходят из snap или flatpak, и в apt их не видно
// вообще. Раздел показывает и их — иначе список установленного врёт.
//
// Обе системы читаются своими командами; отсутствие любой из них —
// обычное состояние, а не ошибка.

// SandboxPackage — пакет из snap или flatpak.
type SandboxPackage struct {
	Kind    string `json:"kind"` // "snap" | "flatpak"
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// Channel для snap — «latest/stable»; для flatpak это ветка.
	Channel string `json:"channel,omitempty"`
	Origin  string `json:"origin,omitempty"`
	// ID нужен flatpak'у: удаление идёт по идентификатору приложения, а
	// не по человеческому имени.
	ID string `json:"id,omitempty"`
}

// SandboxPackages — то, что отдаётся интерфейсу.
type SandboxPackages struct {
	SnapAvailable    bool             `json:"snap_available"`
	FlatpakAvailable bool             `json:"flatpak_available"`
	Packages         []SandboxPackage `json:"packages"`
	Notes            []string         `json:"notes,omitempty"`
}

// sandboxPkgTimeout — snap и flatpak ходят в сеть даже за списком
// установленного (проверяют обновления), поэтому им нужен свой потолок,
// заметно больше обычного командного.
const sandboxPkgTimeout = 90 * time.Second

// SandboxPkgManager читает и удаляет пакеты snap/flatpak.
type SandboxPkgManager struct {
	c collect.Collector
}

func NewSandboxPkgManager(c collect.Collector) *SandboxPkgManager { return &SandboxPkgManager{c: c} }

// List собирает установленное из обеих систем.
func (m *SandboxPkgManager) List(ctx context.Context) SandboxPackages {
	ctx, cancel := context.WithTimeout(ctx, sandboxPkgTimeout)
	defer cancel()

	out := SandboxPackages{Packages: []SandboxPackage{}}

	if collect.Which(ctx, m.c, "snap") {
		out.SnapAvailable = true
		if res, err := m.c.Run(ctx, "snap", "list"); err == nil && res.ExitCode == 0 {
			out.Packages = append(out.Packages, parseSnapList(res.Stdout)...)
		} else {
			out.Notes = append(out.Notes, msgs.Tc(ctx, "control.snapListFailed", commandError(ctx, res, err)))
		}
	}
	if collect.Which(ctx, m.c, "flatpak") {
		out.FlatpakAvailable = true
		if res, err := m.c.Run(ctx, "flatpak", "list", "--columns=application,name,version,branch,origin"); err == nil && res.ExitCode == 0 {
			out.Packages = append(out.Packages, parseFlatpakList(res.Stdout)...)
		} else {
			out.Notes = append(out.Notes, msgs.Tc(ctx, "control.flatpakListFailed", commandError(ctx, res, err)))
		}
	}
	if !out.SnapAvailable && !out.FlatpakAvailable {
		out.Notes = append(out.Notes, msgs.Tc(ctx, "control.noSnapNoFlatpak"))
	}
	return out
}

// parseSnapList разбирает табличный вывод `snap list`: имя, версия,
// ревизия, канал, издатель, примечания. Разделитель — пробелы, поэтому
// разбор идёт по полям, а не по позициям.
func parseSnapList(out string) []SandboxPackage {
	var list []SandboxPackage
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) < 5 {
			continue
		}
		list = append(list, SandboxPackage{
			Kind: "snap", Name: fields[0], Version: fields[1],
			Channel: fields[3], Origin: fields[4], ID: fields[0],
		})
	}
	return list
}

// parseFlatpakList разбирает `flatpak list --columns=...`: колонки
// разделены табуляцией, заголовка нет.
func parseFlatpakList(out string) []SandboxPackage {
	var list []SandboxPackage
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		list = append(list, SandboxPackage{
			Kind: "flatpak", ID: strings.TrimSpace(f[0]), Name: strings.TrimSpace(f[1]),
			Version: strings.TrimSpace(f[2]), Channel: strings.TrimSpace(f[3]),
			Origin: strings.TrimSpace(f[4]),
		})
	}
	return list
}

// sandboxNameRe — имя пакета уходит в команду удаления.
var sandboxNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]*$`)

// Remove удаляет пакет. Kind решает, чем именно: snap и flatpak — разные
// системы с разными именами одного и того же приложения.
func (m *SandboxPkgManager) Remove(ctx context.Context, kind, name string) error {
	if !sandboxNameRe.MatchString(name) {
		return msgs.Errorf("control.invalidPackageName", name)
	}
	ctx, cancel := context.WithTimeout(ctx, sandboxPkgTimeout)
	defer cancel()

	var res collect.CommandResult
	var err error
	switch kind {
	case "snap":
		res, err = m.c.Run(ctx, "snap", "remove", name)
	case "flatpak":
		res, err = m.c.Run(ctx, "flatpak", "uninstall", "-y", name)
	default:
		return msgs.Errorf("control.unknownPackageKind", kind)
	}
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: %s", kind, strings.TrimSpace(res.Output()))
	}
	return nil
}

// Update обновляет всё установленное в одной из двух систем. Отдельной
// командой на систему: у snap обновление называется refresh, у flatpak —
// update, и общего пути у них нет.
func (m *SandboxPkgManager) Update(ctx context.Context, kind string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, sandboxPkgTimeout)
	defer cancel()

	var res collect.CommandResult
	var err error
	switch kind {
	case "snap":
		res, err = m.c.Run(ctx, "snap", "refresh")
	case "flatpak":
		res, err = m.c.Run(ctx, "flatpak", "update", "-y")
	default:
		return "", msgs.Errorf("control.unknownPackageKind", kind)
	}
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("%s: %s", kind, strings.TrimSpace(res.Output()))
	}
	return strings.TrimSpace(res.Output()), nil
}
