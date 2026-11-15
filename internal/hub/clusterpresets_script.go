package hub

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// A saved cluster preset opened as a script: the whole form JSON turns
// into one "on HOST k8s create …" line (plus the Cilium values block when
// the preset has one), so it can be checked, dry-run, saved and executed
// in the script editor like any other script.

// clusterPresetRow — a placement row of a preset: the form's Row with the
// host id replaced by the host name.
type clusterPresetRow struct {
	Host     string `json:"host"`
	Role     string `json:"role"`
	Kind     string `json:"kind"`
	Count    int    `json:"count"`
	VCPUs    int    `json:"vcpus"`
	MemoryMB int    `json:"memory_mb"`
	DiskGB   int    `json:"disk_gb"`
	ImageID  string `json:"image_id"`
	Network  string `json:"network"`
	Bridge   string `json:"bridge"`
	Nic      string `json:"nic"`
}

// clusterPresetForm — the JSON of the whole "new cluster" form as saved
// into a preset (the web's PresetForm). Cilium is a pointer: presets from
// before the field existed meant "cilium on" (the form's default). The
// legacy HA-pair fields (lb_count > 1, lb_vip) are read-only leftovers.
type clusterPresetForm struct {
	Name       string `json:"name"`
	Flavor     string `json:"flavor"`
	K8sVersion string `json:"k8s_version"`
	Cilium     *bool  `json:"cilium"`
	KPR        bool   `json:"kpr"`
	Addons     map[string]struct {
		On      bool   `json:"on"`
		Version string `json:"version"`
	} `json:"addons"`
	Longhorn *struct {
		On      bool   `json:"on"`
		Version string `json:"version"`
	} `json:"longhorn"`
	Network   string             `json:"network"`
	Rows      []clusterPresetRow `json:"rows"`
	Endpoints map[string]string  `json:"endpoints"`
	Expose    bool               `json:"expose"`
	Ports     struct {
		API   *int `json:"api"`
		HTTP  *int `json:"http"`
		HTTPS *int `json:"https"`
	} `json:"ports"`
	Endpoint         string             `json:"endpoint"`
	GatewayAddr      string             `json:"gateway_addr"`
	Allow            string             `json:"allow"`
	EndpointInternal bool               `json:"endpoint_internal"`
	LBEnabled        bool               `json:"lb_enabled"`
	LBCount          int                `json:"lb_count"`
	LBVip            string             `json:"lb_vip"`
	LBRows           []clusterPresetRow `json:"lb_rows"`
	LBHaproxy        *struct {
		Connect   int    `json:"connect"`
		Client    int    `json:"client"`
		Server    int    `json:"server"`
		MaxConn   int    `json:"maxConn"`
		StatsPort int    `json:"statsPort"`
		Algorithm string `json:"algorithm"`
	} `json:"lb_haproxy"`
	CiliumValues string `json:"cilium_values"`
}

// presetScript — the script text of a saved preset. Hosts stay names and
// the gateway stays an address, exactly as the preset stores them: the
// script resolves both at run time and survives a host/gateway being
// recreated, same as the preset does.
func presetScript(form string, lang msgs.Lang) (string, error) {
	var f clusterPresetForm
	if err := json.Unmarshal([]byte(form), &f); err != nil {
		return "", msgs.Errorf("hub.presetScriptBroken", err)
	}
	if len(f.Rows) == 0 {
		return "", msgs.Errorf("hub.presetScriptNoRows")
	}
	name := strings.TrimSpace(f.Name)
	if name == "" {
		name = "k8s"
	}
	nodes, err := presetNodes(f)
	if err != nil {
		return "", err
	}
	cilium := f.Cilium == nil || *f.Cilium

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", msgs.T(lang, "hub.presetScriptHeader", name))
	if f.LBVip != "" || f.LBCount > 1 {
		fmt.Fprintf(&b, "# %s\n", msgs.T(lang, "hub.presetScriptLBVip"))
	}
	args := []string{"on", firstPresetHost(f), "k8s", "create", name}
	if f.Flavor != "" {
		args = append(args, "flavor", f.Flavor)
	}
	args = append(args, "nodes", strconv.Quote(nodes))
	if f.Network != "" {
		args = append(args, "network", f.Network)
	}
	if f.K8sVersion != "" {
		args = append(args, "version", f.K8sVersion)
	}
	if cilium {
		args = append(args, "cni", "cilium")
		if f.KPR {
			args = append(args, "no-kube-proxy")
		}
	}
	if f.Endpoint != "" {
		args = append(args, "endpoint", f.Endpoint)
	}
	if f.GatewayAddr != "" {
		args = append(args, "gateway", f.GatewayAddr)
	}
	if f.EndpointInternal {
		args = append(args, "internal")
	}
	if f.Allow != "" {
		args = append(args, "allow", strings.ReplaceAll(f.Allow, " ", ""))
	}
	if a := presetAddons(f); a != "" {
		args = append(args, "addons", a)
	}
	if hp := presetHaproxy(f); hp != "" {
		args = append(args, "haproxy", hp)
	}
	if f.Expose {
		args = append(args, "expose")
		if p := f.Ports.API; p != nil && *p > 0 {
			args = append(args, "api", strconv.Itoa(*p))
		}
		if p := f.Ports.HTTP; p != nil && *p > 0 {
			args = append(args, "http", strconv.Itoa(*p))
		}
		if p := f.Ports.HTTPS; p != nil && *p > 0 {
			args = append(args, "https", strconv.Itoa(*p))
		}
	}
	b.WriteString(strings.Join(args, " "))
	b.WriteString("\n")
	if cilium && strings.TrimSpace(f.CiliumValues) != "" {
		b.WriteString(strings.TrimRight(f.CiliumValues, "\n"))
		b.WriteString("\nend\n")
	}
	return b.String(), nil
}

// firstPresetHost — the host the "on" prefix runs the creation from: the
// first host of the placement, like the form's own job does.
func firstPresetHost(f clusterPresetForm) string {
	for _, r := range f.Rows {
		if r.Host != "" {
			return r.Host
		}
	}
	for _, r := range f.LBRows {
		if r.Host != "" {
			return r.Host
		}
	}
	return ""
}

// presetNodes — the placement string: rows grouped by host in order of
// appearance, load-balancer rows join the group of their host, host-line
// keys (bridge/nic/endpoint) come from the rows and the endpoints map.
func presetNodes(f clusterPresetForm) (string, error) {
	type group struct {
		entries  []string
		bridge   string
		nic      string
		endpoint string
	}
	order := []string{}
	groups := map[string]*group{}
	add := func(r clusterPresetRow) error {
		if r.Host == "" {
			return msgs.Errorf("hub.presetScriptNoRows")
		}
		e, err := presetEntry(r)
		if err != nil {
			return err
		}
		g, ok := groups[r.Host]
		if !ok {
			g = &group{endpoint: f.Endpoints[r.Host]}
			groups[r.Host] = g
			order = append(order, r.Host)
		}
		g.entries = append(g.entries, e)
		if g.bridge == "" {
			g.bridge = r.Bridge
		}
		if g.nic == "" {
			g.nic = r.Nic
		}
		return nil
	}
	for _, r := range f.Rows {
		if err := add(r); err != nil {
			return "", err
		}
	}
	if f.LBEnabled {
		for _, r := range f.LBRows {
			if r.Role == "" {
				r.Role = RoleLoadBalancer
			}
			if err := add(r); err != nil {
				return "", err
			}
		}
	}
	var hosts []string
	for _, h := range order {
		g := groups[h]
		items := append([]string{}, g.entries...)
		if g.bridge != "" {
			items = append(items, "bridge "+g.bridge)
		}
		if g.nic != "" {
			items = append(items, "nic "+g.nic)
		}
		if g.endpoint != "" {
			items = append(items, "endpoint "+g.endpoint)
		}
		hosts = append(hosts, h+": "+strings.Join(items, ", "))
	}
	return strings.Join(hosts, "; "), nil
}

// presetEntry — one placement entry with its own parameters.
func presetEntry(r clusterPresetRow) (string, error) {
	var e []string
	switch {
	case r.Role == RoleLoadBalancer:
		e = append(e, "lb")
		if r.Count > 1 {
			e = append(e, strconv.Itoa(r.Count))
		}
	case r.Kind == KindHost:
		short, err := presetRoleShort(r.Role)
		if err != nil {
			return "", err
		}
		e = append(e, "host", short)
	default:
		short, err := presetRoleShort(r.Role)
		if err != nil {
			return "", err
		}
		n := r.Count
		if n < 1 {
			n = 1
		}
		e = append(e, short, strconv.Itoa(n))
	}
	if r.Kind != KindHost {
		if r.VCPUs > 0 {
			e = append(e, "cpu", strconv.Itoa(r.VCPUs))
		}
		if r.MemoryMB > 0 {
			e = append(e, "mem", strconv.Itoa(r.MemoryMB))
		}
		if r.DiskGB > 0 {
			e = append(e, "disk", strconv.Itoa(r.DiskGB))
		}
		if r.ImageID != "" {
			e = append(e, "image", r.ImageID)
		}
		if r.Network != "" {
			e = append(e, "network", r.Network)
		}
	}
	return strings.Join(e, " "), nil
}

func presetRoleShort(role string) (string, error) {
	switch role {
	case RoleControlPlane:
		return "cp", nil
	case RoleWorker:
		return "w", nil
	}
	return "", msgs.Errorf("hub.presetScriptBadRole", role)
}

// presetAddons — the "addons" argument: registry names sorted for a
// stable text, each with its version when the preset pinned one. The
// legacy longhorn-only field is carried over when the map is absent.
func presetAddons(f clusterPresetForm) string {
	var names []string
	for name, a := range f.Addons {
		if a.On {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var items []string
	for _, name := range names {
		if v := f.Addons[name].Version; v != "" {
			items = append(items, name+"="+v)
		} else {
			items = append(items, name)
		}
	}
	if len(items) == 0 && f.Longhorn != nil && f.Longhorn.On {
		if f.Longhorn.Version != "" {
			return "longhorn=" + f.Longhorn.Version
		}
		return "longhorn"
	}
	return strings.Join(items, ",")
}

// presetHaproxy — the "haproxy" argument from the form's knobs; zero
// values are left out so the runner's defaults apply.
func presetHaproxy(f clusterPresetForm) string {
	if !f.LBEnabled || f.LBHaproxy == nil {
		return ""
	}
	h := f.LBHaproxy
	var items []string
	if h.Connect > 0 {
		items = append(items, "connect="+strconv.Itoa(h.Connect))
	}
	if h.Client > 0 {
		items = append(items, "client="+strconv.Itoa(h.Client))
	}
	if h.Server > 0 {
		items = append(items, "server="+strconv.Itoa(h.Server))
	}
	if h.MaxConn > 0 {
		items = append(items, "maxconn="+strconv.Itoa(h.MaxConn))
	}
	if h.StatsPort > 0 {
		items = append(items, "stats="+strconv.Itoa(h.StatsPort))
	}
	if h.Algorithm != "" {
		items = append(items, "algo="+h.Algorithm)
	}
	return strings.Join(items, ",")
}
