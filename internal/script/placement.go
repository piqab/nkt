package script

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// The cluster placement across hosts in a script is the same table the
// "Clusters" section shows, written as one line:
//
//	nodes "hv1: cp 1 cpu 4 mem 8192 disk 60 image debian-13, w 2; hv2: w 2, bridge br0; hv3: host w, endpoint 10.0.0.7; hv4: lb"
//
// Hosts are separated by ";", entries inside a host by ",": "cp N" / "w N"
// are machines with a role, "lb [N]" is the built-in HAProxy load balancer
// machine (N is 1 or 2; two machines without a VIP the form can't do
// either), "host cp|w" is the host itself as a node. A machine entry can
// carry its own "cpu N", "mem MB", "disk GB", "image IMAGE" and "network
// NETWORK" (a libvirt NAT network name); whatever is not named comes from
// the command's shared arguments. The host-line keys — "bridge NAME" (the
// bridge in bridge mode), "nic NAME" (the macvtap device), "endpoint ADDR"
// (the host address for WireGuard tunnel peers) — are shared by every entry
// of that host.

// PlacementRow is one placement row.
type PlacementRow struct {
	Host     string
	Role     string // control-plane | worker | load-balancer
	Kind     string // vm | host
	Count    int
	Bridge   string
	Nic      string
	Endpoint string
	// The entry's own machine parameters; zero/empty means the shared
	// command arguments (cpu/mem/disk/image) apply.
	VCPUs   int
	MemMB   int
	DiskGB  int
	ImageID string
	// Network is the libvirt NAT network name (nat mode); in wireguard/
	// bridge/macvtap modes the network comes from the host-line keys
	// (bridge/nic) or the mode itself.
	Network string
}

// IsPlacement — nodes is written as a placement across hosts, not as a
// topology.
func IsPlacement(nodes string) bool { return strings.Contains(nodes, ":") }

// placementImageRe — an image identifier: a name from the host catalog or
// "hub:file" from the hub's image library.
var placementImageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+/-]{0,127}$`)

// ParsePlacement parses a placement string.
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
		bridge, nic, endpoint := "", "", ""
		var rows []PlacementRow
		for _, item := range strings.Split(items, ",") {
			f := strings.Fields(item)
			if len(f) == 0 {
				return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
			}
			switch f[0] {
			case "cp", "control-plane", "w", "worker", "lb", "load-balancer":
				row, err := placementEntry(host, f)
				if err != nil {
					return nil, err
				}
				rows = append(rows, row)
			case "host":
				// The host itself can only be a node — the load balancer
				// machine is always a VM (validateLB refuses kind=host).
				if len(f) != 2 || !isNodeRole(placementRole(f[1])) {
					return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
				}
				rows = append(rows, PlacementRow{Host: host, Role: placementRole(f[1]), Kind: "host", Count: 1})
			case "bridge":
				if len(f) != 2 || !nameRe.MatchString(f[1]) {
					return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
				}
				bridge = f[1]
			case "nic":
				if len(f) != 2 || !nameRe.MatchString(f[1]) {
					return nil, msgs.Errorf("script.badPlacementItem", strings.TrimSpace(item))
				}
				nic = f[1]
			case "endpoint":
				if len(f) != 2 || !addrRe.MatchString(f[1]) {
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
			rows[i].Bridge, rows[i].Nic, rows[i].Endpoint = bridge, nic, endpoint
		}
		out = append(out, rows...)
	}
	if len(out) == 0 {
		return nil, msgs.Errorf("script.badPlacementHost", s)
	}
	return out, nil
}

// placementRole — the role of an entry; empty string for an unknown word.
func placementRole(s string) string {
	switch s {
	case "cp", "control-plane":
		return "control-plane"
	case "w", "worker":
		return "worker"
	case "lb", "load-balancer":
		return "load-balancer"
	}
	return ""
}

// isNodeRole — a cluster node role (not the load balancer).
func isNodeRole(role string) bool { return role == "control-plane" || role == "worker" }

// placementEntry — a machine role entry: "cp 3 cpu 4 mem 8192 disk 60
// image debian-13 network lab-net". cp/w require the count, lb does not
// (one machine) and never takes more than two.
func placementEntry(host string, f []string) (PlacementRow, error) {
	bad := func() (PlacementRow, error) {
		return PlacementRow{}, msgs.Errorf("script.badPlacementItem", strings.Join(f, " "))
	}
	role := placementRole(f[0])
	row := PlacementRow{Host: host, Role: role, Kind: "vm"}
	i := 1
	if role == "load-balancer" {
		row.Count = 1
		// The count is optional for lb — a non-numeric next token is
		// already the first key of the entry.
		if i < len(f) {
			if n, err := strconv.Atoi(f[i]); err == nil {
				if n < 1 || n > 2 {
					return bad()
				}
				row.Count, i = n, i+1
			}
		}
	} else {
		if i >= len(f) {
			return bad()
		}
		n, err := strconv.Atoi(f[i])
		if err != nil || n < 1 || n > 20 {
			return bad()
		}
		row.Count, i = n, i+1
	}
	for ; i < len(f); i += 2 {
		if i+1 >= len(f) {
			return bad()
		}
		k, v := f[i], f[i+1]
		n, err := strconv.Atoi(v)
		switch {
		case k == "cpu" && err == nil && n >= 1 && n <= 512:
			row.VCPUs = n
		case k == "mem" && err == nil && n >= 128 && n <= 4194304:
			row.MemMB = n
		case k == "disk" && err == nil && n >= 1 && n <= 65536:
			row.DiskGB = n
		case k == "image" && placementImageRe.MatchString(v):
			row.ImageID = v
		case k == "network" && nameRe.MatchString(v):
			row.Network = v
		default:
			return bad()
		}
	}
	return row, nil
}

// addrRe — a hostname or an IP without a port.
var addrRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,253}$`)
