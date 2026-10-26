package control

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Снимки и ресурсы машины (1.17.4): то, что владелец машины делает с ней
// сам, — через хаб, в том числе токеном «по правам».

// snapshotNameRe — имя снимка: становится аргументом virsh.
var snapshotNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,47}$`)

// libvirtOK выполняет virsh и пишет журнал действий.
func (m *LibvirtManager) libvirtOK(ctx context.Context, user, action, name string, args ...string) error {
	argv := append([]string{"virsh", "-c", m.cfg.LibvirtURI}, args...)
	res, err := m.run(ctx, argv...)
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "vm."+action, name, outcome, map[string]any{
		"args": strings.Join(args, " "), "exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return fmt.Errorf("virsh %s: %w", args[0], err)
	}
	if !res.OK() {
		return fmt.Errorf("virsh %s %s: %s", args[0], name, virshFailure(res))
	}
	return nil
}

func checkDomain(name string) error {
	if !libvirtDomainRe.MatchString(name) {
		return msgs.Errorf("control.invalidDomainName2", name)
	}
	return nil
}

// Snapshots — снимки машины (имена, старые первыми).
func (m *LibvirtManager) Snapshots(ctx context.Context, name string) ([]string, error) {
	if err := checkDomain(name); err != nil {
		return nil, err
	}
	res, err := m.run(ctx, "virsh", "-c", m.cfg.LibvirtURI, "snapshot-list", name, "--name", "--topological")
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, fmt.Errorf("virsh snapshot-list %s: %s", name, virshFailure(res))
	}
	var out []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// CreateSnapshot делает снимок (внутренний, в qcow2; у запущенной машины —
// вместе с памятью).
func (m *LibvirtManager) CreateSnapshot(ctx context.Context, user, name, snap string) error {
	if err := checkDomain(name); err != nil {
		return err
	}
	if !snapshotNameRe.MatchString(snap) {
		return msgs.Errorf("control.invalidSnapshotName", snap)
	}
	return m.libvirtOK(ctx, user, "snapshot", name, "snapshot-create-as", name, snap, "--atomic")
}

// RevertSnapshot возвращает машину к снимку.
func (m *LibvirtManager) RevertSnapshot(ctx context.Context, user, name, snap string) error {
	if err := checkDomain(name); err != nil {
		return err
	}
	if !snapshotNameRe.MatchString(snap) {
		return msgs.Errorf("control.invalidSnapshotName", snap)
	}
	return m.libvirtOK(ctx, user, "snapshot-revert", name, "snapshot-revert", name, snap)
}

// DeleteSnapshot удаляет снимок.
func (m *LibvirtManager) DeleteSnapshot(ctx context.Context, user, name, snap string) error {
	if err := checkDomain(name); err != nil {
		return err
	}
	if !snapshotNameRe.MatchString(snap) {
		return msgs.Errorf("control.invalidSnapshotName", snap)
	}
	return m.libvirtOK(ctx, user, "snapshot-delete", name, "snapshot-delete", name, snap)
}

// ResizeResult — что изменилось и нужен ли перезапуск (vCPU и память
// меняются в описании домена и вступают после выключения и запуска).
type ResizeResult struct {
	RestartNeeded bool `json:"restart_needed"`
}

// Resize меняет vCPU, память (МБ) и размер диска (ГБ) машины; 0 — не
// менять. Диск только растёт.
func (m *LibvirtManager) Resize(ctx context.Context, user, name string, vcpus int, memMB, diskGB int64) (ResizeResult, error) {
	var out ResizeResult
	if err := checkDomain(name); err != nil {
		return out, err
	}
	if vcpus < 0 || vcpus > 128 || memMB < 0 || (memMB > 0 && memMB < 256) || memMB > 1<<20 || diskGB < 0 || diskGB > 65536 {
		return out, msgs.Errorf("control.invalidResize")
	}
	state, _ := m.run(ctx, "virsh", "-c", m.cfg.LibvirtURI, "domstate", name)
	running := strings.TrimSpace(state.Stdout) == "running"
	// Потолок и текущее значение: при росте сначала потолок, при
	// уменьшении — сначала текущее; первая попытка в «неправильном»
	// порядке просто не проходит.
	both := func(action string, cur, max []string) error {
		_, _ = m.run(ctx, append([]string{"virsh", "-c", m.cfg.LibvirtURI}, cur...)...)
		if err := m.libvirtOK(ctx, user, action, name, max...); err != nil {
			return err
		}
		return m.libvirtOK(ctx, user, action, name, cur...)
	}
	if vcpus > 0 {
		n := strconv.Itoa(vcpus)
		if err := both("resize", []string{"setvcpus", name, n, "--config"}, []string{"setvcpus", name, n, "--config", "--maximum"}); err != nil {
			return out, err
		}
		out.RestartNeeded = running
	}
	if memMB > 0 {
		kib := strconv.FormatInt(memMB*1024, 10)
		if err := both("resize", []string{"setmem", name, kib, "--config"}, []string{"setmaxmem", name, kib, "--config"}); err != nil {
			return out, err
		}
		out.RestartNeeded = running
	}
	if diskGB > 0 {
		path := libvirtImagesRoot + "/" + name + ".qcow2"
		info, err := m.run(ctx, "qemu-img", "info", "-U", "--output=json", path)
		if err != nil || !info.OK() {
			return out, msgs.Errorf("control.diskNotNkt", path)
		}
		var meta struct {
			VirtualSize int64 `json:"virtual-size"`
		}
		_ = json.Unmarshal([]byte(info.Stdout), &meta)
		if want := diskGB << 30; want < meta.VirtualSize {
			return out, msgs.Errorf("control.diskShrink", meta.VirtualSize>>30, diskGB)
		} else if want == meta.VirtualSize {
			return out, nil
		}
		size := strconv.FormatInt(diskGB, 10) + "G"
		if running {
			return out, m.libvirtOK(ctx, user, "resize", name, "blockresize", name, path, "--size", size)
		}
		res, err := m.run(ctx, "qemu-img", "resize", path, size)
		if err != nil {
			return out, err
		}
		if !res.OK() {
			return out, fmt.Errorf("qemu-img resize: %s", virshFailure(res))
		}
	}
	return out, nil
}
