package script

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Размещение кластера по хостам в сценарии — то же, что таблица в
// разделе «Кластеры», одной строкой:
//
//	nodes "hv1: cp 1, w 2; hv2: w 2, bridge br0; hv3: host w, endpoint 10.0.0.7"
//
// Хосты через «;», внутри хоста через «,»: «cp N» / «w N» — машины с
// ролью, «host cp|w» — сам хост как узел, «bridge ИМЯ» — мост этого
// хоста в режиме моста, «endpoint АДРЕС» — адрес хоста для соседей по
// туннелю WireGuard.

// PlacementRow — одна строка размещения.
type PlacementRow struct {
	Host     string
	Role     string // control-plane | worker
	Kind     string // vm | host
	Count    int
	Bridge   string
	Endpoint string
}

// IsPlacement — nodes записан размещением по хостам, а не топологией.
func IsPlacement(nodes string) bool { return strings.Contains(nodes, ":") }

// ParsePlacement разбирает строку размещения.
func ParsePlacement(s string) ([]PlacementRow, error) {
	var out []PlacementRow
	for _, hostPart := range strings.Split(s, ";") {
		hostPart = strings.TrimSpace(hostPart)
		if hostPart == "" {
			continue
		}
		host, items, ok := strings.Cut(hostPart, ":")
		host = strings.TrimSpace(host)
		if !ok || !nameRe.MatchString(host) {
			return nil, msgs.Errorf("script.badPlacementHost", hostPart)
		}
		bridge, endpoint := "", ""
		var rows []PlacementRow
		for _, item := range strings.Split(items, ",") {
			f := strings.Fields(item)
			if len(f) != 2 {
				return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
			}
			switch f[0] {
			case "cp", "control-plane", "w", "worker":
				n, err := strconv.Atoi(f[1])
				if err != nil || n < 1 || n > 20 {
					return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
				}
				rows = append(rows, PlacementRow{Host: host, Role: placementRole(f[0]), Kind: "vm", Count: n})
			case "host":
				if placementRole(f[1]) == "" {
					return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
				}
				rows = append(rows, PlacementRow{Host: host, Role: placementRole(f[1]), Kind: "host", Count: 1})
			case "bridge":
				if !nameRe.MatchString(f[1]) {
					return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
				}
				bridge = f[1]
			case "endpoint":
				if !addrRe.MatchString(f[1]) {
					return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
				}
				endpoint = f[1]
			default:
				return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
			}
		}
		if len(rows) == 0 {
			return nil, msgs.Errorf("script.badPlacementHost", hostPart)
		}
		for i := range rows {
			rows[i].Bridge, rows[i].Endpoint = bridge, endpoint
		}
		out = append(out, rows...)
	}
	if len(out) == 0 {
		return nil, msgs.Errorf("script.badPlacementHost", s)
	}
	return out, nil
}

// addrRe — имя хоста или IP без порта.
var addrRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,253}$`)

func placementRole(s string) string {
	switch s {
	case "cp", "control-plane":
		return "control-plane"
	case "w", "worker":
		return "worker"
	}
	return ""
}
