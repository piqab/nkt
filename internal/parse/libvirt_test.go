package parse

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// TestLibvirtNeverReturnsNilSlices guards against a real production crash:
// a host without libvirt must still get real empty slices back, not nil —
// neither field has `omitempty`, encoding/json marshals nil as `null`, and
// the VMs page crashes calling .map on `null`.
func TestLibvirtNeverReturnsNilSlices(t *testing.T) {
	res := Libvirt(context.Background(), collect.NewFixtures(t.TempDir()), "qemu:///system")
	if res.VMs == nil {
		t.Error("VMs = nil, ожидался непустой (даже если пустой) срез")
	}
	if res.Files == nil {
		t.Error("Files = nil, ожидался непустой (даже если пустой) срез")
	}
}

func TestLibvirtListsDomains(t *testing.T) {
	res := Libvirt(context.Background(), fixtureCollector(t), "qemu:///system")
	if res.Status.Error != "" {
		t.Fatalf("парсер вернул ошибку: %s", res.Status.Error)
	}
	if !res.Status.Available {
		t.Fatal("Status.Available = false, ожидалось true")
	}
	if len(res.Status.Warnings) != 0 {
		t.Errorf("Warnings = %v, ожидался пустой список", res.Status.Warnings)
	}
	if len(res.VMs) != 2 {
		t.Fatalf("len(VMs) = %d, ожидалось 2", len(res.VMs))
	}

	byName := map[string]int{}
	for i, vm := range res.VMs {
		byName[vm.Name] = i
	}
	web, ok := byName["web-vm"]
	if !ok {
		t.Fatal("web-vm не найден")
	}
	vm := res.VMs[web]
	if vm.State != "running" {
		t.Errorf("web-vm: State = %q, ожидалось running", vm.State)
	}
	if !vm.Persistent {
		t.Error("web-vm: Persistent = false, ожидалось true")
	}
	if !vm.Autostart {
		t.Error("web-vm: Autostart = false, ожидалось true")
	}
	if vm.VCPUs != 2 {
		t.Errorf("web-vm: VCPUs = %d, ожидалось 2", vm.VCPUs)
	}
	if vm.MemoryKB != 2097152 {
		t.Errorf("web-vm: MemoryKB = %d, ожидалось 2097152", vm.MemoryKB)
	}
	if vm.UUID != "4b3f9b1e-1234-4a2b-9c3d-abcdef123456" {
		t.Errorf("web-vm: UUID = %q", vm.UUID)
	}
	if len(vm.Disks) != 1 || vm.Disks[0].Bus != "virtio" || vm.Disks[0].Source != "/var/lib/libvirt/images/web-vm.qcow2" {
		t.Errorf("web-vm: Disks = %+v", vm.Disks)
	}
	if len(vm.Networks) != 1 || vm.Networks[0].Source != "br0" || vm.Networks[0].MAC != "52:54:00:12:34:56" {
		t.Errorf("web-vm: Networks = %+v", vm.Networks)
	}

	db, ok := byName["db-vm"]
	if !ok {
		t.Fatal("db-vm не найден")
	}
	if res.VMs[db].State != "shut off" {
		t.Errorf("db-vm: State = %q, ожидалось \"shut off\"", res.VMs[db].State)
	}
	if res.VMs[db].Autostart {
		t.Error("db-vm: Autostart = true, ожидалось false")
	}

	// Both domains are persistent, so both should surface as editable files
	// under LibvirtQEMUDir — this is what lets "Конфигурации" list them.
	if len(res.Files) != 2 {
		t.Fatalf("len(Files) = %d, ожидалось 2", len(res.Files))
	}
	for _, f := range res.Files {
		if f.Service != "libvirt" {
			t.Errorf("файл %s: Service = %q, ожидалось libvirt", f.Path, f.Service)
		}
		if !f.Readable {
			t.Errorf("файл %s: Readable = false", f.Path)
		}
	}
}

func TestToKB(t *testing.T) {
	cases := []struct {
		value int64
		unit  string
		want  int64
	}{
		{2097152, "KiB", 2097152},
		{2048, "MiB", 2048 * 1024},
		{2, "GiB", 2 * 1024 * 1024},
		{2097152, "", 2097152},
	}
	for _, c := range cases {
		if got := toKB(c.value, c.unit); got != c.want {
			t.Errorf("toKB(%d, %q) = %d, ожидалось %d", c.value, c.unit, got, c.want)
		}
	}
}
