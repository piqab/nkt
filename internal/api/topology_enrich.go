package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/topology"
)

// enrichTopology дописывает в узлы машин и инстансов LXD то, чего нет в
// снимке инвентаря: доступность по ping (цели vm-ip:/lxd-ip:), текущие
// процессор и память (последние замеры нагрузки) и уязвимости пакетов
// внутри (последний скан). Цвет узла — худшее из этого: не отвечает на
// ping — ошибка, есть критические уязвимости — предупреждение.
func (s *Server) enrichTopology(ctx context.Context, g *topology.Graph) {
	if g == nil || s.db == nil {
		return
	}
	pings := map[string]store.TargetStatus{}
	if list, err := s.db.TargetStatuses(ctx); err == nil {
		for _, t := range list {
			if strings.HasPrefix(t.Key, "vm-ip:") || strings.HasPrefix(t.Key, "lxd-ip:") {
				pings[t.Key] = t
			}
		}
	}
	vulns := map[string]map[string]int{}
	if scan, err := s.loadPersistedVulnScan(ctx); err == nil && scan != nil {
		for _, f := range scan.Findings {
			if f.Target == "" {
				continue
			}
			if vulns[f.Target] == nil {
				vulns[f.Target] = map[string]int{}
			}
			vulns[f.Target][strings.ToLower(f.Severity)]++
		}
	}
	since := store.FormatTime(time.Now().Add(-15 * time.Minute))
	for i := range g.Nodes {
		n := &g.Nodes[i]
		var name, pingKey, source, vulnTarget string
		switch n.Kind {
		case topology.KindVM:
			name = strings.TrimPrefix(n.ID, "vm:")
			pingKey, source, vulnTarget = "vm-ip:"+name, "libvirt", "VM "+name
		case topology.KindLXD:
			name = strings.TrimPrefix(n.ID, "lxd:")
			pingKey, source, vulnTarget = "lxd-ip:"+name, "lxd", "LXD "+name
		default:
			continue
		}
		if n.Meta == nil {
			n.Meta = map[string]string{}
		}
		if t, ok := pings[pingKey]; ok && t.LastOK != nil {
			if *t.LastOK {
				n.Meta["ping"] = fmt.Sprintf("ok %.1f ms, 24h %.1f%%", t.LastLatency, t.Uptime24h)
			} else {
				n.Meta["ping"] = "down: " + t.LastError
				n.Status = topology.StatusError
			}
		}
		if m, err := s.db.LatestMetrics(ctx, source, name, since); err == nil {
			if v, ok := m["cpu_pct"]; ok {
				n.Meta["cpu"] = fmt.Sprintf("%.1f%%", v)
			}
			if v, ok := m["mem_bytes"]; ok {
				n.Meta["memory"] = formatBytesIEC(v)
			}
		}
		if c := vulns[vulnTarget]; len(c) > 0 {
			var parts []string
			for _, sev := range []string{"critical", "high", "medium", "low", "unknown"} {
				if c[sev] > 0 {
					parts = append(parts, fmt.Sprintf("%s %d", sev, c[sev]))
				}
			}
			n.Meta["vulns"] = strings.Join(parts, ", ")
			if c["critical"] > 0 && n.Status == topology.StatusOK {
				n.Status = topology.StatusWarn
			}
		}
	}
}

func formatBytesIEC(v float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
