// Package vmcreate поднимает машину из облачного образа.
//
// До этого раздел виртуализации умел создать пустой диск и определить
// домен — систему в машину оператор ставил сам, как на физическом
// сервере. Здесь закрывается разрыв: готовый образ копируется под новую
// машину, cloud-init при первом запуске заводит пользователя с ключом и
// именем, и машина сразу доступна по SSH.
package vmcreate

import (
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"html"
	"regexp"
	"strings"
)

// Пределы. Не «сколько влезет», а сколько осмысленно: опечатка в поле
// размера не должна занимать весь диск хоста.
const (
	maxDiskGB   = 4096
	maxMemoryMB = 1 << 20
	maxVCPUs    = 256
)

var (
	nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62})?$`)
	userRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
)

// Tool — программа, без которой машину не создать, и пакет, в котором
// она лежит.
type Tool struct {
	Command string `json:"command"`
	Package string `json:"package"`
	// Why — зачем она нужна: список из четырёх незнакомых имён без
	// объяснения ничего не говорит.
	Why string `json:"why"`
	// Alternative — команда, которая заменяет эту. Из пары нужна любая.
	Alternative string `json:"alternative,omitempty"`
	Present     bool   `json:"present"`
}

// Tools — что должно быть на хосте, чтобы создание машин работало.
//
// Пакеты названы по Debian и Ubuntu — на них рассчитано всё остальное в
// nkt, и предлагать установку того, чего в их репозиториях нет, было бы
// нечестно.
func Tools() []Tool {
	return []Tool{
		{Command: "qemu-img", Package: "qemu-utils", Why: "делает диск машины из образа"},
		{Command: "virsh", Package: "libvirt-clients", Why: "определяет и запускает машину"},
		{Command: "cloud-localds", Package: "cloud-image-utils",
			Why: "собирает настройки первого запуска", Alternative: "genisoimage"},
		{Command: "genisoimage", Package: "genisoimage",
			Why: "то же самое, если нет cloud-localds", Alternative: "cloud-localds"},
	}
}

// MissingTools отвечает, чего не хватает. Пара с заменой считается
// собранной, если есть хотя бы одна из двух — требовать обе значило бы
// просить лишний пакет.
func MissingTools(tools []Tool) []Tool {
	present := map[string]bool{}
	for _, t := range tools {
		present[t.Command] = t.Present
	}
	var missing []Tool
	for _, t := range tools {
		if t.Present {
			continue
		}
		if t.Alternative != "" && present[t.Alternative] {
			continue
		}
		missing = append(missing, t)
	}
	return missing
}

// Spec — что за машину создаём.
type Spec struct {
	Name    string `json:"name"`
	ImageID string `json:"image_id"`
	// Hostname — имя внутри машины. Пусто — берётся Name.
	Hostname string `json:"hostname,omitempty"`
	DiskGB   int    `json:"disk_gb"`
	MemoryMB int    `json:"memory_mb"`
	VCPUs    int    `json:"vcpus"`
	// Network — сеть libvirt, в которую включается машина. Пусто и без
	// моста — «default»: она есть почти везде и работает через NAT.
	Network string `json:"network,omitempty"`
	// Bridge — сетевой мост хоста напрямую, мимо сетей libvirt. Нужен
	// там, где мост уже настроен руками и заводить под него сеть
	// libvirt незачем.
	Bridge string `json:"bridge,omitempty"`
	// User и SSHKey — учётная запись, под которой в машину заходят.
	// Без ключа машина будет создана, но войти в неё будет нечем: пароля
	// облачные образы не заводят вовсе.
	User   string `json:"user"`
	SSHKey string `json:"ssh_key"`
	// ExtraKeys — дополнительные публичные ключи в ту же учётную запись.
	// Через них в машину входит хаб: свой ключ он выдаёт заранее, и
	// класть его надо в тот же первый запуск, иначе машину придётся
	// открывать руками.
	ExtraKeys []string `json:"extra_keys,omitempty"`
	// Packages — что доставить при первом запуске.
	Packages []string `json:"packages,omitempty"`
	// Autostart — поднимать машину вместе с хостом.
	Autostart bool `json:"autostart,omitempty"`
}

// Validate проверяет то, что уйдёт в команды и в XML.
func (s Spec) Validate() error {
	switch {
	case !nameRe.MatchString(s.Name):
		return msgs.Errorf("vmcreate.machineNameLowercaseLatinLetters")
	case s.ImageID == "":
		return msgs.Errorf("vmcreate.imageSelected")
	case s.DiskGB < 1 || s.DiskGB > maxDiskGB:
		return msgs.Errorf("vmcreate.diskSizeMustBetween1", maxDiskGB)
	case s.MemoryMB < 256 || s.MemoryMB > maxMemoryMB:
		return msgs.Errorf("vmcreate.memoryMustBetween256MB", maxMemoryMB)
	case s.VCPUs < 1 || s.VCPUs > maxVCPUs:
		return msgs.Errorf("vmcreate.coresMustBetween1", maxVCPUs)
	case !userRe.MatchString(s.User):
		return msgs.Errorf("vmcreate.userNameInsideMachineInvalid", s.User)
	}
	for _, key := range append([]string{s.SSHKey}, s.ExtraKeys...) {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !strings.HasPrefix(key, "ssh-") && !strings.HasPrefix(key, "ecdsa-") && !strings.HasPrefix(key, "sk-") {
			return msgs.Errorf("vmcreate.keyDoesLookLikeSSH")
		}
		if strings.ContainsAny(key, "\n\r") {
			return msgs.Errorf("vmcreate.keyMustSingleLine")
		}
	}
	if s.Hostname != "" && !nameRe.MatchString(s.Hostname) {
		return msgs.Errorf("vmcreate.nameInsideMachineInvalid", s.Hostname)
	}
	for _, p := range s.Packages {
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`).MatchString(p) {
			return msgs.Errorf("profile.invalidPackageName", p)
		}
	}
	if s.Bridge != "" && !regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`).MatchString(s.Bridge) {
		return msgs.Errorf("vmcreate.invalidBridgeName", s.Bridge)
	}
	if s.Network != "" && !regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`).MatchString(s.Network) {
		return msgs.Errorf("vmcreate.invalidNetworkName", s.Network)
	}
	return nil
}

// hostname отдаёт имя внутри машины.
func (s Spec) hostname() string {
	if s.Hostname != "" {
		return s.Hostname
	}
	return s.Name
}

// UserData собирает конфигурацию cloud-init.
//
// Пароль не заводится вовсе: облачные образы приходят без него, и
// придумывать его здесь значило бы раздавать машины с паролем, который
// оператор не выбирал. Вход — только по ключу.
func UserData(s Spec) string {
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	fmt.Fprintf(&b, "hostname: %s\n", s.hostname())
	b.WriteString("manage_etc_hosts: true\n")
	b.WriteString("ssh_pwauth: false\n")
	b.WriteString("users:\n")
	fmt.Fprintf(&b, "  - name: %s\n", s.User)
	b.WriteString("    sudo: 'ALL=(ALL) NOPASSWD:ALL'\n")
	b.WriteString("    shell: /bin/bash\n")
	b.WriteString("    lock_passwd: true\n")
	keys := make([]string, 0, 1+len(s.ExtraKeys))
	for _, key := range append([]string{s.SSHKey}, s.ExtraKeys...) {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) > 0 {
		b.WriteString("    ssh_authorized_keys:\n")
		for _, key := range keys {
			fmt.Fprintf(&b, "      - %q\n", key)
		}
	}
	if len(s.Packages) > 0 {
		b.WriteString("package_update: true\n")
		b.WriteString("packages:\n")
		for _, p := range s.Packages {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	// Диск образа маленький (2–3 ГБ), а машине отдан больший: без этого
	// файловая система осталась бы прежнего размера.
	b.WriteString("growpart:\n  mode: auto\n  devices: ['/']\n")
	return b.String()
}

// MetaData — вторая половина того, что читает cloud-init.
func MetaData(s Spec) string {
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", s.Name, s.hostname())
}

// DomainXML описывает машину для libvirt.
//
// Два диска: сам образ и seed с настройками cloud-init — второй
// подключается как cdrom, откуда cloud-init его и читает при первом
// запуске.
func DomainXML(s Spec, diskPath, seedPath string) string {
	// Мост указывают, когда он уже настроен руками; иначе машина
	// включается в сеть libvirt — названную или «default».
	name := s.Network
	if name == "" {
		name = "default"
	}
	network := fmt.Sprintf("    <interface type='network'>\n      <source network='%s'/>\n      <model type='virtio'/>\n    </interface>",
		html.EscapeString(name))
	if s.Bridge != "" {
		network = fmt.Sprintf("    <interface type='bridge'>\n      <source bridge='%s'/>\n      <model type='virtio'/>\n    </interface>",
			html.EscapeString(s.Bridge))
	}
	return fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <memory unit='KiB'>%d</memory>
  <vcpu placement='static'>%d</vcpu>
  <os>
    <type arch='x86_64' machine='q35'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <cpu mode='host-passthrough'/>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='%s'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='%s'/>
      <target dev='sda' bus='sata'/>
      <readonly/>
    </disk>
%s
    <serial type='pty'><target port='0'/></serial>
    <console type='pty'><target type='serial' port='0'/></console>
    <graphics type='vnc' port='-1' autoport='yes'/>
    <channel type='unix'>
      <target type='virtio' name='org.qemu.guest_agent.0'/>
    </channel>
  </devices>
</domain>
`, html.EscapeString(s.Name), s.MemoryMB*1024, s.VCPUs,
		html.EscapeString(diskPath), html.EscapeString(seedPath), network)
}
