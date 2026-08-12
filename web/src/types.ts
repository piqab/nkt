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
  nkt_version: string
  admin_user?: string
  error_msg?: string
  created_at: string
  last_seen_at?: string
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

export interface Listener {
  protocol: string
  address: string
  port: number
  process?: string
  pid?: number
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

export interface ManagedFile {
  path: string
  service: string
  size: number
  mod_time: string
  sha256: string
  editable: boolean
  readable: boolean
  note?: string
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
    ufw_active: boolean
    ufw_policy: string
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
    ufw_active: boolean
    ufw_policy?: string
    policies: FirewallPolicy[]
    rules: FirewallRule[]
  }
  listeners: Listener[]
  findings: Finding[]
  digest: string
  scan_ms: number
}
