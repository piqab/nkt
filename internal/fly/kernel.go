package fly

import (
	"strconv"
	"strings"
)

// Сводка маршрутов ядра — из conntrack (соединения с меткой маршрута) и
// состояние туннелей — из wg show dump.

// ConntrackSummary — соединения одного маршрута.
type ConntrackSummary struct {
	Conns   int
	Clients map[string]int
	Bytes   int64
}

// ParseConntrack — вывод «conntrack -L -m <метка>»: строка — соединение;
// первый src= — клиент, bytes= (при nf_conntrack_acct) — в обе стороны.
func ParseConntrack(out string) ConntrackSummary {
	s := ConntrackSummary{Clients: map[string]int{}}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "conntrack ") {
			continue
		}
		src := ""
		for _, f := range strings.Fields(line) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch k {
			case "src":
				if src == "" {
					src = v
				}
			case "bytes":
				n, _ := strconv.ParseInt(v, 10, 64)
				s.Bytes += n
			}
		}
		if src == "" {
			continue
		}
		s.Conns++
		s.Clients[src]++
	}
	return s
}

// PeerStats — туннель хоста (на шлюзе).
type PeerStats struct {
	PublicKey string `json:"public_key"`
	// Address — адрес хоста в туннеле (первый из allowed-ips).
	Address  string `json:"address,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	// Handshake — время последнего рукопожатия (Unix), 0 — не было.
	Handshake int64 `json:"handshake"`
	RxBytes   int64 `json:"rx_bytes"`
	TxBytes   int64 `json:"tx_bytes"`
}

// ParseWGDump — «wg show <iface> dump»: первая строка — сам интерфейс,
// дальше по пиру: ключ, psk, endpoint, allowed-ips, рукопожатие, rx, tx,
// keepalive.
func ParseWGDump(out string) []PeerStats {
	var list []PeerStats
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, line := range lines {
		f := strings.Split(line, "\t")
		if i == 0 || len(f) < 8 {
			continue
		}
		hs, _ := strconv.ParseInt(f[4], 10, 64)
		rx, _ := strconv.ParseInt(f[5], 10, 64)
		tx, _ := strconv.ParseInt(f[6], 10, 64)
		ep := f[2]
		if ep == "(none)" {
			ep = ""
		}
		addr, _, _ := strings.Cut(strings.Split(f[3], ",")[0], "/")
		if addr == "(none)" {
			addr = ""
		}
		list = append(list, PeerStats{PublicKey: f[0], Address: addr, Endpoint: ep, Handshake: hs, RxBytes: rx, TxBytes: tx})
	}
	return list
}

// TunnelUp — туннель жив: рукопожатие не старше 3 минут (WireGuard
// обновляет его раз в 2 минуты при трафике и keepalive).
func TunnelUp(handshake, now int64) bool { return handshake > 0 && now-handshake < 180 }
