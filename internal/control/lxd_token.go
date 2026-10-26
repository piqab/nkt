package control

import (
	"context"
	"strconv"

	"github.com/piqab/nkt/internal/msgs"
)

// Экземпляры LXD для токенов (1.17.7): сеть токена, потолки vCPU, памяти
// и диска, без привилегий и вложенности — как машины KVM в 1.17.3–1.17.4.

// LXDLaunchOpts — что сверх образа и имени задаёт запуск.
type LXDLaunchOpts struct {
	// Network — сеть LXD вместо сети профиля (у токена — его сеть).
	Network string `json:"network,omitempty"`
	CPU     int    `json:"cpu,omitempty"`
	MemMB   int64  `json:"mem_mb,omitempty"`
	DiskGB  int64  `json:"disk_gb,omitempty"`
	// Isolated — экземпляр токена: явно без привилегий и вложенности, что
	// бы ни было в профиле по умолчанию.
	Isolated bool `json:"isolated,omitempty"`
}

// LXDLaunchArgsOpts — LXDLaunchArgs с сетью и потолками.
func LXDLaunchArgsOpts(image, name string, vm bool, o LXDLaunchOpts) ([]string, error) {
	args, err := LXDLaunchArgs(image, name, vm)
	if err != nil {
		return nil, err
	}
	if o.Network != "" {
		if !lxdNetNameRe.MatchString(o.Network) {
			return nil, msgs.Errorf("control.lxdBadNetwork", o.Network)
		}
		args = append(args, "--network", o.Network)
	}
	if o.CPU < 0 || o.CPU > 128 || o.MemMB < 0 || (o.MemMB > 0 && o.MemMB < 128) || o.MemMB > 1<<20 || o.DiskGB < 0 || o.DiskGB > 65536 {
		return nil, msgs.Errorf("control.invalidResize")
	}
	if o.CPU > 0 {
		args = append(args, "-c", "limits.cpu="+strconv.Itoa(o.CPU))
	}
	if o.MemMB > 0 {
		args = append(args, "-c", "limits.memory="+strconv.FormatInt(o.MemMB, 10)+"MiB")
	}
	if o.DiskGB > 0 {
		args = append(args, "-d", "root,size="+strconv.FormatInt(o.DiskGB, 10)+"GiB")
	}
	if o.Isolated {
		args = append(args, "-c", "security.privileged=false", "-c", "security.nesting=false")
	}
	return args, nil
}

// SetLimits меняет vCPU и память экземпляра (сразу, без перезапуска) и
// размер корневого диска (только больше); 0 — не менять.
func (m *LXDManager) SetLimits(ctx context.Context, user, name string, cpu int, memMB, diskGB int64) error {
	if !lxdInstanceNameRe.MatchString(name) {
		return msgs.Errorf("control.invalidInstanceName", name)
	}
	if _, err := LXDLaunchArgsOpts("x", name, false, LXDLaunchOpts{CPU: cpu, MemMB: memMB, DiskGB: diskGB}); err != nil {
		return err
	}
	if cpu > 0 || memMB > 0 {
		args := []string{"config", "set", name}
		if cpu > 0 {
			args = append(args, "limits.cpu="+strconv.Itoa(cpu))
		}
		if memMB > 0 {
			args = append(args, "limits.memory="+strconv.FormatInt(memMB, 10)+"MiB")
		}
		if err := m.runAudited(ctx, user, "lxd.limits", name, args...); err != nil {
			return err
		}
	}
	if diskGB > 0 {
		size := "size=" + strconv.FormatInt(diskGB, 10) + "GiB"
		// Свой root у экземпляра — set; root из профиля — override.
		if err := m.runAudited(ctx, user, "lxd.disk", name, "config", "device", "set", name, "root", size); err != nil {
			return m.runAudited(ctx, user, "lxd.disk", name, "config", "device", "override", name, "root", size)
		}
	}
	return nil
}

// EnsureNetwork заводит мост LXD с NAT (IPv4 — сам, без IPv6), если сети с
// таким именем нет.
func (m *LXDManager) EnsureNetwork(ctx context.Context, user, name string) (created bool, err error) {
	nets, err := m.ListNetworks(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range nets {
		if n.Name == name {
			return false, nil
		}
	}
	if !lxdNetNameRe.MatchString(name) {
		return false, msgs.Errorf("control.lxdBadNetwork", name)
	}
	if err := m.runAudited(ctx, user, "lxd.network.create", name, "network", "create", name, "ipv4.address=auto", "ipv4.nat=true", "ipv6.address=none"); err != nil {
		return false, err
	}
	return true, nil
}
