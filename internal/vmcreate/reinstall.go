package vmcreate

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/vmimage"
	"github.com/piqab/nkt/internal/vmnet"
)

// KindReinstall — вид задания «переустановить машину» (1.17.4): домен
// остаётся (имя, сеть, vCPU, память, MAC), диск и настройки первого
// запуска делаются заново из образа. Снимки уходят вместе со старым
// диском.
const KindReinstall = "vm.reinstall"

// ReinstallParams — вход задания. Spec.VCPUs, MemoryMB и DiskGB — 0:
// взять у домена и у старого диска.
type ReinstallParams struct {
	Spec Spec `json:"spec"`
}

// ReinstallRunner переустанавливает машину, созданную nkt.
type ReinstallRunner struct{ c *CreateRunner }

// NewReinstallRunner строит исполнителя на том же окружении, что создание.
func NewReinstallRunner(c *CreateRunner) *ReinstallRunner { return &ReinstallRunner{c: c} }

// Resumable — нет: старый диск к этому моменту может быть уже удалён, и
// продолжать с середины значит гадать; задание повторяют целиком.
func (r *ReinstallRunner) Resumable() bool { return false }

// domainSize — vCPU и память из описания домена (XML не переводится, в
// отличие от virsh dominfo).
type domainSize struct {
	VCPU   int `xml:"vcpu"`
	Memory struct {
		Unit  string `xml:"unit,attr"`
		Value int64  `xml:",chardata"`
	} `xml:"memory"`
}

// Run проходит шаги переустановки.
func (r *ReinstallRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ReinstallParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	c := r.c
	if c.escape == nil {
		return msgs.Errorf("vmcreate.machineCreationUnavailableMode")
	}
	spec := p.Spec
	diskPath := filepath.Join(imagesRoot, spec.Name+".qcow2")
	seedPath := filepath.Join(imagesRoot, spec.Name+"-seed.iso")

	// 1. Домен и его диск: переустанавливается только машина nkt — диск
	// по имени машины в каталоге дисков libvirt.
	jc.StepKey(1, 4, "vmcreate.stepReinstallCheck")
	if !nameRe.MatchString(spec.Name) {
		return msgs.Errorf("vmcreate.machineNameLowercaseLatinLetters")
	}
	blk, err := c.run(ctx, "virsh", "domblklist", spec.Name)
	if err != nil {
		return err
	}
	if blk.ExitCode != 0 {
		return fmt.Errorf("virsh domblklist: %s", commandError(blk.Stderr, blk.Stdout))
	}
	if !strings.Contains(blk.Stdout, diskPath) {
		return msgs.Errorf("vmcreate.reinstallNotNkt", spec.Name, diskPath)
	}
	if spec.VCPUs == 0 || spec.MemoryMB == 0 {
		dump, err := c.run(ctx, "virsh", "dumpxml", "--inactive", spec.Name)
		if err != nil || dump.ExitCode != 0 {
			return msgs.Errorf("vmcreate.reinstallNotNkt", spec.Name, diskPath)
		}
		var d domainSize
		_ = xml.Unmarshal([]byte(dump.Stdout), &d)
		if spec.VCPUs == 0 {
			spec.VCPUs = d.VCPU
		}
		if spec.MemoryMB == 0 {
			mem := d.Memory.Value / 1024
			switch strings.ToLower(d.Memory.Unit) {
			case "mib", "m":
				mem = d.Memory.Value
			case "gib", "g":
				mem = d.Memory.Value * 1024
			}
			spec.MemoryMB = int(mem)
		}
	}
	if spec.DiskGB == 0 {
		info, err := c.run(ctx, "qemu-img", "info", "-U", "--output=json", diskPath)
		if err == nil && info.ExitCode == 0 {
			var meta struct {
				VirtualSize int64 `json:"virtual-size"`
			}
			_ = json.Unmarshal([]byte(info.Stdout), &meta)
			spec.DiskGB = int((meta.VirtualSize + 1<<30 - 1) >> 30)
		}
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	img, err := resolveImage(c.store, spec.ImageID)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(spec.ImageID, HostPrefix) && !c.store.Have(img) {
		jc.Log("vmcreate.imagePlace", img.Name)
		if _, err := c.store.Download(ctx, img, func(pr vmimage.Progress) {
			if pr.Total > 0 {
				jc.Log("vmcreate.downloadingImage", pr.Done*100/pr.Total)
			}
		}); err != nil {
			return msgs.Errorf("vmcreate.image", err)
		}
	}

	// 2. Выключение, снимки (их метаданные; сами снимки — в старом диске)
	// и старые файлы.
	jc.StepKey(2, 4, "vmcreate.stepReinstallWipe")
	_, _ = c.run(ctx, "virsh", "destroy", spec.Name)
	if list, err := c.run(ctx, "virsh", "snapshot-list", spec.Name, "--name"); err == nil && list.ExitCode == 0 {
		for _, snap := range strings.Fields(list.Stdout) {
			_, _ = c.run(ctx, "virsh", "snapshot-delete", spec.Name, snap, "--metadata")
		}
	}
	if res, err := c.run(ctx, "rm", "-f", diskPath, seedPath); err != nil {
		return err
	} else if res.ExitCode != 0 {
		return fmt.Errorf("rm: %s", commandError(res.Stderr, res.Stdout))
	}

	// 3. Новый диск и настройки первого запуска.
	jc.StepKey(3, 4, "vmcreate.stepDisk")
	if err := c.makeDisk(ctx, jc, c.imagePath(img), diskPath, spec.DiskGB); err != nil {
		return err
	}
	if err := c.makeSeed(ctx, jc, spec, seedPath); err != nil {
		return err
	}

	// 4. Запуск (сеть токена — сначала правила).
	jc.StepKey(4, 4, "vmcreate.stepStart")
	if spec.Isolated || vmnet.IsIsolated(spec.Network) {
		if err := c.ensureNetwork(ctx, jc, spec.Network); err != nil {
			return err
		}
		if err := c.isolate(ctx, jc, spec.Network); err != nil {
			return err
		}
	}
	if res, err := c.run(ctx, "virsh", "start", spec.Name); err != nil {
		return err
	} else if res.ExitCode != 0 {
		return fmt.Errorf("virsh start: %s", commandError(res.Stderr, res.Stdout))
	}
	jc.Log("vmcreate.machineReinstalled", spec.Name)
	if addr := c.waitAddress(ctx, jc, spec.Name); addr != "" {
		jc.Log("vmcreate.machineAddress", addr)
	} else {
		jc.Log("vmcreate.addressKnownYetMachineStill")
	}
	return nil
}
