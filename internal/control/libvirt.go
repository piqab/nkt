package control

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

// LibvirtManager performs lifecycle actions against libvirt/QEMU domains via
// `virsh`. Creating and editing a domain's definition goes through
// ConfigManager instead (see its serviceForPath/Validate/apply-step
// handling of LibvirtQEMUDir) — this manager only covers what is not a
// config-file write: starting, stopping, toggling autostart, and removing
// a domain's definition entirely.
type LibvirtManager struct {
	cfg     *config.Config
	c       collect.Collector
	db      *store.DB
	scanner *inventory.Scanner
	escape  PrivilegedRunner
}

// NewLibvirtManager builds the libvirt control plane. escape может быть
// nil — тогда команды идут обычным путём (fixtures-режим, тесты).
func NewLibvirtManager(cfg *config.Config, c collect.Collector, db *store.DB,
	scanner *inventory.Scanner, escape PrivilegedRunner) *LibvirtManager {
	return &LibvirtManager{cfg: cfg, c: c, db: db, scanner: scanner, escape: escape}
}

// run выполняет команду, меняющую состояние libvirt.
//
// Через выход из песочницы, если он доступен: собственный юнит nkt живёт
// с ProtectSystem=strict и PrivateTmp, а virsh лезет и к сокету libvirt,
// и к файлам дисков в /var/lib/libvirt. Изнутри песочницы удаление
// домена молча не доходит до цели — ровно та жалоба, с которой «кнопка
// удалить ничего не удаляет».
func (m *LibvirtManager) run(ctx context.Context, argv ...string) (collect.CommandResult, error) {
	if m.escape != nil {
		return m.escape(ctx, argv...)
	}
	return m.c.Run(ctx, argv[0], argv[1:]...)
}

// virshFailure собирает причину отказа virsh.
//
// Не первая строка: заголовок вроде «Failed to start domain» virsh пишет
// первым, а причину — следующей строкой, и показывать только заголовок
// значит каждый раз выбрасывать самое нужное.
func virshFailure(res collect.CommandResult) string {
	var lines []string
	for _, line := range strings.Split(res.Output(), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "error: ")
		line = strings.TrimPrefix(line, "ошибка: ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return fmt.Sprintf("код %d", res.ExitCode)
	}
	if len(lines) > 3 {
		lines = lines[:3]
	}
	return strings.Join(lines, "; ")
}

// libvirtDomainRe accepts libvirt domain names: letters, digits, and the
// punctuation libvirt itself allows in a name, nothing that could be
// interpreted as a shell or virsh option.
var libvirtDomainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,63})?$`)

// libvirtImagesRoot is qemu/KVM's conventional disk image directory — the
// same default the domain-XML skeleton (web/src/pages/Virtualization.tsx)
// already points new disks at. CreateDisk is sandboxed to it for the same
// reason config edits are sandboxed to specific roots: an operator-supplied
// path must not be able to write a qcow2 header over an arbitrary file.
const libvirtImagesRoot = "/var/lib/libvirt/images"

// CreateDisk provisions a new qcow2 disk image via qemu-img — the piece a
// hand-written or wizard-generated domain XML never does on its own: the
// XML only ever references a disk path, it never creates the file, so a VM
// booted against a path with nothing there fails immediately. Only paths
// under libvirtImagesRoot are accepted.
func (m *LibvirtManager) CreateDisk(ctx context.Context, user, path string, sizeGB int) error {
	if !strings.HasPrefix(path, libvirtImagesRoot+"/") || strings.Contains(path, "..") {
		return fmt.Errorf("путь диска должен быть внутри %s", libvirtImagesRoot)
	}
	if sizeGB <= 0 || sizeGB > 65536 {
		return fmt.Errorf("недопустимый размер диска: %d ГБ", sizeGB)
	}

	res, err := m.run(ctx, "qemu-img", "create", "-f", "qcow2", path, fmt.Sprintf("%dG", sizeGB))
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "vm.create_disk", path, outcome, map[string]any{
		"size_gb": sizeGB, "exit_code": res.ExitCode,
		"output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("qemu-img create %s: %w", path, err)
	}
	if !res.OK() {
		return fmt.Errorf("qemu-img create %s: %s", path, virshFailure(res))
	}
	return nil
}

// VMAction runs a lifecycle action against a domain. "stop" is deliberately
// not one of the accepted values: shutdown (graceful, ACPI) and destroy
// (immediate power-off) have different blast radii and this application
// keeps that distinction explicit everywhere else it applies, rather than
// silently picking one for a generic "stop".
func (m *LibvirtManager) VMAction(ctx context.Context, user, name, action string) error {
	switch action {
	case "start", "shutdown", "destroy", "reboot", "suspend", "resume":
	default:
		return fmt.Errorf("недопустимое действие для VM: %q", action)
	}
	if !libvirtDomainRe.MatchString(name) {
		return fmt.Errorf("недопустимое имя домена: %q", name)
	}

	res, err := m.run(ctx, "virsh", "-c", m.cfg.LibvirtURI, action, name)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "vm."+action, name, outcome, map[string]any{
		"exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("virsh %s %s: %w", action, name, err)
	}
	if !res.OK() {
		return fmt.Errorf("virsh %s %s: %s", action, name, virshFailure(res))
	}
	return nil
}

// SetAutostart toggles whether libvirtd starts a domain automatically at
// host boot.
func (m *LibvirtManager) SetAutostart(ctx context.Context, user, name string, on bool) error {
	if !libvirtDomainRe.MatchString(name) {
		return fmt.Errorf("недопустимое имя домена: %q", name)
	}

	args := []string{"-c", m.cfg.LibvirtURI, "autostart", name}
	action := "autostart-on"
	if !on {
		args = append(args, "--disable")
		action = "autostart-off"
	}
	res, err := m.run(ctx, append([]string{"virsh"}, args...)...)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "vm."+action, name, outcome, map[string]any{
		"exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("virsh autostart %s: %w", name, err)
	}
	if !res.OK() {
		return fmt.Errorf("virsh autostart %s: %s", name, virshFailure(res))
	}
	return nil
}

// UndefineVM removes a domain's persistent definition. removeStorage also
// deletes its disk images — an explicit, separately-confirmed flag, never a
// default, since that step is irreversible. A running domain is refused
// rather than implicitly destroyed first: the operator must stop it as a
// deliberate, visible step before deleting it.
//
// force снимает этот отказ: домен гасится тут же, перед удалением. Так
// удаляет хаб, когда машину убирают целиком — там выключение уже не
// отдельный шаг оператора, а часть решения «этой машины больше нет».
// Состояние при этом берётся у самого libvirt, а не из снимка инвентаря:
// снимок мог устареть на минуту, и тогда отказ «домен запущен» приходил
// бы на давно погашенный домен.
func (m *LibvirtManager) UndefineVM(ctx context.Context, user, name string, removeStorage, force bool) error {
	if !libvirtDomainRe.MatchString(name) {
		return fmt.Errorf("недопустимое имя домена: %q", name)
	}
	if force {
		// Отказ гасить незапущенный домен — не ошибка: он уже в нужном
		// состоянии. Остальные разберёт сам undefine ниже.
		res, err := m.run(ctx, "virsh", "-c", m.cfg.LibvirtURI, "destroy", name)
		if err == nil && !res.OK() {
			m.db.Audit(ctx, user, "vm.destroy", name, "error", map[string]any{
				"exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
			})
		}
	} else if snap := m.scanner.Latest(); snap != nil {
		for _, vm := range snap.VMs {
			if vm.Name == name && vm.State == "running" {
				return fmt.Errorf("домен %s запущен — сначала остановите его (shutdown/destroy)", name)
			}
		}
	}

	args := []string{"-c", m.cfg.LibvirtURI, "undefine", name}
	if removeStorage {
		args = append(args, "--remove-all-storage")
	}
	res, err := m.run(ctx, append([]string{"virsh"}, args...)...)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "vm.undefine", name, outcome, map[string]any{
		"remove_storage": removeStorage, "exit_code": res.ExitCode,
		"output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("virsh undefine %s: %w", name, err)
	}
	if !res.OK() {
		return fmt.Errorf("virsh undefine %s: %s", name, virshFailure(res))
	}
	return nil
}
