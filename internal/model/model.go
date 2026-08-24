// Package model holds the vendor-neutral description of everything found on the
// host. Parsers produce it, the analyzer annotates it, the topology builder and
// the HTTP API consume it.
package model

import (
	"fmt"
	"strings"
	"time"
)

// Service identifiers used throughout the application.
const (
	ServiceNginx     = "nginx"
	ServiceHAProxy   = "haproxy"
	ServiceCaddy     = "caddy"
	ServiceDocker    = "docker"
	ServicePodman    = "podman"
	ServiceLXD       = "lxd"
	ServiceLibvirt   = "libvirt"
	ServiceIptables  = "iptables"
	ServiceUFW       = "ufw"
	ServiceFirewalld = "firewalld"
	ServiceHost      = "host"
)

// Severity levels for findings, ordered from worst to least important.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"
)

// SeverityRank orders severities for sorting; lower is worse.
func SeverityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityHigh:
		return 1
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 3
	default:
		return 4
	}
}

// SourceStatus reports how one parser fared during a scan.
type SourceStatus struct {
	Name       string   `json:"name"`
	Available  bool     `json:"available"`
	Version    string   `json:"version,omitempty"`
	Files      []string `json:"files,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	Error      string   `json:"error,omitempty"`
	DurationMS int64    `json:"duration_ms"`
}

// ManagedFile is a config file the dashboard can show, diff and edit.
type ManagedFile struct {
	Path     string    `json:"path"`
	Service  string    `json:"service"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	SHA256   string    `json:"sha256"`
	Editable bool      `json:"editable"`
	Readable bool      `json:"readable"`
	Note     string    `json:"note,omitempty"`
	// Sites is every distinct server_name/host this file declares —
	// gathered from Endpoint.Names across every Endpoint whose File
	// matches this one's Path (see inventory.attachSiteNames), not parsed
	// independently here: Endpoint already is the parsed, per-service
	// understanding of "server_name"/"host"/whatever each backend calls
	// it, so this just re-groups that same data by file instead of
	// re-deriving it.
	Sites []SiteName `json:"sites,omitempty"`
}

// SiteName is one server_name/host declared in a ManagedFile, with its
// readable Unicode form alongside the ASCII/punycode form the config
// itself actually contains, when they differ — see HostnameUnicode.
type SiteName struct {
	Name        string `json:"name"`
	NameUnicode string `json:"name_unicode,omitempty"`
}

// AttachSiteNames groups every Endpoint.Names entry by which file it came
// from (Endpoint.File, set by each parser to the same path a ManagedFile
// carries as Path) and fills in each matching ManagedFile.Sites in place —
// shared by the inventory scanner (attaching to its own snapshot) and the
// Configs API handler (attaching to ConfigManager.List's separately-sourced
// file listing against the latest scan's Endpoints), so both end up with
// identically-computed site lists rather than two subtly different
// implementations of the same join. Order-preserving and de-duplicated per
// file: a file can easily declare the same name twice (an HTTP and an HTTPS
// server block for the same domain, say), which would otherwise repeat the
// line for no informational gain.
func AttachSiteNames(files []ManagedFile, endpoints []Endpoint) {
	if len(endpoints) == 0 {
		return
	}
	namesByFile := make(map[string][]string, len(files))
	seen := make(map[string]map[string]bool, len(files))
	for _, e := range endpoints {
		if e.File == "" {
			continue
		}
		if seen[e.File] == nil {
			seen[e.File] = map[string]bool{}
		}
		for _, name := range e.Names {
			if name == "" || seen[e.File][name] {
				continue
			}
			seen[e.File][name] = true
			namesByFile[e.File] = append(namesByFile[e.File], name)
		}
	}
	for i := range files {
		names := namesByFile[files[i].Path]
		if len(names) == 0 {
			continue
		}
		sites := make([]SiteName, len(names))
		for j, name := range names {
			sites[j] = SiteName{Name: name, NameUnicode: HostnameUnicode(name)}
		}
		files[i].Sites = sites
	}
}

// Route is one routing decision inside an endpoint: an nginx location, an
// haproxy use_backend rule, or a default backend.
type Route struct {
	Match      string `json:"match"`
	Target     string `json:"target"`
	TargetKind string `json:"target_kind"` // upstream | address | redirect | deny | static | unknown
	Condition  string `json:"condition,omitempty"`
	File       string `json:"file,omitempty"`
	Line       int    `json:"line,omitempty"`
}

// Endpoint is a socket a service declares it will listen on.
type Endpoint struct {
	ID        string            `json:"id"`
	Service   string            `json:"service"`
	Kind      string            `json:"kind"` // server | frontend | listen | published-port
	Address   string            `json:"address"`
	Port      int               `json:"port"`
	Protocol  string            `json:"protocol"` // tcp | udp
	TLS       bool              `json:"tls"`
	Mode      string            `json:"mode"` // http | tcp
	Names     []string          `json:"names,omitempty"`
	Routes    []Route           `json:"routes,omitempty"`
	Upstream  []string          `json:"upstreams,omitempty"`
	File      string            `json:"file,omitempty"`
	Line      int               `json:"line,omitempty"`
	Label     string            `json:"label"`
	AccessLog []string          `json:"access_logs,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// Public reports whether the endpoint is reachable from outside the host.
func (e Endpoint) Public() bool {
	switch e.Address {
	case "", "*", "0.0.0.0", "::", "[::]", "::0":
		return true
	}
	return false
}

// Loopback reports whether the endpoint is bound to localhost only.
func (e Endpoint) Loopback() bool {
	return strings.HasPrefix(e.Address, "127.") || e.Address == "::1" || e.Address == "[::1]"
}

// Socket renders address:port.
func (e Endpoint) Socket() string { return fmt.Sprintf("%s:%d", e.Address, e.Port) }

// UpstreamServer is one member of a backend pool.
type UpstreamServer struct {
	Name    string   `json:"name,omitempty"`
	Host    string   `json:"host"`
	Port    int      `json:"port"`
	Weight  int      `json:"weight,omitempty"`
	Backup  bool     `json:"backup"`
	Down    bool     `json:"down"`
	Checked bool     `json:"checked"`
	Params  []string `json:"params,omitempty"`
}

// Socket renders host:port.
func (s UpstreamServer) Socket() string { return fmt.Sprintf("%s:%d", s.Host, s.Port) }

// Upstream is a named pool of backend servers.
type Upstream struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Service   string           `json:"service"`
	Mode      string           `json:"mode,omitempty"`
	Algorithm string           `json:"algorithm,omitempty"`
	Health    string           `json:"health,omitempty"`
	Servers   []UpstreamServer `json:"servers"`
	File      string           `json:"file,omitempty"`
	Line      int              `json:"line,omitempty"`
}

// PortMapping is a container port published on the host.
type PortMapping struct {
	HostIP        string `json:"host_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

// Published reports whether the mapping actually reaches the host network.
func (p PortMapping) Published() bool { return p.HostPort > 0 }

// PublicallyBound reports whether the mapping is exposed on every interface.
func (p PortMapping) PublicallyBound() bool {
	return p.Published() && (p.HostIP == "" || p.HostIP == "0.0.0.0" || p.HostIP == "::")
}

// ContainerNetwork is a container's attachment to a docker network.
type ContainerNetwork struct {
	Name      string `json:"name"`
	IPAddress string `json:"ip_address,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
}

// Container is a docker workload, either running, declared in compose, or both.
type Container struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Image       string             `json:"image"`
	State       string             `json:"state"` // running | exited | restarting | declared
	Status      string             `json:"status"`
	Created     int64              `json:"created,omitempty"`
	Project     string             `json:"project,omitempty"`
	ServiceName string             `json:"service_name,omitempty"`
	ComposeFile string             `json:"compose_file,omitempty"`
	Restart     string             `json:"restart,omitempty"`
	Ports       []PortMapping      `json:"ports,omitempty"`
	Networks    []ContainerNetwork `json:"networks,omitempty"`
	DependsOn   []string           `json:"depends_on,omitempty"`
	Declared    bool               `json:"declared"` // present in a compose file
	Running     bool               `json:"running"`  // present in the docker daemon
}

// DockerNetwork is a docker bridge/overlay network.
type DockerNetwork struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Driver   string   `json:"driver"`
	Scope    string   `json:"scope"`
	Internal bool     `json:"internal"`
	Subnets  []string `json:"subnets,omitempty"`
	Gateway  string   `json:"gateway,omitempty"`
	Bridge   string   `json:"bridge,omitempty"`
	Project  string   `json:"project,omitempty"`
}

// PodmanContainer is a Podman workload. Podman is a separate engine from
// Docker (not a client for it) and adds one concept Docker lacks: pods —
// groups of containers sharing a network namespace, similar to a Kubernetes
// pod. There is no declared/running duality here the way there is for
// Container: Podman has no first-class compose-file concept in this
// application (Quadlet systemd units are out of scope for v1), so every
// entry is simply what the engine reports right now.
type PodmanContainer struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Image    string             `json:"image"`
	State    string             `json:"state"` // running | exited | paused | created
	Status   string             `json:"status"`
	Created  int64              `json:"created,omitempty"`
	Pod      string             `json:"pod,omitempty"`
	Ports    []PortMapping      `json:"ports,omitempty"`
	Networks []ContainerNetwork `json:"networks,omitempty"`
}

// LXDInstance is a container or virtual machine managed by LXD — unlike
// libvirt/QEMU (a separate, dedicated VM technology this application also
// supports) or Podman, LXD ≥4.0 can run either kind of workload under one
// tool, which is why Type is a field here rather than implied by the model.
type LXDInstance struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"` // container | virtual-machine
	Status       string   `json:"status"`
	Architecture string   `json:"architecture,omitempty"`
	IPv4         []string `json:"ipv4,omitempty"`
}

// VMDisk is one storage device attached to a libvirt/QEMU domain.
type VMDisk struct {
	Device string `json:"device"` // disk | cdrom
	Source string `json:"source,omitempty"`
	Bus    string `json:"bus,omitempty"` // virtio | sata | scsi | ide
}

// VMNetIface is one network interface attached to a libvirt/QEMU domain.
type VMNetIface struct {
	Source string `json:"source,omitempty"` // bridge/network name
	MAC    string `json:"mac,omitempty"`
	Model  string `json:"model,omitempty"`
}

// VirtualMachine is a libvirt/QEMU domain, whether running or only defined.
// Unlike Container's declared/running split (compose file vs docker daemon),
// libvirt itself is the single source of truth for both a domain's
// definition and its runtime state — "defined but not running" is just
// State == "shut off" on a Persistent domain, not a separate concept to
// reconcile.
type VirtualMachine struct {
	Name       string       `json:"name"`
	UUID       string       `json:"uuid,omitempty"`
	State      string       `json:"state"` // running | shut off | paused | crashed | ...
	Persistent bool         `json:"persistent"`
	Autostart  bool         `json:"autostart"`
	VCPUs      int          `json:"vcpus,omitempty"`
	MemoryKB   int64        `json:"memory_kb,omitempty"`
	Disks      []VMDisk     `json:"disks,omitempty"`
	Networks   []VMNetIface `json:"networks,omitempty"`
}

// FirewallPolicy is a chain's default policy plus its counters.
type FirewallPolicy struct {
	Backend string `json:"backend"`
	Table   string `json:"table"`
	Chain   string `json:"chain"`
	Policy  string `json:"policy"`
	Packets int64  `json:"packets"`
	Bytes   int64  `json:"bytes"`
}

// FirewallRule is one normalised packet-filter rule.
type FirewallRule struct {
	ID          string `json:"id"`
	Backend     string `json:"backend"` // iptables | ip6tables | ufw | firewalld
	Table       string `json:"table,omitempty"`
	Chain       string `json:"chain"`
	Order       int    `json:"order"`
	Action      string `json:"action"` // ACCEPT | DROP | REJECT | DNAT | LOG | RETURN | jump target
	Protocol    string `json:"protocol,omitempty"`
	PortSpec    string `json:"port_spec,omitempty"`
	Ports       []int  `json:"ports,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	InIface     string `json:"in_iface,omitempty"`
	OutIface    string `json:"out_iface,omitempty"`
	DNATTo      string `json:"dnat_to,omitempty"`
	Comment     string `json:"comment,omitempty"`
	// Zone is firewalld-only — the zone (public, trusted, dmz, ...) a rule
	// belongs to. Empty for every other backend, which have no such concept.
	Zone      string `json:"zone,omitempty"`
	Permanent bool   `json:"permanent,omitempty"`
	Runtime   bool   `json:"runtime,omitempty"`
	Packets   int64  `json:"packets"`
	Bytes     int64  `json:"bytes"`
	Raw       string `json:"raw"`
	ManagedBy string `json:"managed_by,omitempty"` // docker | ufw | firewalld | manual
}

// FirewallManagerState is one high-level firewall manager's own summary —
// ufw and firewalld (and anything added later) each report through one of
// these, so the Firewall page and TUI overview iterate a list instead of
// hardcoding which managers exist. A third manager is a new entry in the
// slice, not new sibling fields on FirewallState.
type FirewallManagerState struct {
	Name      string `json:"name"` // ufw | firewalld
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	// Policy is a short human-readable summary of the default posture:
	// ufw's own "Default: deny (incoming), allow (outgoing)" line, or
	// firewalld's default zone name.
	Policy string `json:"policy,omitempty"`
}

// FirewallState is the whole packet-filter picture.
type FirewallState struct {
	Backends []string               `json:"backends"`
	Managers []FirewallManagerState `json:"managers"`
	Policies []FirewallPolicy       `json:"policies"`
	Rules    []FirewallRule         `json:"rules"`
}

// Manager returns the named manager's state, or a zero value (Installed
// and Active both false) if this snapshot never saw one by that name —
// callers don't need a separate "found" bool, since a manager that was
// never even checked is indistinguishable from one confirmed absent for
// every purpose this is used for.
func (f FirewallState) Manager(name string) FirewallManagerState {
	for _, m := range f.Managers {
		if m.Name == name {
			return m
		}
	}
	return FirewallManagerState{Name: name}
}

// AnyManagerActive reports whether some default-deny-capable firewall
// manager (ufw, firewalld, ...) is actively enforcing right now — used by
// the analyzer to decide whether an ACCEPT-everything iptables INPUT chain
// is actually a problem, or just the substrate under a manager that's
// doing the real enforcement of its own accord.
func (f FirewallState) AnyManagerActive() bool {
	for _, m := range f.Managers {
		if m.Active {
			return true
		}
	}
	return false
}

// Listener is a socket actually observed in LISTEN state on the host.
type Listener struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Process  string `json:"process,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Raw      string `json:"raw,omitempty"`

	// Everything below is resolved from the owning PID after the socket
	// table is read (see parse.ProcessDetails). `ss` alone only ever gives
	// the short executable name — "python3", "node", "beam.smp" — which is
	// least informative exactly when it matters most: identifying a
	// listener nobody remembers configuring. All optional: a process can
	// exit between reading the socket table and looking it up, and a
	// snapshot has no live /proc to consult at all.
	Command     string `json:"command,omitempty"`      // full argv
	User        string `json:"user,omitempty"`         // who runs it
	UptimeS     int    `json:"uptime_s,omitempty"`     // seconds since start
	Unit        string `json:"unit,omitempty"`         // owning systemd unit
	ContainerID string `json:"container_id,omitempty"` // when it runs in a container
	Origin      string `json:"origin,omitempty"`       // Origin* below
}

// How a listening process came to be running — derived from its cgroup,
// which is what actually distinguishes a managed service from something
// a person started by hand in a shell. That difference is the whole point
// of the "Разное" inventory, and no amount of `ps` output reveals it.
const (
	// OriginService — started by systemd, Listener.Unit names it.
	OriginService = "service"
	// OriginManual — running inside an interactive login session's scope,
	// i.e. someone ran it by hand over SSH and it will not survive a
	// reboot or come back on its own.
	OriginManual = "manual"
	// OriginContainer — inside a container runtime's cgroup;
	// Listener.ContainerID identifies which.
	OriginContainer = "container"
)

// Public reports whether the observed socket is bound to all interfaces.
func (l Listener) Public() bool {
	return l.Address == "0.0.0.0" || l.Address == "*" || l.Address == "::" || l.Address == "[::]"
}

// NetworkInterface is one network interface on the host — physical NIC,
// bridge, VLAN, tunnel, or loopback — as `ip addr` reports it. A plain
// inventory: it does not attempt to say which interface is "the public
// one" (0.0.0.0 already covers that per-listener via Public() above) —
// just what actually exists.
type NetworkInterface struct {
	Name string `json:"name"`
	MAC  string `json:"mac,omitempty"`
	MTU  int    `json:"mtu,omitempty"`
	// Up is the administrative state (ip link set <iface> up/down) — a NIC
	// can be Up with no actual carrier (cable unplugged, peer down), which
	// LowerUp below is what actually catches.
	Up bool `json:"up"`
	// LowerUp is the operational/carrier state: is there actually a link
	// partner responding right now. Up=true, LowerUp=false is exactly the
	// "administratively enabled but the cable fell out" case a plain "up"
	// flag can't distinguish from a genuinely working interface.
	LowerUp   bool     `json:"lower_up"`
	Loopback  bool     `json:"loopback,omitempty"`
	Addresses []string `json:"addresses,omitempty"` // CIDR form, e.g. "192.168.1.5/24"
	// Traffic counters since boot, from /proc/net/dev — 0 is a legitimate
	// reading (an idle interface), not a missing one; these are only
	// actually absent (all zero) when /proc/net/dev couldn't be read at
	// all, which the source's own SourceStatus.Error already reports.
	RXBytes   int64 `json:"rx_bytes"`
	TXBytes   int64 `json:"tx_bytes"`
	RXErrors  int64 `json:"rx_errors,omitempty"`
	RXDropped int64 `json:"rx_dropped,omitempty"`
	TXErrors  int64 `json:"tx_errors,omitempty"`
	TXDropped int64 `json:"tx_dropped,omitempty"`
	// DockerNetwork/AttachedContainers answer the one thing `ip addr`
	// fundamentally cannot: what a bridge interface actually connects.
	// Unlike a point-to-point link (a VPN tunnel genuinely goes "from this
	// host to that peer"), a bridge is a virtual switch with no single
	// from/to of its own — the meaningful question for one is "what's
	// plugged into it", which only exists in the separately-parsed docker
	// network/container data, resolved after scanning by matching this
	// interface's name against each network's real (or Docker's
	// deterministically auto-generated "br-<12 hex>") bridge name.
	DockerNetwork      string `json:"docker_network,omitempty"`
	AttachedContainers int    `json:"attached_containers,omitempty"`
}

// ServiceUnit is a managed systemd service (or the docker engine).
type ServiceUnit struct {
	Name        string   `json:"name"`
	Unit        string   `json:"unit"`
	Description string   `json:"description,omitempty"`
	ActiveState string   `json:"active_state"` // active | inactive | failed | unknown
	SubState    string   `json:"sub_state,omitempty"`
	Enabled     string   `json:"enabled,omitempty"`
	MainPID     int      `json:"main_pid,omitempty"`
	MemoryBytes int64    `json:"memory_bytes,omitempty"`
	SinceText   string   `json:"since,omitempty"`
	Restarts    int      `json:"restarts,omitempty"`
	Installed   bool     `json:"installed"`
	ConfigFiles []string `json:"config_files,omitempty"`
	Actions     []string `json:"actions,omitempty"`
}

// RenewalInfo describes how a certificate gets replaced before it expires.
type RenewalInfo struct {
	Tool      string `json:"tool,omitempty"`   // certbot | acme.sh | manual
	Managed   bool   `json:"managed"`          // an automation owns this lineage
	Automatic bool   `json:"automatic"`        // a timer or cron job actually runs
	Detail    string `json:"detail,omitempty"` // what was found, in words
	// Lineage is the certbot lineage name (the directory under
	// /etc/letsencrypt/live/), set whenever Tool is "certbot" — including an
	// orphan lineage with Managed false, so the UI can explain which lineage
	// is missing its renewal file. Empty for anything certbot does not own.
	Lineage string `json:"lineage,omitempty"`
	// Derived marks a certificate found outside /etc/letsencrypt whose leaf
	// certificate bytes are identical to one already found under
	// /etc/letsencrypt/live/<Lineage> — the common haproxy pattern of a
	// deploy-hook concatenating fullchain.pem+privkey.pem into a single file
	// haproxy's "crt" wants, since haproxy (unlike nginx) has no separate
	// certificate/key directives. certbot renew still only rewrites
	// SourcePath; this copy needs a separate step to pick up the new bytes.
	Derived bool `json:"derived,omitempty"`
	// SourcePath is the /etc/letsencrypt/live/... file this content matched,
	// set only when Derived is true.
	SourcePath string `json:"source_path,omitempty"`
}

// Certificate is one TLS certificate a service presents, read from the file the
// configuration points at.
type Certificate struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	Service   string   `json:"service"`
	Endpoints []string `json:"endpoints,omitempty"` // sockets that serve it
	Sites     []string `json:"sites,omitempty"`     // server_name / frontend names

	Subject   string    `json:"subject,omitempty"`
	Issuer    string    `json:"issuer,omitempty"`
	Serial    string    `json:"serial,omitempty"`
	Names     []string  `json:"names,omitempty"` // CN plus subject alternative names
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	// DaysLeft is negative once the certificate has expired.
	DaysLeft int `json:"days_left"`

	KeyAlgorithm string `json:"key_algorithm,omitempty"`
	KeyBits      int    `json:"key_bits,omitempty"`
	SigAlgorithm string `json:"sig_algorithm,omitempty"`
	SelfSigned   bool   `json:"self_signed"`
	ChainLength  int    `json:"chain_length"`
	// Fingerprint is the SHA-256 of the leaf certificate's raw DER bytes, hex
	// encoded. It is what Serving.Match is checked against: the file on disk
	// versus what the socket actually hands back.
	Fingerprint string `json:"fingerprint,omitempty"`

	Renewal RenewalInfo `json:"renewal"`
	Serving CertServing `json:"serving"`
	Error   string      `json:"error,omitempty"`
}

// CertServing describes what a TLS endpoint actually presents on the wire,
// found by dialing it directly rather than trusting the file a configuration
// points at. A certificate renewed on disk with nobody reloading the service
// that serves it is invisible from the filesystem alone — this is the only
// check in the application that looks at the socket instead.
type CertServing struct {
	// Checked is true once a dial was attempted. False means no reachable
	// endpoint was known for this certificate, or the check was skipped
	// entirely (fixtures mode has no real sockets to dial).
	Checked  bool   `json:"checked"`
	Endpoint string `json:"endpoint,omitempty"`
	// Match is only meaningful when Checked is true and Error is empty.
	Match          bool      `json:"match"`
	ServedSerial   string    `json:"served_serial,omitempty"`
	ServedNotAfter time.Time `json:"served_not_after,omitempty"`
	Error          string    `json:"error,omitempty"`
}

// Expired reports whether the certificate is already past its validity.
func (c Certificate) Expired() bool { return c.DaysLeft < 0 }

// Valid reports whether the certificate parsed and is currently usable.
func (c Certificate) Valid() bool {
	return c.Error == "" && !c.Expired() && !time.Now().Before(c.NotBefore)
}

// CoversName reports whether the certificate is valid for a host name,
// including one level of wildcard.
func (c Certificate) CoversName(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	for _, n := range c.Names {
		n = strings.ToLower(n)
		if n == name {
			return true
		}
		if strings.HasPrefix(n, "*.") {
			if suffix := n[1:]; strings.HasSuffix(name, suffix) &&
				!strings.Contains(strings.TrimSuffix(name, suffix), ".") {
				return true
			}
		}
	}
	return false
}

// Finding is one problem the analyzer found.
type Finding struct {
	ID         string   `json:"id"`
	Rule       string   `json:"rule"`
	Severity   string   `json:"severity"`
	Title      string   `json:"title"`
	Detail     string   `json:"detail"`
	Service    string   `json:"service"`
	Object     string   `json:"object,omitempty"`
	File       string   `json:"file,omitempty"`
	Line       int      `json:"line,omitempty"`
	Suggestion string   `json:"suggestion,omitempty"`
	Refs       []string `json:"refs,omitempty"`
}

// Snapshot is the complete picture of the host at one moment.
type Snapshot struct {
	TS         string             `json:"ts"`
	Mode       string             `json:"mode"`
	Host       HostInfo           `json:"host"`
	Sources    []SourceStatus     `json:"sources"`
	Services   []ServiceUnit      `json:"services"`
	Files      []ManagedFile      `json:"files"`
	Endpoints  []Endpoint         `json:"endpoints"`
	Upstreams  []Upstream         `json:"upstreams"`
	Container  []Container        `json:"containers"`
	Networks   []DockerNetwork    `json:"networks"`
	Podman     []PodmanContainer  `json:"podman_containers,omitempty"`
	LXD        []LXDInstance      `json:"lxd_instances,omitempty"`
	VMs        []VirtualMachine   `json:"vms,omitempty"`
	Firewall   FirewallState      `json:"firewall"`
	Listeners  []Listener         `json:"listeners"`
	Interfaces []NetworkInterface `json:"interfaces"`
	Certs      []Certificate      `json:"certificates"`
	Packages   PackageUpdates     `json:"package_updates"`
	Capacity   HostCapacity       `json:"capacity"`
	Findings   []Finding          `json:"findings"`
	Digest     string             `json:"digest"`
	ScanMS     int64              `json:"scan_ms"`
}

// HostCapacity is the host's total installed memory and CPU core count —
// see parse.HostCapacity for how it's read. Used as the reference ceiling
// for the CPU/memory usage charts (see /monitor/usage's "total" field),
// not tracked over time like the metrics themselves: it very rarely
// changes on a running host, so one figure from the latest scan is enough.
type HostCapacity struct {
	MemTotalBytes int64 `json:"mem_total_bytes,omitempty"`
	CPUCores      int   `json:"cpu_cores,omitempty"`
}

// PackageUpdate is one package `apt list --upgradable` reports as having a
// newer version available.
type PackageUpdate struct {
	Name       string `json:"name"`
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
}

// PackageUpdates summarises pending OS package updates — Debian/Ubuntu (apt)
// hosts only for now, matching where this project actually runs. Checked
// once per scan (see internal/parse.Packages), not on every request: `apt
// list --upgradable` can take a couple of seconds.
type PackageUpdates struct {
	Packages []PackageUpdate `json:"packages,omitempty"`
	// RebootRequired mirrors /var/run/reboot-required — can be true from an
	// update nkt had nothing to do with (unattended-upgrades, an operator
	// running apt by hand), so it is checked independently of Packages
	// rather than only right after nkt's own upgrade flow finishes.
	RebootRequired bool `json:"reboot_required"`
}

// PackageManifest is the raw dpkg/os-release content internal/vuln needs to
// run a vulnerability scan against — Debian/Ubuntu only for now, matching
// PackageUpdates. Collected as three plain file reads (cheap, no exec) by
// internal/parse.Manifest, deliberately kept separate from PackageUpdates/
// the regular scan snapshot: it exists purely to travel to wherever the
// actual trivy scan runs (this host itself in standalone/localhost mode, or
// the hub for a real managed host — see internal/vuln's own doc comment on
// why the vulnerability DB only ever lives there, not on every host), not
// to be displayed on its own.
type PackageManifest struct {
	Available     bool   `json:"available"`
	OSRelease     string `json:"os_release,omitempty"`
	DebianVersion string `json:"debian_version,omitempty"`
	DpkgStatus    string `json:"dpkg_status,omitempty"`
}

// VulnFinding is one vulnerability trivy reported for an installed package —
// see internal/vuln.Scan.
type VulnFinding struct {
	ID               string `json:"id"` // CVE-2024-... or a vendor-specific advisory ID (e.g. TEMP-...)
	Package          string `json:"package"`
	InstalledVersion string `json:"installed_version"`
	// FixedVersion is empty when trivy reports no fix is available yet —
	// distinct from "not a security update at all" (VulnFinding isn't
	// created for those), so the UI can tell "upgrade now" apart from
	// "nothing to do yet but watch this one".
	FixedVersion string `json:"fixed_version,omitempty"`
	Severity     string `json:"severity"` // CRITICAL/HIGH/MEDIUM/LOW/UNKNOWN, trivy's own scale
	Title        string `json:"title,omitempty"`
	// New is set when this (ID, Package, Target) triple was not present in
	// the previous scan this host kept — see VulnScan.Compared for when
	// that comparison could even happen at all (never on the very first
	// scan).
	New bool `json:"new,omitempty"`
	// Target is empty for the host's own OS packages, or a container image
	// reference (e.g. "nginx:1.25") for a vulnerability found inside a
	// running Docker/Podman container's image — see internal/vuln.ScanImage.
	// Part of what makes a finding "the same" across scans (see findingKey
	// in handlers_vulnerabilities.go): the identical CVE against the same
	// package name can legitimately exist in both the host's own packages
	// and inside some container's image at once, and those are different
	// things to fix.
	Target string `json:"target,omitempty"`
}

// VulnScan is the result of one vulnerability scan, plus enough metadata
// for the UI to show how current it is — the vulnerability DB is refreshed
// on its own schedule (see internal/vuln), independent of when this host
// was last scanned against it. Available is true when there was anything
// at all to scan — either the host itself is dpkg-based (matching
// PackageManifest.Available) or at least one Docker/Podman image was
// found running — distinct from an empty Findings, which means "scanned
// and clean," not "nothing here to scan."
//
// NewCount/FixedCount/Compared describe this scan against the one
// immediately before it (a rolling one-step diff, not full history — see
// handlers_vulnerabilities.go). Compared is false only for the very first
// scan this host has ever run: there is nothing to diff against yet, which
// is a different thing from "compared, and nothing changed" (Compared
// true, both counts zero) — the UI needs to tell those apart rather than
// showing a misleading "0 new" the first time someone ever scans.
type VulnScan struct {
	Available  bool          `json:"available"`
	Findings   []VulnFinding `json:"findings,omitempty"`
	Compared   bool          `json:"compared"`
	NewCount   int           `json:"new_count,omitempty"`
	FixedCount int           `json:"fixed_count,omitempty"`
	// Warnings holds one entry per image that could not be scanned (e.g.
	// removed between the container list being read and the scan actually
	// reaching it) — noted rather than failing the whole scan over one
	// image, the same "degrade, don't abort" shape SourceStatus.Warnings
	// uses elsewhere.
	Warnings  []string  `json:"warnings,omitempty"`
	DBUpdated time.Time `json:"db_updated"`
	ScannedAt time.Time `json:"scanned_at"`
}

// HostInfo mirrors collect.HostInfo without importing that package.
type HostInfo struct {
	Mode     string   `json:"mode"`
	Hostname string   `json:"hostname"`
	Kernel   string   `json:"kernel"`
	OS       string   `json:"os"`
	Notes    []string `json:"notes,omitempty"`
}

// FindingCounts summarises findings by severity.
func (s *Snapshot) FindingCounts() map[string]int {
	out := map[string]int{}
	for _, f := range s.Findings {
		out[f.Severity]++
	}
	return out
}

// UpstreamByName looks a pool up by service and name.
func (s *Snapshot) UpstreamByName(service, name string) *Upstream {
	for i := range s.Upstreams {
		if s.Upstreams[i].Service == service && s.Upstreams[i].Name == name {
			return &s.Upstreams[i]
		}
	}
	return nil
}

// ContainerByPort finds the container publishing a host port.
func (s *Snapshot) ContainerByPort(port int) *Container {
	for i := range s.Container {
		for _, p := range s.Container[i].Ports {
			if p.HostPort == port {
				return &s.Container[i]
			}
		}
	}
	return nil
}
