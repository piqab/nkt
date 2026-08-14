package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// ipAddrEntry mirrors the JSON `ip -j addr show` emits — one object per
// interface. Only the fields this package actually uses are declared;
// iproute2 adds more (qdisc, group, txqlen, ...) that nothing here needs.
type ipAddrEntry struct {
	IfName   string   `json:"ifname"`
	Flags    []string `json:"flags"`
	MTU      int      `json:"mtu"`
	Address  string   `json:"address"`
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

// Interfaces reads every network interface on the host via `ip -j addr
// show` (structured JSON, not scraped text) plus /proc/net/dev for
// cumulative traffic counters. A plain inventory — physical NICs, bridges,
// VLANs, tunnels, loopback — with no attempt to guess which one is "the
// public one" (Listener.Public() already covers exposure per-socket).
func Interfaces(ctx context.Context, c collect.Collector) ([]model.NetworkInterface, model.SourceStatus) {
	started := time.Now()
	status := model.SourceStatus{Name: "interfaces"}
	defer func() { status.DurationMS = time.Since(started).Milliseconds() }()

	// No `omitempty` on the JSON field this feeds — every return path below
	// must hand back a real (even if empty) slice, never nil, or
	// encoding/json marshals it as `null` and the frontend crashes calling
	// .filter/.map on it.
	ifaces := []model.NetworkInterface{}

	out, err := c.Run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		status.Error = fmt.Sprintf("ip addr: %v", err)
		return ifaces, status
	}
	if !out.OK() {
		status.Error = fmt.Sprintf("ip addr завершился с кодом %d: %s", out.ExitCode, strings.TrimSpace(out.Stderr))
		return ifaces, status
	}

	var entries []ipAddrEntry
	if err := json.Unmarshal([]byte(out.Stdout), &entries); err != nil {
		status.Error = fmt.Sprintf("разбор вывода ip addr: %v", err)
		return ifaces, status
	}
	status.Available = true

	counters := readTrafficCounters(c)

	for _, e := range entries {
		iface := model.NetworkInterface{
			Name: e.IfName,
			MAC:  e.Address,
			MTU:  e.MTU,
		}
		for _, f := range e.Flags {
			switch f {
			case "UP":
				iface.Up = true
			case "LOWER_UP":
				iface.LowerUp = true
			case "LOOPBACK":
				iface.Loopback = true
			}
		}
		for _, a := range e.AddrInfo {
			iface.Addresses = append(iface.Addresses, a.Local+"/"+strconv.Itoa(a.PrefixLen))
		}
		if ctr, ok := counters[e.IfName]; ok {
			iface.RXBytes, iface.TXBytes = ctr.rxBytes, ctr.txBytes
			iface.RXErrors, iface.RXDropped = ctr.rxErrors, ctr.rxDropped
			iface.TXErrors, iface.TXDropped = ctr.txErrors, ctr.txDropped
		}
		ifaces = append(ifaces, iface)
	}

	return ifaces, status
}

type trafficCounters struct {
	rxBytes, txBytes    int64
	rxErrors, rxDropped int64
	txErrors, txDropped int64
}

// readTrafficCounters reads /proc/net/dev — cumulative counters since boot
// for every interface, in a fixed two-header-line text table. A missing or
// unreadable file just means no counters get attached; that is not worth
// failing the whole interface listing over, since `ip addr` already
// succeeded and is the part that actually matters.
//
// Errors and drops are read alongside bytes because they are the one thing
// a raw byte count can never show: an interface can carry plenty of
// traffic and still be quietly losing packets to a bad cable, a
// misconfigured MTU further down the path, or a saturated queue — that
// only shows up in these columns, never in throughput alone.
func readTrafficCounters(c collect.Collector) map[string]trafficCounters {
	raw, err := c.ReadFile("/proc/net/dev")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) < 3 {
		return nil
	}
	out := make(map[string]trafficCounters, len(lines)-2)
	for _, line := range lines[2:] {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		// 16 fields: 8 receive counters (bytes packets errs drop fifo frame
		// compressed multicast), then 8 transmit counters (bytes packets
		// errs drop fifo colls carrier compressed).
		if len(fields) < 16 {
			continue
		}
		parse := func(i int) (int64, bool) {
			v, err := strconv.ParseInt(fields[i], 10, 64)
			return v, err == nil
		}
		rxBytes, ok1 := parse(0)
		rxErrors, ok2 := parse(2)
		rxDropped, ok3 := parse(3)
		txBytes, ok4 := parse(8)
		txErrors, ok5 := parse(10)
		txDropped, ok6 := parse(11)
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 {
			continue
		}
		out[strings.TrimSpace(name)] = trafficCounters{
			rxBytes: rxBytes, txBytes: txBytes,
			rxErrors: rxErrors, rxDropped: rxDropped,
			txErrors: txErrors, txDropped: txDropped,
		}
	}
	return out
}
