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
	"github.com/piqab/nkt/internal/msgs"
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
	// Force — создавать, даже если подсеть пересекается с сетью самого
	// хоста. Пересечение с другой сетью libvirt не снимается: такая сеть
	// всё равно не поднимется.
	Force bool `json:"force,omitempty"`
}

// Validate проверяет то, что уйдёт в описание сети и в virsh.
func (s Spec) Validate() error {
	if !nameRe.MatchString(s.Name) {
		return msgs.Errorf("vmnet.networkNameLatinLettersDigits")
	}
	switch s.Mode {
	case ModeNAT, ModeIsolated:
		if s.Bridge != "" && !bridgeRe.MatchString(s.Bridge) {
			return msgs.Errorf("vmnet.invalidBridgeName", s.Bridge)
		}
		if _, _, err := subnetParts(s.Subnet); err != nil {
			return err
		}
	case ModeBridge:
		if !bridgeRe.MatchString(s.Bridge) {
			return msgs.Errorf("vmnet.specifyExistingHostBridge")
		}
	default:
		return msgs.Errorf("vmnet.networkKindMust", ModeNAT, ModeBridge, ModeIsolated)
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
		return "", "", msgs.Errorf("vmnet.subnetMustLookLike192", err)
	}
	if ip.To4() == nil {
		return "", "", msgs.Errorf("vmnet.onlyIPv4Supported")
	}
	ones, bits := ipNet.Mask.Size()
	if bits != 32 || ones < 8 || ones > 30 {
		return "", "", msgs.Errorf("vmnet.subnetMaskMustBetween8")
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

// Occupied — занятый диапазон адресов и то, кем он занят.
//
// Подсеть сети libvirt нельзя пересекать с другой её сетью (libvirt
// такую сеть просто не поднимет) и не стоит пересекать с сетью самого
// хоста: адреса машин совпадут с адресами соседей, и маршрутизация
// сломается ровно в тот момент, когда понадобится.
type Occupied struct {
	// CIDR — занятый диапазон.
	CIDR string
	// Where — чем занят: «сеть libvirt «default»» или «интерфейс eth0».
	Where string
	// Host — диапазон принадлежит интерфейсу самого хоста, а не сети
	// libvirt. Такое пересечение оператор может сознательно разрешить,
	// пересечение двух сетей libvirt — нет.
	Host bool
}

// SubnetInUse — просимая подсеть пересекается с уже занятой.
type SubnetInUse struct {
	Subnet string
	With   Occupied
}

func (e *SubnetInUse) Error() string {
	return msgs.T(msgs.DefaultLang, "vmnet.subnetOverlaps", e.Subnet, e.With.Where, e.With.CIDR)
}

// Unwrap отдаёт каталожную ошибку — на языке запроса её покажет writeErr.
func (e *SubnetInUse) Unwrap() error {
	return msgs.Errorf("vmnet.subnetOverlaps", e.Subnet, e.With.Where, e.With.CIDR)
}

// Overridable отвечает, можно ли создать сеть вопреки этому пересечению.
func (e *SubnetInUse) Overridable() bool { return e.With.Host }

// NetworkRanges отдаёт подсети уже заведённых сетей libvirt.
func NetworkRanges(ctx context.Context, nets []Network) []Occupied {
	var out []Occupied
	for _, n := range nets {
		cidr := networkCIDR(n)
		if cidr == "" {
			continue
		}
		out = append(out, Occupied{CIDR: cidr, Where: msgs.Tc(ctx, "vmnet.libvirtNetwork", n.Name)})
	}
	return out
}

// networkCIDR собирает подсеть сети из адреса хоста и маски.
func networkCIDR(n Network) string {
	if n.Address == "" {
		return ""
	}
	ip := net.ParseIP(strings.TrimSpace(n.Address)).To4()
	if ip == nil {
		return ""
	}
	mask := net.IPv4Mask(255, 255, 255, 0)
	if n.Netmask != "" {
		m := net.ParseIP(strings.TrimSpace(n.Netmask)).To4()
		if m == nil {
			return ""
		}
		mask = net.IPMask(m)
	}
	ones, bits := mask.Size()
	if bits != 32 {
		return ""
	}
	return fmt.Sprintf("%s/%d", ip.Mask(mask).String(), ones)
}

// CheckSubnet отвечает, свободна ли подсеть.
//
// Проверка до создания, а не после: сеть с пересекающейся подсетью
// заводится молча и отказывается подниматься уже потом — отказ прилетает
// при создании машины, за несколько экранов от места ошибки.
func CheckSubnet(subnet string, taken []Occupied) error {
	want, err := parseCIDR4(subnet)
	if err != nil {
		return err
	}
	for _, t := range taken {
		have, err := parseCIDR4(t.CIDR)
		if err != nil {
			continue
		}
		if netsOverlap(want, have) {
			return &SubnetInUse{Subnet: subnet, With: t}
		}
	}
	return nil
}

func parseCIDR4(cidr string) (*net.IPNet, error) {
	_, ipNet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, msgs.Errorf("vmnet.subnetMustLookLike192", err)
	}
	if ipNet.IP.To4() == nil {
		return nil, msgs.Errorf("vmnet.onlyIPv4Supported")
	}
	return ipNet, nil
}

// netsOverlap — пересекаются ли два диапазона. Достаточно проверить,
// лежит ли начало одного внутри другого: подсети выровнены по границе.
func netsOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}
