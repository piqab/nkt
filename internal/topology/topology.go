// Package topology turns a host snapshot into the network resource map:
// who listens where, what each listener routes to, and which container or
// backend ultimately serves the request.
package topology

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/althq/netknownsthat/internal/model"
)

// Node kinds.
const (
	KindInternet  = "internet"
	KindHost      = "host"
	KindService   = "service"
	KindEndpoint  = "endpoint"
	KindUpstream  = "upstream"
	KindBackend   = "backend"
	KindContainer = "container"
	KindPodman    = "podman_container"
	KindLXD       = "lxd_instance"
	KindVM        = "vm"
	KindNetwork   = "network"
)

// Node statuses, used by the UI to colour the map.
const (
	StatusOK      = "ok"
	StatusWarn    = "warn"
	StatusError   = "error"
	StatusUnknown = "unknown"
)

// Node is one vertex of the resource map.
type Node struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Label    string            `json:"label"`
	Sublabel string            `json:"sublabel,omitempty"`
	Group    string            `json:"group,omitempty"`
	Status   string            `json:"status"`
	Findings int               `json:"findings"`
	Severity string            `json:"severity,omitempty"`
	Port     int               `json:"port,omitempty"`
	Public   bool              `json:"public,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
}

// Edge is one directed link of the resource map.
type Edge struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Label  string `json:"label,omitempty"`
	Status string `json:"status"`
}

// FindingRef is one problem or warning attached to a node, surfaced at the
// top of the resource map — the node itself only carries a colour and a
// count, which says something is wrong but not what, so the map is useless
// for triage without opening every flagged node one at a time.
type FindingRef struct {
	NodeID   string `json:"node_id"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
}

// Graph is the whole map.
type Graph struct {
	Nodes    []Node         `json:"nodes"`
	Edges    []Edge         `json:"edges"`
	Stats    map[string]int `json:"stats"`
	Findings []FindingRef   `json:"findings"`
}

type builder struct {
	nodes    map[string]*Node
	order    []string
	edges    []Edge
	edgeSeen map[string]bool
	bySubj   map[string][]model.Finding
	findings []FindingRef
}

// Build assembles the resource map from a snapshot.
func Build(s *model.Snapshot) *Graph {
	b := &builder{
		nodes:    map[string]*Node{},
		edgeSeen: map[string]bool{},
		bySubj:   map[string][]model.Finding{},
	}
	for _, f := range s.Findings {
		b.bySubj[f.Object] = append(b.bySubj[f.Object], f)
	}

	hostID := "host"
	b.node(Node{
		ID: hostID, Kind: KindHost, Label: s.Host.Hostname, Sublabel: s.Host.OS,
		Status: StatusOK,
		Meta:   map[string]string{"mode": s.Mode, "kernel": s.Host.Kernel},
	})
	b.node(Node{ID: "internet", Kind: KindInternet, Label: "Внешняя сеть", Status: StatusOK})

	listeningPorts := map[int]bool{}
	for _, l := range s.Listeners {
		listeningPorts[l.Port] = true
	}

	// Services own endpoints.
	for _, svc := range s.Services {
		if !svc.Installed && svc.ActiveState == "unknown" {
			continue
		}
		status := StatusOK
		switch svc.ActiveState {
		case "failed":
			status = StatusError
		case "inactive", "unknown":
			status = StatusWarn
		}
		id := "svc:" + svc.Name
		b.node(Node{
			ID: id, Kind: KindService, Label: svc.Name, Sublabel: svc.ActiveState,
			Group: svc.Name, Status: status,
			Meta: map[string]string{"unit": svc.Unit, "enabled": svc.Enabled, "sub_state": svc.SubState},
		})
		b.edge(hostID, id, "runs", "", StatusOK)
	}

	// Containers and docker networks.
	for _, ct := range s.Container {
		id := "ct:" + ct.Name
		status := StatusOK
		switch {
		case ct.State == "restarting" || ct.State == "dead":
			status = StatusError
		case !ct.Running:
			status = StatusWarn
		}
		b.node(Node{
			ID: id, Kind: KindContainer, Label: ct.Name, Sublabel: ct.Image,
			Group: model.ServiceDocker, Status: status,
			Meta: map[string]string{
				"state": ct.State, "status": ct.Status, "project": ct.Project,
				"service": ct.ServiceName, "restart": ct.Restart,
			},
		})
		b.attachFindings(id, ct.Name)
		for _, n := range ct.Networks {
			netID := "net:" + n.Name
			b.node(Node{
				ID: netID, Kind: KindNetwork, Label: n.Name, Group: model.ServiceDocker,
				Status: StatusOK, Sublabel: "docker network",
			})
			label := n.IPAddress
			b.edge(id, netID, "attached", label, StatusOK)
		}
		if b.nodes["svc:docker"] != nil {
			b.edge("svc:docker", id, "manages", "", StatusOK)
		}
	}
	// Podman containers — a separate engine from docker, so its own kind and
	// its own "svc:podman" link rather than folding into KindContainer.
	for _, ct := range s.Podman {
		id := "pod:" + ct.Name
		status := StatusOK
		switch ct.State {
		case "exited", "dead", "stopped":
			status = StatusWarn
		case "restarting":
			status = StatusError
		}
		b.node(Node{
			ID: id, Kind: KindPodman, Label: ct.Name, Sublabel: ct.Image,
			Group: model.ServicePodman, Status: status,
			Meta: map[string]string{"state": ct.State, "status": ct.Status, "pod": ct.Pod},
		})
		b.attachFindings(id, ct.Name)
		if b.nodes["svc:podman"] != nil {
			b.edge("svc:podman", id, "manages", "", StatusOK)
		}
	}

	// LXD instances — containers or VMs, so status only distinguishes
	// running from everything else rather than assuming container semantics.
	for _, inst := range s.LXD {
		id := "lxd:" + inst.Name
		status := StatusOK
		if inst.Status != "running" {
			status = StatusWarn
		}
		b.node(Node{
			ID: id, Kind: KindLXD, Label: inst.Name, Sublabel: inst.Type,
			Group: model.ServiceLXD, Status: status,
			Meta: map[string]string{"status": inst.Status, "type": inst.Type, "architecture": inst.Architecture},
		})
		b.attachFindings(id, inst.Name)
		if b.nodes["svc:lxd"] != nil {
			b.edge("svc:lxd", id, "manages", "", StatusOK)
		}
	}

	// libvirt/QEMU virtual machines.
	for _, vm := range s.VMs {
		id := "vm:" + vm.Name
		status := StatusOK
		switch vm.State {
		case "crashed":
			status = StatusError
		case "shut off", "paused":
			status = StatusWarn
		}
		sub := fmt.Sprintf("%d vCPU", vm.VCPUs)
		b.node(Node{
			ID: id, Kind: KindVM, Label: vm.Name, Sublabel: sub,
			Group: model.ServiceLibvirt, Status: status,
			Meta: map[string]string{
				"state": vm.State, "persistent": strconv.FormatBool(vm.Persistent),
				"autostart": strconv.FormatBool(vm.Autostart), "uuid": vm.UUID,
			},
		})
		b.attachFindings(id, vm.Name)
		if b.nodes["svc:libvirt"] != nil {
			b.edge("svc:libvirt", id, "manages", "", StatusOK)
		}
	}

	for _, n := range s.Networks {
		netID := "net:" + n.Name
		sub := strings.Join(n.Subnets, ", ")
		if node, ok := b.nodes[netID]; ok {
			node.Sublabel = sub
			node.Meta = map[string]string{"driver": n.Driver, "bridge": n.Bridge, "gateway": n.Gateway}
			continue
		}
		b.node(Node{
			ID: netID, Kind: KindNetwork, Label: n.Name, Sublabel: sub,
			Group: model.ServiceDocker, Status: StatusOK,
			Meta: map[string]string{"driver": n.Driver, "bridge": n.Bridge, "gateway": n.Gateway},
		})
	}

	// Upstream pools are built before the endpoints that reference them.
	// Otherwise a route would create a placeholder node first, and the real
	// pool — with its balancing algorithm and health check — would never
	// replace it, leaving every referenced pool labelled "не определён".
	buildUpstreams(b, s, listeningPorts)

	// Endpoints.
	for _, e := range s.Endpoints {
		id := "ep:" + e.ID
		status := StatusOK
		if len(s.Listeners) > 0 && !listeningPorts[e.Port] {
			status = StatusError
		}
		scheme := "tcp"
		if e.TLS {
			scheme = "https"
		} else if e.Mode == "http" {
			scheme = "http"
		}
		meta := map[string]string{
			"service": e.Service, "scheme": scheme, "mode": e.Mode,
			"tls": strconv.FormatBool(e.TLS), "file": e.File,
			"names": strings.Join(e.Names, ", "),
		}
		if hint := unicodeHint(e.Names); hint != "" {
			// server_name/frontend names are ASCII (punycode) whenever the
			// site is an IDN domain — this is the readable form, shown
			// alongside rather than instead of what nginx/haproxy actually use.
			meta["names_unicode"] = hint
		}
		b.node(Node{
			ID: id, Kind: KindEndpoint, Label: e.Label, Sublabel: e.Socket(),
			Group: e.Service, Status: status, Port: e.Port, Public: e.Public(),
			Meta: meta,
		})
		b.attachFindings(id, e.Socket())

		if svc := b.nodes["svc:"+e.Service]; svc != nil {
			b.edge(svc.ID, id, "listens", "", StatusOK)
		} else {
			b.edge(hostID, id, "listens", "", StatusOK)
		}
		if e.Public() {
			b.edge("internet", id, "ingress", scheme, statusOfNode(status))
		}

		// A docker endpoint forwards straight into its container.
		if e.Service == model.ServiceDocker {
			if name := e.Extra["container"]; name != "" {
				b.edge(id, "ct:"+name, "publishes", e.Extra["container_port"], StatusOK)
			}
			continue
		}

		for _, r := range e.Routes {
			switch r.TargetKind {
			case "upstream":
				upID := "up:" + e.Service + ":" + r.Target
				if b.nodes[upID] == nil {
					// Referenced but undefined: show it so the gap is visible.
					b.node(Node{
						ID: upID, Kind: KindUpstream, Label: r.Target,
						Sublabel: "не определён", Group: e.Service, Status: StatusError,
					})
				}
				b.edge(id, upID, "routes", r.Match, StatusOK)
			case "address":
				beID := "be:" + r.Target
				b.node(Node{
					ID: beID, Kind: KindBackend, Label: r.Target, Group: e.Service,
					Status: backendStatus(r.Target, listeningPorts), Sublabel: "прямой адрес",
				})
				b.edge(id, beID, "routes", r.Match, StatusOK)
				b.linkBackendToContainer(s, beID, r.Target)
			}
		}
	}

	g := &Graph{Stats: map[string]int{}}
	for _, id := range b.order {
		g.Nodes = append(g.Nodes, *b.nodes[id])
		g.Stats[b.nodes[id].Kind]++
	}
	g.Edges = b.edges
	g.Stats["edges"] = len(b.edges)
	g.Findings = b.findings
	sort.SliceStable(g.Findings, func(i, j int) bool {
		return model.SeverityRank(g.Findings[i].Severity) < model.SeverityRank(g.Findings[j].Severity)
	})
	sort.SliceStable(g.Nodes, func(i, j int) bool {
		if g.Nodes[i].Kind != g.Nodes[j].Kind {
			return kindRank(g.Nodes[i].Kind) < kindRank(g.Nodes[j].Kind)
		}
		return g.Nodes[i].Label < g.Nodes[j].Label
	})
	return g
}

// buildUpstreams adds every declared pool and its members.
func buildUpstreams(b *builder, s *model.Snapshot, listeningPorts map[int]bool) {
	for _, u := range s.Upstreams {
		upID := "up:" + u.Service + ":" + u.Name
		sub := u.Algorithm
		if u.Health != "" {
			sub += " · " + u.Health
		}
		if len(u.Servers) == 0 {
			// A pool that answers by itself, such as an haproxy backend built
			// from http-request return.
			sub = strings.TrimSpace(sub + " · без backend-серверов")
		}
		status := StatusOK
		if u.Health == "" && len(u.Servers) > 1 {
			status = StatusWarn
		}
		b.node(Node{
			ID: upID, Kind: KindUpstream, Label: u.Name, Sublabel: sub,
			Group: u.Service, Status: status,
			Meta: map[string]string{"file": u.File, "mode": u.Mode, "servers": strconv.Itoa(len(u.Servers))},
		})
		b.attachFindings(upID, u.Name)

		for _, srv := range u.Servers {
			beID := "be:" + srv.Socket()
			label := srv.Name
			if label == "" {
				label = srv.Socket()
			}
			st := backendStatus(srv.Socket(), listeningPorts)
			if srv.Down {
				st = StatusError
			}
			b.node(Node{
				ID: beID, Kind: KindBackend, Label: srv.Socket(), Sublabel: label,
				Group: u.Service, Status: st, Port: srv.Port,
				Meta: map[string]string{
					"backup": strconv.FormatBool(srv.Backup),
					"check":  strconv.FormatBool(srv.Checked),
				},
			})
			edgeLabel := ""
			if srv.Backup {
				edgeLabel = "backup"
			}
			b.edge(upID, beID, "member", edgeLabel, st)
			b.linkBackendToContainer(s, beID, srv.Socket())
		}
	}
}

// linkBackendToContainer connects a backend address to the container that
// actually serves it, either through a published host port or a container IP.
func (b *builder) linkBackendToContainer(s *model.Snapshot, beID, socket string) {
	host, portStr, ok := strings.Cut(socket, ":")
	if !ok {
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return
	}
	for _, ct := range s.Container {
		for _, p := range ct.Ports {
			if p.HostPort == port && (isLoopback(host) || p.HostIP == host || p.HostIP == "" || p.HostIP == "0.0.0.0") {
				b.edge(beID, "ct:"+ct.Name, "served-by", fmt.Sprintf("%d→%d", p.HostPort, p.ContainerPort), StatusOK)
				return
			}
		}
		for _, n := range ct.Networks {
			if n.IPAddress != "" && n.IPAddress == host {
				b.edge(beID, "ct:"+ct.Name, "served-by", portStr, StatusOK)
				return
			}
		}
	}
}

func (b *builder) node(n Node) {
	if existing, ok := b.nodes[n.ID]; ok {
		// Keep the worst status seen for a node touched by several rules.
		if statusRank(n.Status) > statusRank(existing.Status) {
			existing.Status = n.Status
		}
		if existing.Sublabel == "" {
			existing.Sublabel = n.Sublabel
		}
		return
	}
	if n.Status == "" {
		n.Status = StatusUnknown
	}
	cp := n
	b.nodes[n.ID] = &cp
	b.order = append(b.order, n.ID)
}

func (b *builder) edge(from, to, kind, label, status string) {
	if from == "" || to == "" || from == to {
		return
	}
	id := fmt.Sprintf("%s->%s:%s:%s", from, to, kind, label)
	if b.edgeSeen[id] {
		return
	}
	b.edgeSeen[id] = true
	if status == "" {
		status = StatusOK
	}
	b.edges = append(b.edges, Edge{ID: id, From: from, To: to, Kind: kind, Label: label, Status: status})
}

// attachFindings records how many problems point at a node and the worst severity.
func (b *builder) attachFindings(nodeID, subject string) {
	list := b.bySubj[subject]
	if len(list) == 0 {
		return
	}
	n := b.nodes[nodeID]
	if n == nil {
		return
	}
	n.Findings = len(list)
	worst := list[0].Severity
	for _, f := range list {
		if model.SeverityRank(f.Severity) < model.SeverityRank(worst) {
			worst = f.Severity
		}
		b.findings = append(b.findings, FindingRef{NodeID: nodeID, Title: f.Title, Severity: f.Severity})
	}
	n.Severity = worst
	switch worst {
	case model.SeverityCritical, model.SeverityHigh:
		n.Status = StatusError
	case model.SeverityMedium:
		if n.Status == StatusOK {
			n.Status = StatusWarn
		}
	}
}

func backendStatus(socket string, listening map[int]bool) string {
	host, portStr, ok := strings.Cut(socket, ":")
	if !ok || len(listening) == 0 {
		return StatusUnknown
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return StatusUnknown
	}
	if !isLoopback(host) {
		// Remote backends cannot be judged from the local socket table.
		return StatusUnknown
	}
	if listening[port] {
		return StatusOK
	}
	return StatusError
}

// unicodeHint decodes any punycode labels in names back to readable form,
// e.g. "xn--80akhbyknj4f.xn--p1ai" -> "испытание.рф". Names with nothing to
// decode are dropped, and an all-ASCII list yields "".
func unicodeHint(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if u := model.HostnameUnicode(n); u != "" {
			out = append(out, u)
		}
	}
	return strings.Join(out, ", ")
}

func isLoopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

func statusOfNode(s string) string {
	if s == "" {
		return StatusOK
	}
	return s
}

func statusRank(s string) int {
	switch s {
	case StatusError:
		return 3
	case StatusWarn:
		return 2
	case StatusOK:
		return 1
	default:
		return 0
	}
}

func kindRank(kind string) int {
	switch kind {
	case KindInternet:
		return 0
	case KindHost:
		return 1
	case KindService:
		return 2
	case KindEndpoint:
		return 3
	case KindUpstream:
		return 4
	case KindBackend:
		return 5
	case KindContainer:
		return 6
	case KindPodman:
		return 7
	case KindLXD:
		return 8
	case KindVM:
		return 9
	case KindNetwork:
		return 10
	default:
		return 11
	}
}
