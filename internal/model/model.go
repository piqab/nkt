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
	ServiceNginx    = "nginx"
	ServiceHAProxy  = "haproxy"
	ServiceDocker   = "docker"
	ServicePodman   = "podman"
	ServiceIptables = "iptables"
	ServiceUFW      = "ufw"
	ServiceHost     = "host"
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
	Backend     string `json:"backend"` // iptables | ip6tables | ufw
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
	Packets     int64  `json:"packets"`
	Bytes       int64  `json:"bytes"`
	Raw         string `json:"raw"`
	ManagedBy   string `json:"managed_by,omitempty"` // docker | ufw | manual
}

// FirewallState is the whole packet-filter picture.
type FirewallState struct {
	Backends  []string         `json:"backends"`
	UFWActive bool             `json:"ufw_active"`
	UFWPolicy string           `json:"ufw_policy,omitempty"`
	Policies  []FirewallPolicy `json:"policies"`
	Rules     []FirewallRule   `json:"rules"`
}

// Listener is a socket actually observed in LISTEN state on the host.
type Listener struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Process  string `json:"process,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Raw      string `json:"raw,omitempty"`
}

// Public reports whether the observed socket is bound to all interfaces.
func (l Listener) Public() bool {
	return l.Address == "0.0.0.0" || l.Address == "*" || l.Address == "::" || l.Address == "[::]"
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
	TS        string            `json:"ts"`
	Mode      string            `json:"mode"`
	Host      HostInfo          `json:"host"`
	Sources   []SourceStatus    `json:"sources"`
	Services  []ServiceUnit     `json:"services"`
	Files     []ManagedFile     `json:"files"`
	Endpoints []Endpoint        `json:"endpoints"`
	Upstreams []Upstream        `json:"upstreams"`
	Container []Container       `json:"containers"`
	Networks  []DockerNetwork   `json:"networks"`
	Podman    []PodmanContainer `json:"podman_containers,omitempty"`
	Firewall  FirewallState     `json:"firewall"`
	Listeners []Listener        `json:"listeners"`
	Certs     []Certificate     `json:"certificates"`
	Findings  []Finding         `json:"findings"`
	Digest    string            `json:"digest"`
	ScanMS    int64             `json:"scan_ms"`
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
