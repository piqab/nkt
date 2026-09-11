package vmcreate

import (
	"strings"
	"testing"
)

func validSpec() Spec {
	return Spec{
		Name: "web-01", ImageID: "debian-13", DiskGB: 20, MemoryMB: 2048, VCPUs: 2,
		User: "deploy", SSHKey: "ssh-ed25519 AAAAKEY deploy@laptop",
	}
}

func TestSpecValidate(t *testing.T) {
	if err := validSpec().Validate(); err != nil {
		t.Fatalf("верное описание отклонено: %v", err)
	}

	bad := map[string]func(*Spec){
		"имя с пробелом":         func(s *Spec) { s.Name = "web 01" },
		"имя с точкой с запятой": func(s *Spec) { s.Name = "web;rm" },
		"нет образа":             func(s *Spec) { s.ImageID = "" },
		"диск ноль":              func(s *Spec) { s.DiskGB = 0 },
		"память крохотная":       func(s *Spec) { s.MemoryMB = 64 },
		"ядер ноль":              func(s *Spec) { s.VCPUs = 0 },
		"пользователь с ../":     func(s *Spec) { s.User = "../root" },
		"ключ не ключ":           func(s *Spec) { s.SSHKey = "просто пароль" },
		"ключ в две строки":      func(s *Spec) { s.SSHKey = "ssh-ed25519 AAA\nssh-ed25519 BBB" },
		"пакет с командой":       func(s *Spec) { s.Packages = []string{"nginx; rm -rf /"} },
		"мост с кавычкой":        func(s *Spec) { s.Bridge = "br0'/><script>" },
	}
	for name, mutate := range bad {
		s := validSpec()
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: принято без ошибки", name)
		}
	}
}

// Вход в машину — только по ключу: облачные образы приходят без пароля,
// и придумывать его за оператора нельзя.
func TestUserDataHasNoPassword(t *testing.T) {
	out := UserData(validSpec())
	if !strings.HasPrefix(out, "#cloud-config\n") {
		t.Fatalf("cloud-init не узнает такой файл:\n%s", out)
	}
	for _, forbidden := range []string{"password", "chpasswd", "plain_text_passwd"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("в настройках упомянут пароль (%s):\n%s", forbidden, out)
		}
	}
	if !strings.Contains(out, "ssh_pwauth: false") {
		t.Error("вход по паролю не выключен")
	}
	if !strings.Contains(out, "lock_passwd: true") {
		t.Error("пароль учётной записи не заблокирован")
	}
	if !strings.Contains(out, "NOPASSWD:ALL") {
		t.Error("sudo без пароля не настроен — иначе в машину не войти как администратор")
	}
	if !strings.Contains(out, "growpart") {
		t.Error("нет расширения файловой системы: диск больше образа, а место осталось бы прежним")
	}
}

func TestUserDataQuotesKey(t *testing.T) {
	s := validSpec()
	s.SSHKey = `ssh-ed25519 AAAA "кавычка" внутри`
	out := UserData(s)
	// Ключ уходит в YAML, и кавычки в комментарии не должны его ломать.
	if !strings.Contains(out, `\"кавычка\"`) {
		t.Errorf("ключ не заэкранирован:\n%s", out)
	}
}

func TestDomainXMLShape(t *testing.T) {
	xml := DomainXML(validSpec(), "/var/lib/libvirt/images/web-01.qcow2", "/var/lib/libvirt/images/web-01-seed.iso")
	for _, want := range []string{
		"<name>web-01</name>",
		"<memory unit='KiB'>2097152</memory>",
		"<vcpu placement='static'>2</vcpu>",
		"web-01.qcow2",
		// Настройки cloud-init подключаются отдельным cdrom — иначе их
		// негде прочитать при первом запуске.
		"device='cdrom'",
		"web-01-seed.iso",
		// Без моста — сеть libvirt по умолчанию: она есть почти везде.
		"<source network='default'/>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("в XML нет %q:\n%s", want, xml)
		}
	}

	s := validSpec()
	s.Bridge = "br0"
	if xml := DomainXML(s, "d", "s"); !strings.Contains(xml, "<source bridge='br0'/>") {
		t.Errorf("мост не подставлен:\n%s", xml)
	}
}

// Адрес машины берётся из вывода virsh domifaddr — по нему хаб потом и
// подключается.
func TestParseDomifaddr(t *testing.T) {
	out := ` Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 vnet0      52:54:00:ab:cd:ef    ipv4         192.168.122.67/24
`
	if got := parseDomifaddr(out); got != "192.168.122.67" {
		t.Errorf("parseDomifaddr = %q", got)
	}
	// Аренды ещё нет — это не адрес «0.0.0.0», а «пока не знаю».
	empty := " Name       MAC address          Protocol     Address\n-----\n"
	if got := parseDomifaddr(empty); got != "" {
		t.Errorf("на пустом выводе получено %q", got)
	}
}

// Ключ хаба кладётся в ту же учётную запись, что и ключ оператора:
// иначе машину пришлось бы открывать хабу вручную.
func TestUserDataIncludesExtraKeys(t *testing.T) {
	s := validSpec()
	s.ExtraKeys = []string{"ssh-rsa AAAAHUB hub@nkt"}
	out := UserData(s)
	if !strings.Contains(out, "AAAAKEY") || !strings.Contains(out, "AAAAHUB") {
		t.Errorf("в настройках не оба ключа:\n%s", out)
	}
}

// Из пары «cloud-localds или genisoimage» нужна любая: требовать обе —
// значит просить лишний пакет.
func TestMissingToolsHonoursAlternatives(t *testing.T) {
	tools := Tools()
	set := func(cmd string, present bool) {
		for i := range tools {
			if tools[i].Command == cmd {
				tools[i].Present = present
			}
		}
	}
	set("qemu-img", true)
	set("virsh", true)
	set("cloud-localds", false)
	set("genisoimage", true)

	if missing := MissingTools(tools); len(missing) != 0 {
		t.Errorf("не хватает %+v, хотя замена есть", missing)
	}

	set("genisoimage", false)
	missing := MissingTools(tools)
	if len(missing) != 2 {
		t.Fatalf("без обеих должно не хватать двух: %+v", missing)
	}

	set("qemu-img", false)
	if got := len(MissingTools(tools)); got != 3 {
		t.Errorf("не хватает %d, ожидалось 3", got)
	}
}

// virsh пишет «Failed to start domain» первой строкой, а причину —
// следующей. Показывать только первую значит каждый раз выбрасывать ту
// часть, ради которой сообщение и читают.
func TestCommandErrorKeepsReason(t *testing.T) {
	out := "error: Failed to start domain 'w1'\nerror: Network not found: no network with matching name 'default'\n"
	got := commandError(out, "")
	for _, want := range []string{"Failed to start domain 'w1'", "no network with matching name"} {
		if !strings.Contains(got, want) {
			t.Errorf("в сообщении нет %q: %q", want, got)
		}
	}
	if strings.Contains(got, "error: ") {
		t.Errorf("повторяющееся «error:» не убрано: %q", got)
	}
	if got := commandError("", ""); got == "" {
		t.Error("пустой вывод должен давать хоть какое-то объяснение")
	}
}

// Состояние сети берётся из virsh net-info: «Active: yes» и «Active: no»
// различаются одним словом, и спутать их значит либо не поднять сеть,
// либо поднимать уже поднятую.
func TestActiveYes(t *testing.T) {
	active := "Name:           default\nUUID:           abc\nActive:         yes\nPersistent:     yes\n"
	inactive := "Name:           default\nActive:         no\n"
	if !activeYes(active) {
		t.Error("поднятая сеть определена как неподнятая")
	}
	if activeYes(inactive) {
		t.Error("неподнятая сеть определена как поднятая")
	}
}

// Диски машин и свободные образы лежат в одном каталоге, и различать их
// приходится по тому, кто их занимает.
func TestParseDomblklist(t *testing.T) {
	out := `Type       Device    Target   Source
------------------------------------------------
file       disk      vda      /var/lib/libvirt/images/w1.qcow2
file       cdrom     sda      /var/lib/libvirt/images/w1-seed.iso
network    disk      vdb      rbd:pool/image
block      disk      vdc      /dev/sdb
`
	paths := parseDomblklist(out)
	if len(paths) != 2 {
		t.Fatalf("разобрано %d путей: %q", len(paths), paths)
	}
	if paths[0] != "/var/lib/libvirt/images/w1.qcow2" || paths[1] != "/var/lib/libvirt/images/w1-seed.iso" {
		t.Errorf("пути = %q", paths)
	}
}
