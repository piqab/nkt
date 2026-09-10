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
