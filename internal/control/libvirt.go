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
}

// NewLibvirtManager builds the libvirt control plane.
func NewLibvirtManager(cfg *config.Config, c collect.Collector, db *store.DB, scanner *inventory.Scanner) *LibvirtManager {
	return &LibvirtManager{cfg: cfg, c: c, db: db, scanner: scanner}
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

	res, err := m.c.Run(ctx, "qemu-img", "create", "-f", "qcow2", path, fmt.Sprintf("%dG", sizeGB))
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
		return fmt.Errorf("qemu-img create %s: код %d: %s", path, res.ExitCode, strings.TrimSpace(res.Output()))
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

	res, err := m.c.Run(ctx, "virsh", "-c", m.cfg.LibvirtURI, action, name)
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
		return fmt.Errorf("virsh %s %s: код %d: %s", action, name, res.ExitCode, strings.TrimSpace(res.Output()))
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
	res, err := m.c.Run(ctx, "virsh", args...)
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
		return fmt.Errorf("virsh autostart %s: код %d: %s", name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}

// UndefineVM removes a domain's persistent definition. removeStorage also
// deletes its disk images — an explicit, separately-confirmed flag, never a
// default, since that step is irreversible. A running domain is refused
// rather than implicitly destroyed first: the operator must stop it as a
// deliberate, visible step before deleting it.
func (m *LibvirtManager) UndefineVM(ctx context.Context, user, name string, removeStorage bool) error {
	if !libvirtDomainRe.MatchString(name) {
		return fmt.Errorf("недопустимое имя домена: %q", name)
	}
	if snap := m.scanner.Latest(); snap != nil {
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
	res, err := m.c.Run(ctx, "virsh", args...)
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
		return fmt.Errorf("virsh undefine %s: код %d: %s", name, res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return nil
}
