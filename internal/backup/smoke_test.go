package backup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Прогон сценария бэкапа контейнера с поддельным docker из NKT_FAKE_BIN:
// архив появляется, в нём manifest, образ и том. Без переменной — пропуск.
func TestContainerBackupSmoke(t *testing.T) {
	bin := os.Getenv("NKT_FAKE_BIN")
	if bin == "" {
		t.Skip("NKT_FAKE_BIN не задан")
	}
	root := t.TempDir()
	sc, out, err := Script(root, Params{Kind: KindDocker, Name: "web"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", sc)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	list, err := exec.Command("tar", "-tf", out).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, list)
	}
	for _, want := range []string{"./manifest.json", "./image.tar", "./inspect.json", "./volumes/appdata.tgz"} {
		if !strings.Contains(string(list), want) {
			t.Errorf("нет %s в архиве:\n%s\nжурнал:\n%s", want, list, b)
		}
	}
	m, _ := exec.Command("tar", "-xOf", out, "./manifest.json").Output()
	if _, err := ReadManifest(m); err != nil {
		t.Errorf("manifest: %v %s", err, m)
	}
	// Восстановление копией: образ грузится, том создаётся под новым
	// именем, контейнер пересоздаётся с параметрами из inspect.
	rs, err := RestoreScript(root, RestoreParams{Path: out, NewName: "web2"})
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("bash", "-c", rs)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if b, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(b), "--name web2") || !strings.Contains(string(b), "-v web2-appdata:/data") || strings.Contains(string(b), "8080") {
		t.Fatalf("восстановление: %v\n%s", err, b)
	}
}

// Бэкап работающей машины с поддельными virsh/qemu-img (NKT_FAKE_VM_BIN):
// снимок с diskspec на каждый диск, копия, blockcommit, архив с диском.
func TestVMBackupSmoke(t *testing.T) {
	bin := os.Getenv("NKT_FAKE_VM_BIN")
	if bin == "" {
		t.Skip("NKT_FAKE_VM_BIN не задан")
	}
	root := t.TempDir()
	log := filepath.Join(root, "calls.log")
	sc, out, err := Script(root, Params{Kind: KindVM, Name: "web"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path, _ := writeScript(root, 1, sc)
	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "FAKELOG="+log)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"--diskspec vda,snapshot=external", "qemu-img convert -p -O qcow2 -c /var/lib/libvirt/images/web.qcow2", "blockcommit web vda --active --pivot"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("нет %q в вызовах:\n%s\nжурнал:\n%s", want, calls, b)
		}
	}
	list, _ := exec.Command("tar", "-tf", out).CombinedOutput()
	if !strings.Contains(string(list), "./vda.qcow2") || !strings.Contains(string(list), "./domain.xml") {
		t.Errorf("архив:\n%s", list)
	}
}
