package control

import (
	"context"
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/msgs"
)

const newVMFile = "/etc/libvirt/qemu/new-vm.xml"

const newVMXML = `<domain type='kvm'>
  <name>new-vm</name>
  <memory unit='KiB'>1048576</memory>
  <vcpu placement='static'>1</vcpu>
</domain>
`

// TestWriteCreatesAndDefinesNewVM covers the key architectural point of this
// feature: creating a VM has no dedicated API, it goes through the same
// validated-write path every other config file uses. A brand-new file under
// LibvirtQEMUDir must be recognised (serviceForPath), validated via
// virt-xml-validate, written to disk, and registered via `virsh define` —
// all from one Write call with apply=true.
func TestWriteCreatesAndDefinesNewVM(t *testing.T) {
	root := copyFixturesRoot(t)
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	m := configsSetupWithCollector(t, root, rec)

	res, err := m.Write(context.Background(), msgs.RU, "test", newVMFile, newVMXML, "новая VM", true)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.RolledBack {
		t.Fatalf("RolledBack = true, ожидалось false: %+v", res)
	}
	if !res.Validated {
		t.Error("Validated = false, ожидалось true (virt-xml-validate доступен в фикстурах)")
	}
	if !res.Applied {
		t.Errorf("Applied = false, ожидалось true: %+v", res)
	}

	sawValidate, sawDefine := false, false
	for _, argv := range rec.calls {
		joined := strings.Join(argv, " ")
		if strings.HasPrefix(joined, "virt-xml-validate "+newVMFile) {
			sawValidate = true
		}
		if joined == "virsh -c qemu:///system define "+newVMFile {
			sawDefine = true
		}
	}
	if !sawValidate {
		t.Errorf("virt-xml-validate не вызван, звонки: %v", rec.calls)
	}
	if !sawDefine {
		t.Errorf("virsh define не вызван, звонки: %v", rec.calls)
	}

	got, err := m.Read(newVMFile)
	if err != nil {
		t.Fatalf("Read после записи: %v", err)
	}
	if got.Content != newVMXML {
		t.Errorf("содержимое файла не совпадает с записанным")
	}
	if got.Service != "libvirt" {
		t.Errorf("Service = %q, ожидалось libvirt", got.Service)
	}
}

// TestWriteNewVMWithoutApplyLeavesFileUndefined covers the "apply" checkbox
// being off: the XML lands on disk (staged), but virsh define is never
// called, so libvirtd never actually registers the domain — that is a
// deliberate consequence of reusing the generic config-write apply step,
// not a bug, and it must stay visible via Applied=false.
func TestWriteNewVMWithoutApplyLeavesFileUndefined(t *testing.T) {
	root := copyFixturesRoot(t)
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	m := configsSetupWithCollector(t, root, rec)

	res, err := m.Write(context.Background(), msgs.RU, "test", newVMFile, newVMXML, "черновик VM", false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Applied {
		t.Error("Applied = true, ожидалось false — apply не запрошен")
	}
	for _, argv := range rec.calls {
		if strings.Join(argv, " ") == "virsh -c qemu:///system define "+newVMFile {
			t.Errorf("virsh define не должен вызываться, когда apply=false: %v", rec.calls)
		}
	}
	if _, err := m.Read(newVMFile); err != nil {
		t.Errorf("файл должен быть сохранён на диске даже без apply: %v", err)
	}
}
