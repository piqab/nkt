// Package vmnet управляет сетями libvirt, в которые включаются машины.
//
// Без сети машина не стартует вовсе: «Failed to start domain» с причиной
// «нет сети с совпадающим именем default» — самая частая встреча с
// libvirt на свежем хосте. Пакет libvirt сеть «default» ставит, но не
// везде и не всегда запускает, а на минимальной установке её может не
// быть вовсе.
package vmnet

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/piqab/nkt/internal/collect"
)

// Виды сетей, которые nkt умеет заводить.
const (
	// ModeNAT — своя подсеть с DHCP, наружу через NAT хоста. Так
	// устроена «default», и это то, что нужно машине, которой достаточно
	// доступа в интернет.
	ModeNAT = "nat"
	// ModeBridge — машина включается в существующий мост хоста и живёт в
	// той же сети, что он сам: свой адрес от общего DHCP, видна соседям.
	ModeBridge = "bridge"
	// ModeIsolated — подсеть без выхода наружу: машины видят друг друга
	// и хост, и больше никого.
	ModeIsolated = "isolated"
)

// Runner выполняет команду вне песочницы юнита — virsh правит состояние
// libvirt, изнутри юнита туда хода нет.
type Runner func(ctx context.Context, argv ...string) (collect.CommandResult, error)

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)
	bridgeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,15}$`)
)

// Network — сеть, как её видит оператор.
type Network struct {
	Name       string `json:"name"`
	Active     bool   `json:"active"`
	Autostart  bool   `json:"autostart"`
	Persistent bool   `json:"persistent"`
	// Mode — forward mode из описания: nat, bridge, route или пусто у
	// изолированной.
	Mode   string `json:"mode,omitempty"`
	Bridge string `json:"bridge,omitempty"`
	// Address и Netmask — адрес хоста в этой сети, если она своя.
	Address string `json:"address,omitempty"`
	Netmask string `json:"netmask,omitempty"`
	// DHCP — раздаёт ли сеть адреса. Без этого машина поднимется без
	// адреса, и подключиться к ней будет нечем.
	DHCP bool `json:"dhcp"`
}

// Spec — что за сеть создаём.
type Spec struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
	// Bridge — имя моста. Для nat и isolated создаётся новый (virbr*),
	// для bridge — имя уже существующего моста хоста.
	Bridge string `json:"bridge,omitempty"`
	// Subnet — подсеть в виде CIDR («192.168.100.0/24») для nat и
	// isolated. Первый адрес занимает хост, остальное раздаётся по DHCP.
	Subnet string `json:"subnet,omitempty"`
	// DHCP — раздавать адреса. Выключают редко, но осознанно: в сети со
	// своим DHCP-сервером две раздачи мешают друг другу.
	DHCP bool `json:"dhcp"`
	// Autostart — поднимать сеть вместе с хостом.
	Autostart bool `json:"autostart"`
}

// Validate проверяет то, что уйдёт в описание сети и в virsh.
func (s Spec) Validate() error {
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("имя сети: латинские буквы, цифры, точка, дефис и подчёркивание, до 32 символов")
	}
	switch s.Mode {
	case ModeNAT, ModeIsolated:
		if s.Bridge != "" && !bridgeRe.MatchString(s.Bridge) {
			return fmt.Errorf("некорректное имя моста: %q", s.Bridge)
		}
		if _, _, err := subnetParts(s.Subnet); err != nil {
			return err
		}
	case ModeBridge:
		if !bridgeRe.MatchString(s.Bridge) {
			return fmt.Errorf("укажите существующий мост хоста")
		}
	default:
		return fmt.Errorf("вид сети должен быть %q, %q или %q", ModeNAT, ModeBridge, ModeIsolated)
	}
	return nil
}

// subnetParts разбирает подсеть на адрес хоста, маску и границы раздачи.
//
// Хост занимает первый адрес, DHCP отдаёт всё до предпоследнего:
// последний — широковещательный, и раздавать его нельзя.
func subnetParts(cidr string) (host, mask string, err error) {
	ip, ipNet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return "", "", fmt.Errorf("подсеть должна быть вида 192.168.100.0/24: %w", err)
	}
	if ip.To4() == nil {
		return "", "", fmt.Errorf("поддерживается только IPv4")
	}
	ones, bits := ipNet.Mask.Size()
	if bits != 32 || ones < 8 || ones > 30 {
		return "", "", fmt.Errorf("маска подсети должна быть от /8 до /30")
	}
	base := ipNet.IP.To4()
	hostIP := make(net.IP, len(base))
	copy(hostIP, base)
	hostIP[3]++
	return hostIP.String(), net.IP(ipNet.Mask).String(), nil
}

// dhcpRange отдаёт границы раздачи адресов.
func dhcpRange(cidr string) (start, end string, err error) {
	_, ipNet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return "", "", err
	}
	base := ipNet.IP.To4()
	from := make(net.IP, len(base))
	copy(from, base)
	from[3] += 2 // первый адрес у хоста

	ones, _ := ipNet.Mask.Size()
	size := uint32(1) << uint(32-ones)
	last := make(net.IP, len(base))
	copy(last, base)
	// Последний адрес широковещательный, раздаётся всё до него.
	addUint32(last, size-2)
	return from.String(), last.String(), nil
}

func addUint32(ip net.IP, n uint32) {
	v := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	v += n
	ip[0], ip[1], ip[2], ip[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}

// XML собирает описание сети для virsh net-define.
func XML(s Spec) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<network>\n")
	fmt.Fprintf(&b, "  <name>%s</name>\n", escape(s.Name))

	switch s.Mode {
	case ModeBridge:
		b.WriteString("  <forward mode='bridge'/>\n")
		fmt.Fprintf(&b, "  <bridge name='%s'/>\n", escape(s.Bridge))
	default:
		if s.Mode == ModeNAT {
			b.WriteString("  <forward mode='nat'/>\n")
		}
		if s.Bridge != "" {
			fmt.Fprintf(&b, "  <bridge name='%s' stp='on' delay='0'/>\n", escape(s.Bridge))
		}
		host, mask, err := subnetParts(s.Subnet)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "  <ip address='%s' netmask='%s'>\n", host, mask)
		if s.DHCP {
			start, end, err := dhcpRange(s.Subnet)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "    <dhcp>\n      <range start='%s' end='%s'/>\n    </dhcp>\n", start, end)
		}
		b.WriteString("  </ip>\n")
	}
	b.WriteString("</network>\n")
	return b.String(), nil
}

func escape(s string) string {
	var buf strings.Builder
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
