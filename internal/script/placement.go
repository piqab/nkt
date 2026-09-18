package script

import (
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Размещение кластера по хостам в сценарии — то же, что таблица в
// разделе «Кластеры», одной строкой:
//
//	nodes "hv1: cp 1, w 2; hv2: w 2, bridge br0; hv3: host w"
//
// Хосты через «;», внутри хоста через «,»: «cp N» / «w N» — машины с
// ролью, «host cp|w» — сам хост как узел, «bridge ИМЯ» — мост этого
// хоста в режиме моста.

// PlacementRow — одна строка размещения.
type PlacementRow struct {
	Host   string
	Role   string // control-plane | worker
	Kind   string // vm | host
	Count  int
	Bridge string
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
		bridge := ""
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
			default:
				return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
			}
		}
		if len(rows) == 0 {
			return nil, msgs.Errorf("script.badPlacementHost", hostPart)
		}
		for i := range rows {
			rows[i].Bridge = bridge
		}
		out = append(out, rows...)
	}
	if len(out) == 0 {
		return nil, msgs.Errorf("script.badPlacementHost", s)
	}
	return out, nil
}

func placementRole(s string) string {
	switch s {
	case "cp", "control-plane":
		return "control-plane"
	case "w", "worker":
		return "worker"
	}
	return ""
}
