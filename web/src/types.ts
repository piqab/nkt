// Mirrors the JSON the Go API emits. Kept hand-written and small: only the
// fields the UI actually reads.

export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'info'

export interface Me {
  username: string
  role: 'admin' | 'viewer'
  is_admin: boolean
  mode: string
  allow_mutations: boolean
  simulated: boolean
  /** Hub only: the hub's own build version, for comparing against each
   * host's nkt_version to tell "переустановить" and "обновить" apart. */
  hub_version?: string
}

/** A remote VPS registered with the hub (internal/store.Host). */
export interface HubHost {
  id: number
  name: string
  addr: string
  ssh_port: number
  ssh_user: string
  ssh_auth_kind: 'password' | 'key'
  arch: string
  status: 'new' | 'installing' | 'online' | 'error'
  /** What the hub recorded having installed on this host. */
  nkt_version: string
  /** What the host's own binary reports actually serving requests, read by
   * the hub's background poll. Differs from nkt_version exactly when an
   * update did not take effect — otherwise invisible, since the host keeps
   * working, just without whatever the newer version added. Absent until
   * the first successful poll. */
  running_version?: string
  admin_user?: string
  /** What the last install/update actually observed about sudo for a
   * non-root ssh_user — '' means never observed (or invalidated by an
   * "изменить" that changed the connection). See store.SudoStatus*. */
  sudo_status?: '' | 'root' | 'nopasswd' | 'password_required'
  /** Whether the hub passes NKT_TERMINAL_ENABLED=true when it (re)installs
   * this host — off by default. The host's own nkt.env is regenerated from
   * scratch on every install/update, so this has to live here (not edited
   * by hand on the host) to survive one. */
  terminal_enabled: boolean
  /** Whether the hub passes the reverse-tunnel fallback credentials
   * (NKT_HUB_TUNNEL_*) when it (re)installs this host — on by default for
   * newly added hosts (see HostForm's initialValues). With it on, the hub
   * keeps a standing connection to the host's own tunnel listener so the
   * dashboard/terminal — and reinstall/update itself — keep working even
   * if SSH to it stops responding (a blocked or misconfigured inbound
   * port 22 is the common real case, not necessarily sshd itself being
   * down) — see internal/tunnel. Same "regenerated on every install/
   * update" shape as terminal_enabled. */
  tunnel_enabled: boolean
  /** "ssh" or "tunnel" — which path the hub most recently reached this
   * host through (see internal/hub's Manager.recordChannel). Absent before
   * the first dial attempt. */
  channel?: 'ssh' | 'tunnel'
  /** Whether this host currently has a live reverse-tunnel session
   * registered, independent of channel — a healthy standby channel is
   * common long before SSH ever actually needs it. */
  tunnel_connected?: boolean
  error_msg?: string
  created_at: string
  last_seen_at?: string
  /** Findings severity counts from the hub's own background poll of this
   * host's /api/overview (see internal/hub's pollOverviews) — omitted
   * entirely (not zero) for a host that's never been polled, distinct from
   * a real "полностью здоров" reading. Only keys with a nonzero count are
   * present, same as Overview['findings']. */
  findings?: Partial<Record<Severity, number>>
  /** Whether the last poll attempt reached this host — undefined means
   * "never polled" (not `false`), which is why this is a tri-state on the
   * wire (see internal/hub/handlers.go's hostWithOverview). Stale findings
   * from an earlier successful poll are kept even while this is false. */
  reachable?: boolean
  last_polled_at?: string
}

export interface HostInfo {
  mode: string
  hostname: string
  kernel: string
  os: string
  notes?: string[]
}

export interface SourceStatus {
  name: string
  available: boolean
  version?: string
  files?: string[]
  warnings?: string[]
  error?: string
  duration_ms: number
}

export interface Finding {
  id: string
  rule: string
  severity: Severity
  title: string
  detail: string
  service: string
  object?: string
  file?: string
  line?: number
  suggestion?: string
  refs?: string[]
}

/** One CVE trivy reported for an installed package — severity is trivy's
 * own scale (CRITICAL/HIGH/MEDIUM/LOW/UNKNOWN), not the app's own
 * lowercase Severity union, since it comes from an external scanner rather
 * than nkt's own analyzer. */
export interface VulnFinding {
  id: string
  package: string
  installed_version: string
  fixed_version?: string
  severity: 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'UNKNOWN'
  title?: string
  /** trivy's own choice of the single most authoritative reference for this
   * finding (NVD, a GitHub Security Advisory, a vendor bulletin, ...) — not
   * a URL nkt constructs itself. Can be empty for some vendor-specific
   * advisory IDs. */
  url?: string
  /** Not present in the previous scan this host kept — see VulnScan.compared
   * for when this comparison could even happen (never on the first scan). */
  new?: boolean
  /** Empty for the host's own OS packages, or a container image reference
   * (e.g. "nginx:1.25") for a vulnerability found inside a running
   * Docker/Podman container's image. */
  target?: string
}

export interface VulnScan {
  available: boolean
  findings?: VulnFinding[]
  /** False only for this host's very first scan ever — nothing to diff
   * against yet, distinct from "compared, nothing changed" (true with both
   * counts at 0). */
  compared: boolean
  new_count?: number
  fixed_count?: number
  /** One entry per container image that could not be scanned (removed
   * mid-scan, unreachable) — the rest of the scan still completed. */
  warnings?: string[]
  db_updated: string
  scanned_at: string
}

export interface VulnStatus {
  scanning: boolean
  progress?: string
  scan?: VulnScan
  error?: string
}

export interface ServiceUnit {
  name: string
  unit: string
  description?: string
  active_state: string
  sub_state?: string
  enabled?: string
  main_pid?: number
  memory_bytes?: number
  since?: string
  restarts?: number
  installed: boolean
  config_files?: string[]
  actions?: string[]
}

export interface PortMapping {
  host_ip: string
  host_port: number
  container_port: number
  protocol: string
}

export interface Container {
  id: string
  name: string
  image: string
  state: string
  status: string
  project?: string
  service_name?: string
  compose_file?: string
  restart?: string
  ports?: PortMapping[]
  networks?: { name: string; ip_address?: string }[]
  depends_on?: string[]
  declared: boolean
  running: boolean
}

export interface PodmanContainer {
  id: string
  name: string
  image: string
  state: string
  status: string
  created?: number
  pod?: string
  ports?: PortMapping[]
  networks?: { name: string; ip_address?: string }[]
}

export interface LXDInstance {
  name: string
  type: string
  status: string
  architecture?: string
  ipv4?: string[]
}

export interface VMDisk {
  device: string
  source?: string
  bus?: string
}

export interface VMNetIface {
  source?: string
  mac?: string
  model?: string
}

export interface VirtualMachine {
  name: string
  uuid?: string
  state: string
  persistent: boolean
  autostart: boolean
  vcpus?: number
  memory_kb?: number
  disks?: VMDisk[]
  networks?: VMNetIface[]
}

export interface DockerNetwork {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  subnets?: string[]
  gateway?: string
  bridge?: string
}

export interface FirewallRule {
  id: string
  backend: string
  table?: string
  chain: string
  order: number
  action: string
  protocol?: string
  port_spec?: string
  ports?: number[]
  source?: string
  destination?: string
  in_iface?: string
  out_iface?: string
  dnat_to?: string
  comment?: string
  /** firewalld-only — the zone (public, trusted, dmz, ...) this rule
   * belongs to. Absent for every other backend, which has no such concept. */
  zone?: string
  /** firewalld-only — which of its two independent stores this rule was
   * read from. A port can be permanent, runtime, or (the common case, once
   * applied) both — as two separate rows, not one row with two flags,
   * since they come from two separate `firewall-cmd` listings that can
   * genuinely disagree (a change staged but not yet reloaded). */
  permanent?: boolean
  runtime?: boolean
  packets: number
  bytes: number
  raw: string
  managed_by?: string
}

export interface FirewallPolicy {
  backend: string
  table: string
  chain: string
  policy: string
  packets: number
  bytes: number
}

/** One firewall manager's own installed/active/policy summary — ufw and
 * firewalld each report through one of these (see
 * model.FirewallManagerState) so the page iterates a list instead of
 * hardcoding which managers exist. */
export interface FirewallManagerState {
  name: string // ufw | firewalld
  installed: boolean
  active: boolean
  policy?: string
}

export interface Listener {
  protocol: string
  address: string
  port: number
  process?: string
  pid?: number
  /** Resolved from the owning PID at scan time (see parse.ProcessDetails).
   * All optional — a process can exit between reading the socket table and
   * looking it up, and a fixtures snapshot has no live /proc. */
  command?: string
  user?: string
  uptime_s?: number
  unit?: string
  container_id?: string
  origin?: 'service' | 'manual' | 'container'
}

export interface NetworkInterface {
  name: string
  mac?: string
  mtu?: number
  /** Administrative state (ip link set up/down). Can be true with no
   * actual carrier — see lower_up. */
  up: boolean
  /** Operational/carrier state — a link partner is actually responding
   * right now. up=true, lower_up=false is "enabled but the cable fell
   * out", which up alone can't tell apart from a working interface. */
  lower_up: boolean
  loopback?: boolean
  addresses?: string[]
  rx_bytes: number
  tx_bytes: number
  rx_errors?: number
  rx_dropped?: number
  tx_errors?: number
  tx_dropped?: number
  /** Which docker network this bridge serves, and how many containers are
   * actually attached to it — resolved server-side by matching this
   * interface's name against docker's own network data, real not guessed.
   * Absent for anything that isn't a docker bridge. */
  docker_network?: string
  attached_containers?: number
}

/** One entry from GET /configs/browse — a directory listing under /home,
 * used to pick or create a docker-compose file's location. */
export interface DirEntry {
  path: string
  size: number
  mode: string
  mod_time: string
  is_dir: boolean
  readable: boolean
}

export interface SiteName {
  name: string
  name_unicode?: string
}

export interface ManagedFile {
  path: string
  service: string
  size: number
  mod_time: string
  sha256: string
  editable: boolean
  readable: boolean
  note?: string
  sites?: SiteName[]
}

export interface FileContent extends ManagedFile {
  content: string
}

export type BlockKind =
  | 'server'
  | 'location'
  | 'upstream'
  | 'frontend'
  | 'backend'
  | 'listen'
  | 'global'
  | 'defaults'
  | 'service'
  | 'site'

/** One structural block of a single config file — nginx server{}/location{}/
 * upstream{} or a haproxy frontend/backend/listen/global/defaults section —
 * addressed by exact source line range rather than a persistent id. */
export interface ConfigBlock {
  id: string
  kind: BlockKind
  name: string
  start_line: number
  end_line: number
  raw: string
  children?: ConfigBlock[]
  /** False for haproxy global/defaults: create/delete are refused, update is not. */
  editable: boolean
}

/** What /configs/file (PUT), /configs/blocks (POST) and a version rollback
 * all return — the real validator's verdict, and whether it had to roll the
 * write back. */
export interface WriteResult {
  path: string
  version_id: number
  validated: boolean
  validation?: { exit_code: number; stdout: string; stderr: string; simulated: boolean }
  rolled_back: boolean
  message: string
  applied: boolean
}

export interface ConfigVersion {
  id: number
  path: string
  service: string
  ts: string
  author: string
  action: string
  note: string
  size: number
  sha256: string
}

export interface Overview {
  host: HostInfo
  mode: string
  scanned: string
  scan_ms: number
  simulated: boolean
  counts: Record<string, number>
  findings: Partial<Record<Severity, number>>
  top_findings: Finding[]
  services: ServiceUnit[]
  sources: SourceStatus[]
  firewall: {
    managers: FirewallManagerState[]
    backends: string[]
    policies: FirewallPolicy[]
  }
  availability: {
    targets: number
    up: number
    down: number
    avg_uptime: number
    outages: Outage[]
    metrics_simulated: boolean
  }
  certificates: {
    total: number
    expired: number
    expiring: number
    unreadable: number
    unmanaged: number
    soonest_days: number
    soonest_name: string
  }
  package_updates: PackageUpdates
}

export interface PackageUpdate {
  name: string
  old_version: string
  new_version: string
}

/** Pending OS package updates — apt/Debian-Ubuntu hosts only for now (see
 * internal/parse.Packages). `packages` is undefined/empty both when the
 * host has none pending AND when apt isn't available at all; that
 * distinction lives in the corresponding `sources` entry ("packages"),
 * not here. */
export interface PackageUpdates {
  packages?: PackageUpdate[]
  reboot_required: boolean
}

export interface TargetStatus {
  id: number
  key: string
  label: string
  kind: string
  host: string
  port: number
  path: string
  host_header?: string
  source: string
  service: string
  node_id: string
  enabled: boolean
  first_seen: string
  last_seen: string
  last_check?: string
  last_ok?: boolean
  last_latency_ms: number
  last_error?: string
  checks_24h: number
  failures_24h: number
  uptime_24h: number
  avg_latency_24h: number
}

export interface Bucket {
  bucket: string
  total: number
  ok: number
  uptime: number
  avg_latency_ms: number
  max_latency_ms: number
}

export interface HeatCell {
  dow: number
  hour: number
  total: number
  ok: number
  uptime: number
  value: number
}

export interface Outage {
  target_id: number
  label: string
  start: string
  end: string
  checks: number
  error: string
}

export interface MetricPoint {
  bucket: string
  subject: string
  value: number
}

export interface SubjectTotal {
  subject: string
  total: number
  samples: number
}

export interface RenewalInfo {
  tool?: string
  managed: boolean
  automatic: boolean
  detail?: string
  lineage?: string
  derived?: boolean
  source_path?: string
}

/** What a TLS endpoint actually presents on the wire, checked by dialing it
 * directly — a renewed file nobody reloaded into the service is invisible
 * from the filesystem alone. */
export interface CertServing {
  checked: boolean
  endpoint?: string
  match: boolean
  served_serial?: string
  served_not_after?: string
  error?: string
}

export interface Certificate {
  id: string
  path: string
  service: string
  endpoints?: string[]
  sites?: string[]
  subject?: string
  issuer?: string
  serial?: string
  names?: string[]
  not_before: string
  not_after: string
  days_left: number
  key_algorithm?: string
  key_bits?: number
  sig_algorithm?: string
  self_signed: boolean
  chain_length: number
  fingerprint?: string
  renewal: RenewalInfo
  serving: CertServing
  error?: string
}

export interface CertificatesResponse {
  certificates: Certificate[]
  summary: {
    total: number
    expired: number
    expiring: number
    unreadable: number
    unmanaged: number
  }
}

export interface SelfSignedRequest {
  names: string[]
  service: 'nginx' | 'haproxy'
  bits?: number
  days?: number
}

export interface SelfSignedResult {
  names: string[]
  cert_path?: string
  key_path?: string
  combined_path?: string
  fingerprint: string
  not_after: string
  snippet: string
  /** ASCII/punycode name -> readable form, for names that needed converting. */
  unicode_names?: Record<string, string>
}

export interface CombineResult {
  lineage: string
  combined_path: string
  fingerprint: string
  not_after: string
  /** Non-empty only when combined_path is a brand-new file nothing references yet. */
  snippet?: string
}

export interface LineageInfo {
  name: string
  /** Readable form of name when certbot punycode-encoded an IDN domain. */
  name_unicode?: string
  /** False when fullchain.pem could not be read or parsed — days_left is meaningless then. */
  known: boolean
  not_after?: string
  days_left: number
}

export interface RenewEvent {
  time: string
  text: string
}

export interface RenewJobStatus {
  events: RenewEvent[]
  done: boolean
  error?: string
}

export interface Account {
  id: number
  username: string
  role: 'admin' | 'viewer'
  disabled: boolean
  created_at: string
  last_login_at?: string
}

export interface AuditEntry {
  id: number
  ts: string
  username: string
  action: string
  target: string
  result: string
  detail: string
}

export interface GraphNode {
  id: string
  kind: string
  label: string
  sublabel?: string
  group?: string
  status: 'ok' | 'warn' | 'error' | 'unknown'
  findings: number
  severity?: Severity
  port?: number
  public?: boolean
  meta?: Record<string, string>
}

export interface GraphEdge {
  id: string
  from: string
  to: string
  kind: string
  label?: string
  status: string
}

/** One problem/warning attached to a graph node — the node itself only
 * carries a colour and a count, this is what actually says what's wrong. */
export interface GraphFinding {
  node_id: string
  title: string
  severity: Severity
}

export interface Graph {
  nodes: GraphNode[]
  edges: GraphEdge[]
  stats: Record<string, number>
  findings: GraphFinding[]
}

export interface JobStatus {
  name: string
  last_run?: string
  last_error?: string
  last_count: number
  duration_ms: number
  interval: string
  runs: number
}

export interface Endpoint {
  id: string
  service: string
  kind: string
  address: string
  port: number
  protocol: string
  tls: boolean
  mode: string
  names?: string[]
  routes?: { match: string; target: string; target_kind: string; condition?: string }[]
  upstreams?: string[]
  file?: string
  line?: number
  label: string
  access_logs?: string[]
  extra?: Record<string, string>
}

export interface Upstream {
  id: string
  name: string
  service: string
  mode?: string
  algorithm?: string
  health?: string
  servers: {
    name?: string
    host: string
    port: number
    weight?: number
    backup: boolean
    down: boolean
    checked: boolean
  }[]
  file?: string
  line?: number
}

export interface Snapshot {
  ts: string
  mode: string
  host: HostInfo
  sources: SourceStatus[]
  services: ServiceUnit[]
  files: ManagedFile[]
  endpoints: Endpoint[]
  upstreams: Upstream[]
  containers: Container[]
  networks: DockerNetwork[]
  firewall: {
    backends: string[]
    managers: FirewallManagerState[]
    policies: FirewallPolicy[]
    rules: FirewallRule[]
  }
  listeners: Listener[]
  findings: Finding[]
  digest: string
  scan_ms: number
}
