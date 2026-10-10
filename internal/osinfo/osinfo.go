// Package osinfo определяет операционную систему хоста, машины или
// контейнера по тому, что о ней известно: os-release, гостевой агент QEMU,
// метка libosinfo в XML домена, ключи образа LXD, признаки Windows в
// конфигурации машины. Итог — model.OSInfo: по ID интерфейс выбирает
// значок перед именем.
package osinfo

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/piqab/nkt/internal/model"
)

// Откуда известна ОС (model.OSInfo.Source).
const (
	SourceOSRelease = "os-release" // /etc/os-release хоста или контейнера
	SourceAgent     = "agent"      // гостевой агент QEMU (guest-get-osinfo)
	SourceLibosinfo = "libosinfo"  // метка <libosinfo:os id=…> в XML домена
	SourceImage     = "image"      // ключи образа (LXD image.os) или ОС образа контейнера
	SourceGuess     = "guess"      // догадка: Hyper-V-флаги в конфигурации машины
)

// aliases — разные написания одной ОС → ID значка.
var aliases = map[string]string{
	"mswindows": "windows", "microsoft windows": "windows", "win": "windows",
	"alpinelinux": "alpine", "alpine linux": "alpine",
	"archlinux": "arch", "arch linux": "arch",
	"redhat": "rhel", "red hat": "rhel", "redhatenterpriselinux": "rhel",
	"rockylinux": "rocky", "rocky linux": "rocky",
	"almalinux": "almalinux", "alma": "almalinux",
	"opensuse-leap": "opensuse", "opensuse-tumbleweed": "opensuse", "opensuse-microos": "opensuse", "sles": "opensuse", "suse": "opensuse",
	"amzn": "amzn", "amazon": "amzn", "amazonlinux": "amzn",
	"ol": "ol", "oracle": "ol", "oraclelinux": "ol",
	"centos-stream": "centos", "centosstream": "centos",
	"nixos": "nixos", "nix": "nixos",
	"linuxmint": "linuxmint", "mint": "linuxmint",
}

// NormalizeID — ID значка из os-release ID, имени из агента или образа.
func NormalizeID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if a, ok := aliases[id]; ok {
		return a
	}
	if strings.HasPrefix(id, "windows") || strings.HasPrefix(id, "win") && len(id) <= 5 {
		return "windows"
	}
	return id
}

// FromOSRelease — ОС по тексту /etc/os-release.
func FromOSRelease(text, source string) *model.OSInfo {
	kv := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		kv[k] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	id := NormalizeID(kv["ID"])
	if id == "" {
		return nil
	}
	name := kv["PRETTY_NAME"]
	if name == "" {
		name = strings.TrimSpace(kv["NAME"] + " " + kv["VERSION"])
	}
	info := &model.OSInfo{ID: id, Name: name, Source: source}
	if like := strings.Fields(kv["ID_LIKE"]); len(like) > 0 {
		info.Like = NormalizeID(like[0])
	}
	return info
}

// FromAgent — ОС по ответу guest-get-osinfo гостевого агента QEMU
// ({"return":{"id":"mswindows","pretty-name":"Windows 10 Pro",…}}).
func FromAgent(raw []byte) *model.OSInfo {
	var doc struct {
		Return struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			PrettyName string `json:"pretty-name"`
			Version    string `json:"version"`
		} `json:"return"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	r := doc.Return
	id := NormalizeID(r.ID)
	if id == "" {
		id = NormalizeID(r.Name)
	}
	if id == "" {
		return nil
	}
	name := r.PrettyName
	if name == "" {
		name = strings.TrimSpace(r.Name + " " + r.Version)
	}
	return &model.OSInfo{ID: id, Name: name, Source: SourceAgent}
}

// libosinfoRe — http://<вендор>/<семейство>/<версия> в id libosinfo.
var libosinfoRe = regexp.MustCompile(`^https?://[^/]+/([^/]+)(?:/([^/]+))?`)

// libosinfoNames — семейство libosinfo → ID значка и имя для подсказки.
var libosinfoNames = map[string][2]string{
	"win":         {"windows", "Windows"},
	"ubuntu":      {"ubuntu", "Ubuntu"},
	"debian":      {"debian", "Debian"},
	"fedora":      {"fedora", "Fedora"},
	"centos":      {"centos", "CentOS"},
	"rhel":        {"rhel", "Red Hat Enterprise Linux"},
	"rocky":       {"rocky", "Rocky Linux"},
	"almalinux":   {"almalinux", "AlmaLinux"},
	"alpinelinux": {"alpine", "Alpine Linux"},
	"archlinux":   {"arch", "Arch Linux"},
	"opensuse":    {"opensuse", "openSUSE"},
	"sles":        {"opensuse", "SUSE Linux Enterprise"},
	"nixos":       {"nixos", "NixOS"},
	"ol":          {"ol", "Oracle Linux"},
	"freebsd":     {"freebsd", "FreeBSD"},
}

// FromLibosinfo — ОС по id метки libosinfo из XML домена
// (http://microsoft.com/win/11, http://ubuntu.com/ubuntu/24.04).
func FromLibosinfo(id string) *model.OSInfo {
	m := libosinfoRe.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return nil
	}
	fam, ver := strings.ToLower(m[1]), m[2]
	n, ok := libosinfoNames[fam]
	if !ok {
		// Неизвестное семейство (Linux прочих вендоров) — как есть.
		n = [2]string{NormalizeID(fam), m[1]}
	}
	name := n[1]
	if ver != "" {
		name += " " + ver
	}
	return &model.OSInfo{ID: n[0], Name: name, Source: SourceLibosinfo}
}

// GuessWindows — догадка по XML домена: Hyper-V-флаги (<hyperv> в
// <features>) ставят только Windows-гостям.
func GuessWindows(domainXML string) *model.OSInfo {
	if strings.Contains(domainXML, "<hyperv") {
		return &model.OSInfo{ID: "windows", Name: "Windows", Source: SourceGuess}
	}
	return nil
}

// FromImageKeys — ОС инстанса LXD по ключам image.os, image.release,
// image.description его конфигурации.
func FromImageKeys(osName, release, description string) *model.OSInfo {
	id := NormalizeID(osName)
	if id == "" {
		return nil
	}
	name := description
	if name == "" {
		name = strings.TrimSpace(osName + " " + release)
	}
	return &model.OSInfo{ID: id, Name: name, Source: SourceImage}
}
