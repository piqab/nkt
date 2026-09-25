package parse

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// XML домена режется на настройки верхнего уровня и устройства внутри
// <devices>, с точными границами строк — на них опирается splice.
func TestLibvirtBlocks(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	c := collect.NewFixtures(root)
	blocks, err := Blocks(c, "/etc/libvirt/qemu/web-vm.xml", "libvirt")
	if err != nil {
		t.Fatal(err)
	}
	var devices *Block
	names := map[string]Block{}
	for i := range blocks {
		if blocks[i].Kind == BlockDevices {
			devices = &blocks[i]
		} else {
			names[blocks[i].Name] = blocks[i]
		}
	}
	if devices == nil {
		t.Fatalf("нет блока devices: %+v", blocks)
	}
	if b, ok := names["memory 2097152 KiB"]; !ok || b.StartLine != 4 || b.EndLine != 4 || b.Kind != BlockSetting || !b.Editable {
		t.Errorf("memory: %+v", names)
	}
	if b, ok := names["os"]; !ok || b.StartLine != 7 || b.EndLine != 10 {
		t.Errorf("os должен занимать строки 7–10: %+v", b)
	}
	if devices.Editable || devices.StartLine != 11 || devices.EndLine != 23 {
		t.Errorf("devices: %+v", *devices)
	}
	if len(devices.Children) != 3 {
		t.Fatalf("устройств %d, ожидалось 3: %+v", len(devices.Children), devices.Children)
	}
	disk, iface, gfx := devices.Children[0], devices.Children[1], devices.Children[2]
	if disk.Kind != BlockDisk || disk.Name != "vda (qcow2, /var/lib/libvirt/images/web-vm.qcow2)" || disk.StartLine != 12 || disk.EndLine != 16 {
		t.Errorf("disk: %+v", disk)
	}
	if iface.Kind != BlockInterface || iface.Name != "bridge br0 (52:54:00:12:34:56)" || iface.StartLine != 17 || iface.EndLine != 21 {
		t.Errorf("interface: %+v", iface)
	}
	// Однострочный самозакрывающийся элемент.
	if gfx.Kind != BlockGraphics || gfx.Name != "vnc" || gfx.StartLine != 22 || gfx.EndLine != 22 {
		t.Errorf("graphics: %+v", gfx)
	}

	// Вставка устройства — перед </devices>, с отступом.
	raw, _ := c.ReadFile("/etc/libvirt/qemu/web-vm.xml")
	out, err := InsertBlockAtEnd(string(raw), BlockInterface, "<interface type='bridge'>\n  <source bridge='br1'/>\n</interface>", devices.EndLine)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if lines[devices.EndLine-1] != "    <interface type='bridge'>" || lines[devices.EndLine+2] != "  </devices>" {
		t.Errorf("вставка не туда:\n%s", out)
	}
	// Удаление диска — splice его строк.
	out, err = SpliceBlock(string(raw), disk.StartLine, disk.EndLine, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "web-vm.qcow2") || !strings.Contains(out, "<interface") {
		t.Errorf("удаление диска:\n%s", out)
	}
}

// Разделы compose кроме services — блоками; новый элемент раздела, которого
// ещё нет, создаёт раздел в конце файла.
func TestComposeSections(t *testing.T) {
	text := "services:\n  web:\n    image: nginx\n\nnetworks:\n  backend:\n    driver: bridge\n  frontend: {}\n\nvolumes:\n  data:\n"
	lines := strings.Split(text, "\n")
	nets := composeSectionBlocks(lines, "networks", BlockNetwork)
	if len(nets) != 2 || nets[0].Name != "backend" || nets[0].StartLine != 6 || nets[0].EndLine != 7 || nets[1].Name != "frontend" {
		t.Fatalf("networks = %+v", nets)
	}
	vols := composeSectionBlocks(lines, "volumes", BlockVolume)
	if len(vols) != 1 || vols[0].Name != "data" || vols[0].StartLine != 11 {
		t.Fatalf("volumes = %+v", vols)
	}
	out, err := InsertBlockAtEnd(text, BlockSecret, "db_password:\n  file: ./db_password.txt", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "secrets:\n  db_password:\n    file: ./db_password.txt") {
		t.Errorf("раздел secrets не создан в конце:\n%s", out)
	}
	out, err = InsertBlockAtEnd(text, BlockNetwork, "public:\n  external: true", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  frontend: {}\n  public:\n    external: true\n\nvolumes:") {
		t.Errorf("сеть не после последней:\n%s", out)
	}
}
