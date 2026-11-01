import type { ApiWarning } from '@/lib/warnings'

// API Response wrapper
export interface ApiResponse<T> {
  success: boolean
  data?: T
  error?: {
    code: string
    message: string
  }
}

// Auth
export interface LoginRequest {
  username: string
  password: string
  totp_code?: string
}

export interface LoginResponse {
  token: string
}

// System
export interface HostInfo {
  hostname: string
  os: string
  platform: string
  platform_version: string
  kernel: string
  uptime: number
  num_cpu: number
  // "Proxmox VE 9.2.3 (Debian 13)", "Ubuntu 24.04.3 LTS"; absent from older nodes.
  platform_name?: string
  pve_version?: string
}

export interface Metrics {
  cpu: number
  mem_total: number
  mem_used: number
  mem_percent: number
  swap_total: number
  swap_used: number
  swap_percent: number
  disk_total: number
  disk_used: number
  disk_percent: number
  net_bytes_sent: number
  net_bytes_recv: number
  timestamp: number
  // ZFS ARC counted as cache, not in mem_used; absent without ZFS.
  mem_arc?: number
}

export interface SystemInfo {
  host: HostInfo
  metrics: Metrics
}

export interface MetricsPoint {
  time: number
  cpu: number
  mem_percent: number
  disk_percent: number
}

export interface UpdateInfo {
  update_available: boolean
  latest_version?: string
}

export interface UpdateCheckResult {
  current_version: string
  latest_version: string
  update_available: boolean
  release_notes: string
  published_at: string
}

export interface DashboardOverview {
  host: HostInfo
  metrics: Metrics
  version: string
  metrics_history: MetricsPoint[]
  update_info?: UpdateInfo
}

// Docker
export interface Container {
  Id: string
  Names: string[]
  Image: string
  State: string
  Status: string
  Ports: ContainerPort[]
  Created: number
  Labels: Record<string, string>
  /** Average CPU usage from the container history; null before the first sample. */
  cpu_avg_1h?: number | null
}

export interface ContainerPort {
  PrivatePort: number
  PublicPort: number
  Type: string
}

export interface DockerImage {
  Id: string
  RepoTags: string[]
  Size: number
  Created: number
  in_use: boolean
  used_by: string[]
}

export interface DockerVolume {
  Name: string
  Driver: string
  Mountpoint: string
  CreatedAt: string
  in_use: boolean
  used_by: string[]
  size_bytes: number | null
  size_measured_at: number | null  // unix millis
}

export interface DockerNetwork {
  Id: string
  Name: string
  Driver: string
  Scope: string
  in_use: boolean
  used_by: string[]
}

export interface BackupScheduleConfig {
  enabled: boolean
  interval_hours: number
  retention: number
  last_run: string | null
  last_status: string
  last_error: string
}

export interface BackupFile {
  name: string
  size: number
  mod_time: string
}

export interface PortBindingSpec {
  host_ip?: string
  host_port?: string
  container_port: string
  protocol?: string // 'tcp' | 'udp'
}

export interface CreateContainerSpec {
  name?: string
  image: string
  command?: string[]
  env?: string[] // ['KEY=VALUE']
  ports?: PortBindingSpec[]
  volumes?: string[] // ['/host:/container[:ro]']
  restart_policy?: string // 'no' | 'always' | 'unless-stopped' | 'on-failure'
  network?: string
  auto_start?: boolean
  // Lifts the compose analyser's risky tier for the binds above, after the
  // operator has been shown what the container asks for. Never lifts the
  // forbidden tier (the panel's own secrets). See lib/composeRisk.ts.
  acknowledge_risks?: boolean
}

export interface ComposeProject {
  name: string
  compose_file: string
  has_env: boolean
  path: string
}

// Network
export interface NetworkInterfaceInfo {
  name: string
  type: string
  state: string
  mac_address: string
  mtu: number
  speed: number
  addresses: NetworkAddress[]
  is_default: boolean
  driver: string
  tx_bytes: number
  rx_bytes: number
  tx_packets: number
  rx_packets: number
  tx_errors: number
  rx_errors: number
  bond_info?: BondInfo
  // The kernel's link kind (bridge, bond, vlan, veth, tun…; '' for a NIC)
  // and the bridge or bond it is a port of. Absent from older nodes.
  kind?: string
  master?: string
}

export interface NetworkAddress {
  address: string
  prefix: number
  family: string
}

export interface BondInfo {
  mode: string
  slaves: string[]
  primary: string
}

export interface InterfaceConfig {
  dhcp4: boolean
  dhcp6: boolean
  addresses: string[]
  // Optional on the way out: omitting a gateway leaves the saved one alone,
  // an empty string clears it. Always present on the way in.
  gateway4?: string
  gateway6?: string
  dns: string[]
  mtu?: number
}

export interface InterfaceDetail extends NetworkInterfaceInfo {
  config: InterfaceConfig | null
}

export interface DNSConfig {
  servers: string[]
  search: string[]
}

export interface NetworkRoute {
  destination: string
  gateway: string
  interface: string
  metric: number
  protocol: string
  scope: string
}

// Network Status (combined endpoint)
export interface NetworkStatus {
  interfaces: NetworkInterfaceInfo[]
  routes: NetworkRoute[]
  dns: DNSConfig
  bonds: NetworkInterfaceInfo[]
}

// Cron Jobs
export interface CronJob {
  id: number
  schedule: string
  command: string
  enabled: boolean
  raw: string
  type: 'job' | 'env' | 'comment'
}

// Disk Management
export interface BlockDevice {
  name: string
  size: number
  type: string
  fstype: string
  // gpt, dos; '' when the disk has no partition table. Absent from older nodes.
  pttype?: string
  mountpoint: string
  model: string
  serial: string
  rotational: boolean
  readonly: boolean
  transport: string
  state: string
  vendor: string
  children?: BlockDevice[]
  // The partition's type: a GPT type GUID, or 0x83-style for an MBR table,
  // and lsblk's name for it ("EFI System"). Absent from older nodes.
  parttype?: string
  parttypename?: string
  uuid?: string
  // A partition that holds a boot loader; the server refuses to delete or
  // format it.
  boot?: boolean
  // Proxmox VE: a guest's volume among the disks (zvol, RBD, NBD), naming its
  // guest ("VMID 100") or what it is ("zvol rpool/swap").
  owner?: string
}

export interface SmartInfo {
  device_path: string
  model_name: string
  serial_number: string
  firmware_version: string
  healthy: boolean | null
  smart_supported: boolean
  temperature: number
  power_on_hours: number
  attributes: SmartAttr[]
  self_tests: SmartSelfTest[]
  /** A self-test is running on the drive; another cannot start until it ends. */
  self_test_running?: boolean
  /** Its remaining percent, where the drive reports one (not SCSI). */
  self_test_remaining?: number
}

export interface SmartSelfTest {
  type: string
  status: string
  passed: boolean
  lifetime_hours: number
}

export interface SmartAttr {
  id: number
  name: string
  value: number
  worst: number
  threshold: number
  raw_value: string
  status?: string
}

export interface Filesystem {
  source: string
  fstype: string
  size: number
  used: number
  available: number
  use_percent: number
  mount_point: string
  // A network mount whose server did not answer within the listing's bound.
  // Listed rather than dropped, with no numbers.
  unresponsive?: boolean
}

export interface ExpandStep {
  command: string
  description: string
  device: string
}

export interface ExpandCandidate {
  source: string
  fstype: string
  mount_point: string
  current_size: number
  free_space: number
  is_lvm: boolean
  steps: ExpandStep[]
}

export interface PhysicalVolume {
  name: string
  vg_name: string
  size: string
  free: string
  attr: string
}

export interface VolumeGroup {
  name: string
  size: string
  free: string
  pv_count: number
  lv_count: number
  attr: string
}

export interface LogicalVolume {
  name: string
  vg_name: string
  size: string
  attr: string
  path: string
  pool_lv: string
  data_percent: string
  // A thin pool's metadata fill; absent from older nodes.
  metadata_percent?: string
  // Proxmox VE: the guest ("VMID 100") or storage ("storage local-lvm") that
  // owns the volume; the server refuses to remove one, or to resize one
  // that is not a storage's thin pool.
  owner?: string
}

// An LVM list with the warnings LVM printed beside its report (an old PV
// header, a missing PV).
export interface LVMReport<T> {
  items: T[]
  warnings: string[]
}

export interface RAIDArray {
  name: string
  level: string
  state: string
  size: number
  devices: RAIDDisk[]
  active: number
  total: number
  failed: number
  spare: number
  uuid?: string
}

export interface RAIDDisk {
  device: string
  state: string
  role?: string
}

export interface SwapEntry {
  name: string
  type: string
  size: number
  used: number
  priority: number
}

export interface SwapInfo {
  total: number
  used: number
  free: number
  swappiness: number
  entries: SwapEntry[]
}

export interface IOStat {
  device: string
  read_ops: number
  write_ops: number
  read_bytes: number
  write_bytes: number
  io_time: number
}

export interface DiskUsageEntry {
  path: string
  size: number
  children?: DiskUsageEntry[]
}

// Firewall (UFW)
export interface UFWStatus {
  active: boolean
  default_incoming: string
  default_outgoing: string
}

export interface UFWRule {
  number: number
  to: string
  action: string
  from: string
  comment: string
  v6: boolean
}

export interface AddRuleRequest {
  action: string
  port: string
  protocol: string
  from: string
  to: string
  comment: string
}

export interface ListeningPort {
  protocol: string
  address: string
  port: number
  pid: number
  process: string
}

// Fail2ban
export interface Fail2banStatus {
  installed: boolean
  running: boolean
  version: string
}

export interface Fail2banJail {
  name: string
  enabled: boolean
  filter: string
  banned_count: number
  total_banned: number
  banned_ips: string[]
  max_retry: number
  ban_time: string
  find_time: string
  ignoreip: string
}

// WireGuard VPN
export interface WireGuardStatus {
  installed: boolean
  version: string
}

export interface WireGuardInterface {
  name: string
  active: boolean
  public_key: string
  listen_port: number
  address: string
  dns: string
  peers: WireGuardPeer[]
}

export interface WireGuardPeer {
  public_key: string
  endpoint: string
  allowed_ips: string[]
  latest_handshake: number
  transfer_rx: number
  transfer_tx: number
}

// Tailscale VPN
export interface TailscaleStatus {
  installed: boolean
  daemon_running: boolean
  version: string
  backend_state: string
  self: TailscaleSelf | null
  tailnet_name: string
  magic_dns_suffix: string
  auth_url: string
  accept_routes: boolean
  advertise_exit_node: boolean
  current_exit_node: string
}

export interface TailscaleSelf {
  hostname: string
  tailscale_ip: string
  tailscale_ipv6: string
  online: boolean
  os: string
  exit_node_option: boolean
}

export interface TailscalePeer {
  hostname: string
  dns_name: string
  tailscale_ip: string
  os: string
  online: boolean
  last_seen: string
  exit_node: boolean
  exit_node_option: boolean
  rx_bytes: number
  tx_bytes: number
}

// File Manager
// What a file is, so a click can be routed to the right viewer instead of
// assuming everything that is not a directory is editable text.
export type FileKind = 'dir' | 'text' | 'image' | 'binary'

export interface TrashEntry {
  id: string
  /** Where it came from — the only thing that makes a restore meaningful. */
  originalPath: string
  name: string
  deletedAt: string
  size: number
  isDir: boolean
}

export interface FileOwner {
  uid: number
  gid: number
  /** Empty when the id has no passwd entry — the container case worth showing. */
  user?: string
  group?: string
}

export interface FileEntry {
  name: string
  path: string
  isDir: boolean
  size: number
  modTime: string
  mode: string
  // Derived from the name in listings, so treat it as a hint; the read
  // endpoint sniffs the bytes and is the authority.
  kind: FileKind
  owner?: FileOwner
  /** Present when this entry is a symbolic link. */
  linkTarget?: string
  /** Whether the link resolves to a directory, so a click can navigate. */
  targetIsDir?: boolean
}

// Packages
export interface PackageUpdate {
  name: string
  current_version: string
  new_version: string
  arch: string
  description?: string
}

export interface PackageSearchResult {
  name: string
  version: string
  description: string
  installed: boolean
}

// Docker - Container Stats Batch
export interface ContainerStatsResult {
  id: string
  cpu_percent: number
  mem_percent: number
  mem_usage: number
  mem_limit: number
}

// Docker - Container Inspect Detail
export interface ContainerInspectPort {
  container_port: string
  protocol: string
  host_ip: string
  host_port: string
}

export interface ContainerInspectMount {
  type: string
  source: string
  destination: string
  mode: string
  rw: string
}

export interface ContainerInspectNetwork {
  name: string
  ip_address: string
  gateway: string
  mac_address: string
}

export interface ContainerInspectDetail {
  id: string
  name: string
  image: string
  state: string
  started_at: string
  finished_at: string
  restart_count: number
  platform: string
  cmd: string
  entrypoint: string
  working_dir: string
  hostname: string
  ports: ContainerInspectPort[]
  env: string[]
  mounts: ContainerInspectMount[]
  networks: ContainerInspectNetwork[]
}

// Docker - Compose with Status
export interface ComposeProjectWithStatus extends ComposeProject {
  service_count: number
  running_count: number
  real_status: string
}

// Docker - Compose Service
export interface ComposeService {
  name: string
  container_id: string
  image: string
  state: string
  status: string
  ports: string
  has_healthcheck?: boolean
}

export interface HealthcheckTestResult {
  exit_code: number
  stdout: string
  stderr: string
  duration_ms: number
}

// Docker - Image Update Status
export interface ImageUpdateStatus {
  image: string
  tag?: string
  current_id: string
  current_digest?: string
  remote_digest?: string
  current_created?: string
  has_update: boolean
  error?: string
}

// Docker - Stack Update Check
export interface StackUpdateCheck {
  project: string
  images: ImageUpdateStatus[]
  has_updates: boolean
}

// Docker - Rollback Info
export interface RollbackDetail {
  service: string
  prev_image: string
  prev_image_id: string
  curr_image_id?: string
}

export interface RollbackInfo {
  has_rollback: boolean
  details?: RollbackDetail[]
}

// Systemd Services
export type ServiceAction = 'stop' | 'restart' | 'disable'

export interface ServiceInfo {
  name: string
  description: string
  load_state: string
  active_state: string
  sub_state: string
  enabled: string
  // How this host treats the action: 'refuse' is never done through the panel,
  // 'confirm' needs the unit's name typed. Absent for an ordinary unit.
  protection?: Partial<Record<ServiceAction, 'refuse' | 'confirm'>>
}

export interface ServiceDeps {
  requires?: string[]
  required_by?: string[]
  wanted_by?: string[]
}

// Audit Log
export interface AuditLogEntry {
  id: number
  username: string
  method: string
  path: string
  status: number
  ip: string
  node_id: string
  // protected rows (e.g. audit-clear tombstones) are immune to wipes
  protected: boolean
  created_at: string
}

export interface AuditLogsResponse {
  logs: AuditLogEntry[]
  total: number
}


// Docker - Network Inspect Detail
export interface NetworkInspectDetail {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  subnet: string
  gateway: string
  containers: NetworkContainer[]
  created: string
}

export interface NetworkContainer {
  id: string
  name: string
  ipv4_address: string
  ipv6_address: string
  mac_address: string
}

// Docker - Compose Validation Result
export interface ComposeValidationResult {
  valid: boolean
  message: string
}

// Docker - Hub Search Result
export interface DockerHubSearchResult {
  name: string
  description: string
  star_count: number
  is_official: boolean
}

// Docker - Prune Report
export interface PruneReport {
  deleted: number
  space_reclaimed?: number
}

export interface PruneAllReport {
  containers: PruneReport
  images: PruneReport
  volumes: PruneReport
  networks: PruneReport
}

// Docker Status (packages page)
export interface DockerStatus {
  installed: boolean
  version: string
  running: boolean
  // `docker compose`, the plugin the panel's stacks run on.
  compose_available: boolean
  // Only the old docker-compose binary is there; absent from older nodes.
  compose_v1_only?: boolean
  // A Proxmox VE host, where the page offers no Docker install.
  proxmox_host?: boolean
  // Where Docker is installed: the FORWARD policy ("DROP" once dockerd has
  // turned forwarding on itself) and whether bridged frames pass through
  // iptables (null: br_netfilter is not loaded, so they do not).
  forward_policy?: string
  bridge_nf_call_iptables?: boolean | null
}

// Package transaction (packages page): an upgrade, or an install from the
// page, which apt runs in a transient unit of its own.
export interface AptTransaction {
  id: string
  // '' for one a restarted panel found running without its description.
  kind: 'upgrade' | 'install' | ''
  packages: string[]
  command: string
  started_at: string
  running: boolean
  result?: 'success' | 'failed' | 'interrupted'
  exit_code?: number
  // Worded by code like an error (lib/warnings).
  warnings?: { code: string; message: string; details?: Record<string, string> }[]
}

// Process
export interface ProcessInfo {
  pid: number
  ppid: number
  name: string
  cpu: number
  memory: number
  rss: number
  nice: number
  status: string
  user: string
  command: string
  // A Proxmox VM's own QEMU process, and a process the node cannot lose:
  // the server sends them nothing but CONT.
  vmid?: number
  protected?: boolean
}

// System Tuning
export interface TuningParam {
  key: string
  current: string
  recommended: string
  description: string
  applied: boolean
}

export interface TuningCategory {
  name: string
  benefit: string
  caution: string
  params: TuningParam[]
  applied: number
  total: number
}

export interface TuningSystemInfo {
  cpu_cores: number
  total_ram: number
  kernel: string
}

// A key one of the host's own sysctl files sets: shown with the file, never
// tuned over.
export interface TuningOperatorParam {
  key: string
  current: string
  value: string
  file: string
  description: string
}

// On Proxmox VE, something its admin guide warns of that sysctl does not cover.
export interface TuningAdvisory {
  id: 'swap_on_zvol' | 'zfs_arc_unlimited'
  detail?: string
}

export interface TuningStatus {
  // On Proxmox VE: keys Proxmox keeps for itself, shown and never tuned.
  managed?: TuningParam[]
  operator?: TuningOperatorParam[]
  advisories?: TuningAdvisory[]
  categories: TuningCategory[]
  total_params: number
  applied: number
  pending_rollback: boolean
  rollback_remaining: number
  system_info: TuningSystemInfo
}

// App Store
export interface AppStoreCategory {
  id: string
  name: Record<string, string>
  icon: string
}

export interface AppStoreEnvDef {
  key: string
  label: Record<string, string>
  type: 'text' | 'password' | 'port' | 'select' | 'number'
  default?: string
  required?: boolean
  generate?: boolean
  options?: string[]
}

export interface AppStoreFeature {
  title: Record<string, string>
  description: Record<string, string>
  icon?: string
}

export interface AppStoreMeta {
  id: string
  name: string
  description: Record<string, string>
  category: string
  version: string
  website: string
  source: string
  ports: number[]
  /** How the app's own web UI answers: https only when it serves TLS itself. */
  scheme?: 'http' | 'https'
  env: AppStoreEnvDef[]
  features?: AppStoreFeature[]
  icon?: string
  featured?: boolean
  screenshots?: string[]
}

export interface AppStoreApp extends AppStoreMeta {
  installed: boolean
}

export interface PortStatus {
  port: number
  in_use: boolean
  suggested?: number
}

export interface AppStoreAppDetail {
  app: AppStoreMeta
  compose: string
  readme: string
  readme_base_url?: string
  installed: boolean
  installed_at?: string
  port_status?: PortStatus[]
}

export interface AppStoreInstalledApp {
  id: string
  version: string
  installed_at: string
  name: string
  description?: string
  icon?: string
}

export interface AppStoreStatus {
  stale: boolean
  cached_at: string
  apps: number
}

// Cluster
export interface ClusterNode {
  id: string
  name: string
  api_address: string
  grpc_address: string
  // Absent from an older leader, whose nodes are all managers. Read it through
  // lib/clusterNodes, never by testing role: an agent's role is empty.
  kind?: 'manager' | 'agent'
  role: 'voter' | 'nonvoter' | ''
  status: 'online' | 'suspect' | 'offline' | 'joining'
  labels?: Record<string, string>
  joined_at: string
  last_seen: string
}

export interface ClusterNodeMetrics {
  node_id: string
  cpu_percent: number
  memory_percent: number
  disk_percent: number
  container_count: number
  uptime_seconds: number
  version: string
  timestamp: number
}

export interface ClusterOverview {
  name: string
  node_count: number
  leader_id: string
  nodes: ClusterNode[]
  metrics?: ClusterNodeMetrics[]
  // The leader's cached latest release. Absent from an older leader.
  latest_version?: string
}

// One event of the POST /cluster/update stream: the run (overall), a wave of
// agents (wave, waves, nodes), or one node's step. An older leader sends no
// waves or target and reports a node done as step "complete".
export interface ClusterUpdateEvent {
  overall?: 'started' | 'complete' | 'error'; mode?: string; total_nodes?: number; waves?: number; target?: string
  wave?: number; nodes?: string[]
  node_id?: string; node_name?: string; step?: string; message?: string
  updated?: number; failed?: number; leader_restarting?: boolean
}

// POST /cluster/leave and /cluster/disband: the message stays for the CLI and
// older pages; warnings says the same by code.
export interface ClusterExitResponse {
  message: string
  warnings?: ApiWarning[]
}

export interface ClusterStatus {
  enabled: boolean
  // Only with enabled=false: this node's config says it is a member, but its
  // cluster service did not start at boot. Init and join refuse here.
  configured?: boolean
  name?: string
  node_count?: number
  leader_id?: string
  local_id?: string
  is_leader?: boolean
  // True when the response served local-FSM data without leader confirmation.
  // The backend sets this when (a) a follower's proxy-to-leader failed and
  // we fell back to local, or (b) the local leader's VerifyLeader timed out
  // (indicating a partition / lost quorum). UI surfaces a "stale data"
  // badge so operators know not to fully trust the numbers.
  stale?: boolean
}

// POST /cluster/nodes/:id/convert and …/promote answer 202 once the change
// has started; the node list says when it is done.
export interface ClusterConvertResponse {
  message: string
  node_id: string
  // CLUSTER_MANAGER_BACKUPS when the node keeps backups taken as a manager.
  warnings?: ApiWarning[]
}

export interface ClusterPromoteResponse {
  message: string
  node_id: string
}

export interface ClusterNodesResponse {
  nodes: ClusterNode[]
  local_id: string
  is_leader: boolean
  // Served from the local FSM without the leader's confirmation (see ClusterStatus.stale).
  stale?: boolean
}

// GET /cluster/rotation: where the cluster's four secret rotations stand,
// node by node. Secrets go by generation and CAs by pin (sha256:<hex>); no
// secret is ever in it. Only the leader answers.
export interface ClusterRotationStatus {
  jwt: {
    generation: number
    rotated_at?: string
    managers: RotationManager[]
  }
  proxy: {
    generation: number
    phase?: 'next' | 'switched' | 'done' | string
    switched_at?: string
    // next only: the managers the switch waits for, with why.
    waiting?: string[]
    managers: RotationManager[]
  }
  agent_keys: {
    generation: number
    started_at?: string
    pending: number
    agents: RotationAgent[]
  }
  ca: {
    current_pin: string
    raft_tls: boolean
    // Absent on a cluster that never rotated its CA.
    rotation?: CARotationView
  }
}

export interface RotationManager {
  id: string
  name: string
  online: boolean
  // The generation it runs (JWT) or the newest it accepts (proxy); absent when
  // its heartbeat has not said.
  generation?: number
  can_rotate: boolean
}

export interface RotationAgent {
  id: string
  name: string
  online: boolean
  pending: boolean
  issued: boolean
  can_rotate: boolean
}

export type CAPhase = 'trust' | 'reissue' | 'promote' | 'done' | 'rollback' | 'drop' | 'aborted'

// The CA rotation record as POST /cluster/rotate/ca and …/abort answer it.
export interface CARotation {
  id: string
  seq: number
  phase: CAPhase | string
  old_pin?: string
  next_pin?: string
  started_at?: string
  phase_at?: string
  phase_timeout_seconds?: number
  // Going back only: the node that ran out the phase, or "aborted by the operator".
  reason?: string
}

export interface CARotationView extends CARotation {
  // trust and reissue: when the phase goes back unless it completes.
  deadline?: string
  waiting?: string[]
  nodes: CANodeView[]
}

export interface CANodeView {
  id: string
  name: string
  kind: string
  online: boolean
  trusts: string[]
  cert_ca: string
  can_rotate: boolean
}

export interface ClusterTokenResponse {
  token: string
  // The kind of node the token admits. Absent from an older leader, which
  // makes a manager token whatever kind it is asked for.
  kind?: 'manager' | 'agent'
  expires_at: string
  grpc_port?: number
  advertise_address?: string
  /** sha256 of the cluster CA public key; the join command's --ca */
  ca_pin?: string
}

export interface ClusterTokenInfo {
  id: string
  masked: string
  expires_at: string
  created_by: string
  // Absent from an older leader, whose tokens are all manager tokens.
  kind?: 'manager' | 'agent'
}

export interface TerminalSession {
  session_id: string
  last_use: string
  attached: boolean
  reader_count: number
}

export interface TerminalInfo {
  shell_user: string
  hostname: string
  home: string
  shell: string
  is_root: boolean
}

export interface TLSStatus {
  enabled: boolean
  managed: boolean
  ca_not_after?: string
  ca_fingerprint?: string
  ca_subject?: string
  not_after?: string
  dns_names?: string[]
  ip_addresses?: string[]
  days_until_renew?: number
}

export interface ClusterEvent {
  id: number
  type: string
  node_id: string
  node_name?: string
  detail?: string
  timestamp: string
}

export interface ClusterEventsResponse {
  events: ClusterEvent[]
}

export interface ClusterInterfacesResponse {
  interfaces: { name: string; address: string }[]
}

export interface ClusterInitResponse {
  message: string
  name: string
  node_id: string
  restart: boolean
}

// ---- Group tasks (/cluster/tasks, /cluster/task-runs; internal/feature/tasks) ----
// Field names as the server sends them. Durations in a task's model are Go
// time.Duration: integer nanoseconds (10m = 600_000_000_000).

// Where a task came from. Built-ins are in every manager's binary; a Git task
// carries the repository, branch, file path and the commit it was taken at.
export interface TaskSource {
  kind: 'builtin' | 'user' | 'git'
  repo?: string
  branch?: string
  path?: string
  sha?: string
}

// A value asked for when the task runs, referenced in steps as {{ name }}.
export interface TaskInput {
  name: string
  label?: string
  default?: string
  required?: boolean
  // Goes into commands as typed, not shell-quoted.
  raw?: boolean
}

// The union of the nodes a run takes: a group label, a kind, or node IDs.
export interface TaskTargets {
  groups?: string[]
  kinds?: ('manager' | 'agent')[]
  nodes?: string[]
}

export type TaskStepKind = 'packages' | 'service' | 'command' | 'file' | 'stack' | 'reboot'

// upgrade: all in the document is upgrade_all: true here.
export interface TaskPackagesStep {
  update?: boolean
  upgrade_all?: boolean
  upgrade?: string[]
  install?: string[]
  remove?: string[]
}

export type TaskServiceAction = 'start' | 'stop' | 'restart' | 'enable' | 'disable'

export interface TaskServiceStep {
  name: string
  action: TaskServiceAction
}

// Exactly one of run and script. timeout in nanoseconds; 0 reads as unset
// (the default, 10m) to the server's format.
export interface TaskCommandStep {
  run?: string
  script?: string
  timeout: number
}

export interface TaskFileStep {
  path: string
  content: string
  mode?: string
  owner?: string
}

export interface TaskStackStep {
  name: string
  compose: string
  up?: boolean
}

// wait in nanoseconds; only_if_required is always sent, a boolean cannot be
// left unset.
export interface TaskRebootStep {
  only_if_required: boolean
  wait: number
}

interface TaskStepCommon {
  name?: string
  continue_on_error?: boolean
}

// A step: its kind names the one settings object it carries.
export type TaskStep = TaskStepCommon & (
  | { kind: 'packages'; packages: TaskPackagesStep }
  | { kind: 'service'; service: TaskServiceStep }
  | { kind: 'command'; command: TaskCommandStep }
  | { kind: 'file'; file: TaskFileStep }
  | { kind: 'stack'; stack: TaskStackStep }
  | { kind: 'reboot'; reboot: TaskRebootStep }
)

// A parsed task (tasks.Task): what validate returns and format takes.
export interface TaskModel {
  name: string
  description?: string
  inputs?: TaskInput[]
  targets: TaskTargets
  batch: number
  stop_on_failure: boolean
  steps: TaskStep[]
}

// A task as GET /cluster/tasks lists it; GET /cluster/tasks/:id adds the
// document and, when it parses, its model.
export interface TaskView {
  id: string
  name: string
  description?: string
  source: TaskSource
  read_only: boolean
  updated_at?: string
  updated_by?: string
  yaml?: string
  task?: TaskModel
}

// Line counts from 1; 0 is the document as a whole.
export interface TaskProblem {
  line: number
  message: string
}

// POST /cluster/tasks/validate: problems is always a list; task is null
// unless the document is valid.
export interface TaskValidation {
  valid: boolean
  problems: TaskProblem[]
  task: TaskModel | null
}

// The body of a preview and of a start. targets, when given, replaces the
// task's own whole. confirmed and plan_id matter to a start only.
export interface TaskRunRequest {
  task_id: string
  inputs?: Record<string, string>
  targets?: TaskTargets
  batch?: number
  stop_on_failure?: boolean
  confirmed?: number[]
  plan_id?: string
}

// A node's verdict on one step (0-based). {step} alone means fine.
export interface CheckVerdict {
  step: number
  refused?: boolean
  code?: string
  reason?: string
  needs_confirm?: boolean
}

export type PlanLeftReason = 'unknown' | 'offline' | 'outdated' | 'unreachable' | 'busy'

// kind is '' for an unknown node ID. Nodes in a wave carry verdicts or a
// check_error; nodes left out carry a reason (and run_id when busy).
export interface PlanNode {
  id: string
  name: string
  kind: 'manager' | 'agent' | ''
  leader?: boolean
  reason?: PlanLeftReason
  run_id?: string
  verdicts?: CheckVerdict[]
  check_error?: string
}

// waves is null, not [], when no node remains. steps are the steps of the
// document previewed, which the verdicts' step indexes into: the task may
// have changed since the screen read it.
export interface TaskPlan {
  waves: PlanNode[][] | null
  left?: PlanNode[]
  plan_id: string
  managers_not_ready?: string[]
  steps?: RunStepName[]
}

export type RunStatus = 'running' | 'stopping' | 'handing_over' | 'succeeded' | 'failed' | 'stopped'
export type RunNodeStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'skipped' | 'unknown' | 'stopped'
export type RunStepStatus = 'pending' | 'starting' | 'running' | 'succeeded' | 'failed' | 'skipped' | 'unknown' | 'cancelled'

// changed is left out when false.
export interface RunStepRecord {
  status: RunStepStatus
  job_id?: string
  summary?: string
  changed?: boolean
  started_at?: string
  ended_at?: string
}

// A node not started yet is {status: 'pending'} with no steps.
//
// In an agent install a record is a server's row (s1, s2 … in the order
// given), not a node's, and carries the server: host as given, ip the address
// the leader resolved it to and the agent registers at, name and group as
// asked, node_id once registered. awaiting is the question it waits on the
// operator for: host_key (approve the fingerprint in host_key), check (open
// check_url, Tailscale SSH's sign-in), standalone (turn the panel on it into
// an agent) or review. reason_details fill the reason's sentence.
export interface RunNodeRecord {
  status: RunNodeStatus
  reason?: string
  reason_details?: Record<string, string>
  steps?: RunStepRecord[]
  host?: string
  ip?: string
  name?: string
  group?: string
  node_id?: string
  awaiting?: InstallAwaiting
  host_key?: string
  check_url?: string
  /** An install row's server had SFPanel before: its program or its data. */
  sfpanel_before?: boolean
}

export type InstallAwaiting = 'host_key' | 'check' | 'standalone' | 'review'

// A run's head. nodes are its node IDs in run order (the records are in
// TaskRunDetail.nodes). An agent install is a run of kind agent-install
// (task_id builtin/agent-install) whose nodes are row keys; credentials_until
// is when the leader drops its credentials, set only while a registered
// server that failed may still be rechecked or reverted.
export interface RunRecord {
  id: string
  kind?: 'agent-install'
  credentials_until?: string
  task_id: string
  task_name: string
  rev: string
  inputs?: Record<string, string>
  nodes: string[]
  waves: string[][]
  confirmed?: number[]
  batch: number
  stop_on_failure: boolean
  status: RunStatus
  reason?: string
  force?: boolean
  stopped_by?: string
  started_by: string
  started_at: string
  ended_at?: string
  rerun_of?: string
  start_at?: Record<string, number>
}

// A step of the document the run ran; a step with no name has its kind only.
export interface RunStepName {
  name?: string
  kind: TaskStepKind
}

// GET /cluster/task-runs/:id. steps is left out when the run's revision
// cannot be read.
export interface TaskRunDetail {
  run: RunRecord
  nodes: Record<string, RunNodeRecord>
  steps?: RunStepName[]
}

// One event of GET /cluster/task-runs/:id/stream: a snapshot first (again on
// every reconnect), then changed node records and heads, then end.
export type TaskRunStreamEvent =
  | { type: 'snapshot'; run: RunRecord; nodes: Record<string, RunNodeRecord> }
  | { type: 'node'; node_id: string; record: RunNodeRecord }
  | { type: 'run'; run: RunRecord }
  | { type: 'end' }

export type TaskJobStatus = 'running' | 'rebooting' | 'succeeded' | 'failed' | 'interrupted' | 'cancelled'

// One step's job on its node, with its output from ?skip=. next_skip is an
// offset into the node's raw log: send it back as given, never count it from
// output.
export interface TaskJobView {
  id: string
  run_id: string
  kind: string
  status: TaskJobStatus
  changed: boolean
  exit_code?: number
  summary?: string
  output?: string
  next_skip: number
  done: boolean
  started_at: string
  ended_at?: string
  holder?: { job_id: string; run_id: string; task: string }
}

// The GitHub folder (or one file) a task import reads. An empty branch is
// main. The token is sent to GitHub only, never stored.
export interface TaskImportSource {
  url: string
  branch: string
  path: string
  token?: string
}

// files are paths from the preview, all read at its commit.
export interface TaskImportRequest extends TaskImportSource {
  commit: string
  files: string[]
}

export type TaskImportFileState = 'new' | 'changed' | 'unchanged' | 'too_large'

export interface TaskImportFile {
  path: string
  name: string
  sha: string
  size: number
  state: TaskImportFileState
  task_id?: string
  task_name?: string
}

// POST /cluster/tasks/import/preview. removed are tasks taken from this
// folder whose file is gone at the commit; the import does not delete them.
export interface TaskImportPreview {
  repo: string
  branch: string
  path: string
  commit: string
  files: TaskImportFile[]
  removed: { task_id: string; task_name: string; path: string }[]
}

// POST /cluster/tasks/import. Each failed entry carries details.path.
export interface TaskImportResult {
  commit: string
  imported: TaskView[]
  failed: ApiWarning[]
}

// ---- Agent install over SSH (/cluster/agent-installs; internal/feature/agentinstall) ----

export type AgentInstallMethod = 'password' | 'key' | 'tailscale'
export type AgentInstallMode = 'auto' | 'online' | 'offline'

// One server of an install. A missing port is 22; a missing name is the
// server's hostname; group is merged into the node's labels once it is online.
export interface AgentInstallServer {
  host: string
  port?: number
  name?: string
  group?: string
}

// The one login every server of an install takes. Each method carries its own
// secret and no other's: password the password, key the private key (and its
// passphrase), tailscale none. sudo_same (the login password is sudo's too)
// is for a password login only, and never with sudo_password. These are sent
// in the start's body and nowhere else: the leader holds them in memory only.
export interface AgentInstallAccount {
  user: string
  method: AgentInstallMethod
  password?: string
  key?: string
  passphrase?: string
  sudo_password?: string
  sudo_same?: boolean
}

// POST /cluster/agent-installs. review stops every server after its pre-flight
// check until POST …/proceed. concurrency is 1 to 5 (3 when left out).
export interface AgentInstallRequest {
  servers: AgentInstallServer[]
  account: AgentInstallAccount
  review?: boolean
  mode?: AgentInstallMode
  concurrency?: number
}

// Docker observability — container metrics, events, alert rule kinds
export interface ContainerMetricPoint {
  ts: number // unix millis
  cpu_percent: number
  mem_percent: number
  mem_bytes: number
}

export interface ContainerEvent {
  ts: number
  event_type: 'start' | 'stop' | 'die' | 'oom' | 'kill' | 'restart' | 'healthy' | 'unhealthy'
  exit_code: number | null
  detail: string | null
}

export interface RecentContainerEvent extends ContainerEvent {
  container_id: string
  container_name: string
}

// Alert rows as ListChannels / ListRules return them: the JSON-valued columns
// arrive as JSON text, unparsed. internal/feature/alert/testdata/page_contract.json
// pins the rule row; e2e/tests/alert-settings.spec.ts renders it.
export interface AlertChannel {
  id: number
  name: string
  type: 'discord' | 'telegram' | 'webhook'
  config: string // JSON object text, secrets masked — never send it back (see ChannelsSection)
  enabled: boolean
  created_at: string
}

export interface AlertRule {
  id: number
  name: string
  type: string
  condition: string // JSON text: {operator, threshold} for cpu/memory/disk, a container pattern otherwise
  channel_ids: string // JSON number array
  severity: 'info' | 'warning' | 'critical'
  cooldown: number
  node_scope: string
  node_ids: string // JSON string array
  enabled: boolean
  // false: nothing evaluates this rule's type (#132), so it never fires
  // whatever enabled says. Absent from a node older than the field.
  evaluated?: boolean
  created_at: string
}

export interface AlertHistoryEntry {
  id: number
  rule_name: string
  type: string
  severity: string
  message: string
  node_id: string
  sent_channels: string // JSON array of channel names that received the alert
  created_at: string
}

export interface DiffSummary {
  added: number
  modified: number
  removed: number
}

export interface DiffImageChange { service: string; from: string; to: string }
export interface DiffSetChange   { service: string; added: string[]; removed: string[] }
export interface DiffScalarChange { service: string; from: string; to: string }
export interface DiffHealthcheckChange { service: string; from: string; to: string }

export interface DiffByCategory {
  image:       DiffImageChange[]
  ports:       DiffSetChange[]
  volumes:     DiffSetChange[]
  env:         DiffSetChange[]
  restart:     DiffScalarChange[]
  healthcheck: DiffHealthcheckChange[]
}

export interface DiffResult {
  summary: DiffSummary
  by_category: DiffByCategory
  raw_diff: string
}

export interface ImportRequest {
  url: string
  branch?: string
  path?: string
  token?: string
  name: string
}

// Port map — listening/bound TCP/UDP ports correlated with firewall rules,
// container port bindings, and host process ownership.
export interface PortMapFirewallInfo {
  action: string   // "ALLOW" | "DENY" | "REJECT" | "LIMIT"
  scope: string
  rule_id: number
  source: string   // "ufw"
}
export interface PortMapContainerInfo {
  id: string
  name: string
  stack: string
}
export interface PortMapProcessInfo {
  pid: number
  name: string
}
export interface PortMapRow {
  port: number
  proto: string    // "tcp" | "udp"
  state: string    // "listening" | "bound"
  firewall:   PortMapFirewallInfo  | null
  containers: PortMapContainerInfo[]
  process:    PortMapProcessInfo   | null
}

export type HealthcheckTestType = 'CMD-SHELL' | 'CMD' | 'NONE'

export interface HealthcheckSpec {
  test_type: HealthcheckTestType
  test_value: string
  interval: string
  timeout: string
  retries: number
  start_period: string
}


// --- Node-to-node stack migration ---
export interface MigratePreflightFinding {
  // Codes the dialog acts on: the warnings 'target-unreachable' (the
  // migration refuses it) and 'compose-risky' (an agent stack's host access,
  // whose lines go back as acknowledgedRisks; an older agent or manager
  // sends none). A block, 'compose-forbidden' among them, is shown by its
  // message.
  code: string
  message: string
}

export interface MigratePreflightReport {
  blocks: MigratePreflightFinding[]
  warnings: MigratePreflightFinding[]
}

export interface MigratePhaseEvent {
  phase: string
  message: string
  done: boolean
}

export type MigrateDisposition = 'retain' | 'delete' | 'clone'

// Cluster-wide stacks aggregation (Docker page "전체 클러스터" toggle)
export interface ClusterNodeStacks {
  node_id: string
  node_name: string
  local: boolean
  status: string // node health: online | suspect | offline | joining
  stacks: ComposeProjectWithStatus[]
  error?: string // stable machine code (unreachable | list_failed), mapped to i18n in the UI
}

// ---------- Network shares (SMB/NFS) ----------

export interface NetworkShare {
  type: 'cifs' | 'nfs'
  /** fstab spec: //server/share or server:/export */
  source: string
  server: string
  share: string
  mount_point: string
  options: string
  /** False for entries a human wrote into fstab; those are read-only here. */
  managed: boolean
  /** Live state from findmnt, not what fstab says should be mounted. */
  mounted: boolean
  has_credentials: boolean
  total_bytes?: number
  used_bytes?: number
  error?: string
}

export interface NetworkShareInput {
  type: string
  server: string
  share: string
  mount_point: string
  username?: string
  password?: string
  domain?: string
  options?: string
  read_only?: boolean
}

export interface NetworkShareTools {
  cifs: { installed: boolean; package: string }
  nfs: { installed: boolean; package: string }
}

// ---- AI workspace (internal/feature/ai) ----
export type AITool = 'claude' | 'codex' | 'gemini' | 'shell'
export type AISessionState = 'working' | 'waiting' | 'shell' | 'ended'

/**
 * How a session was started, beyond the tool itself: what the operator chose
 * in the dialog's 실행 옵션 section. Absent — which is what every session
 * created before the feature reports — means the tool was started bare.
 *
 * The values are the CLIs' own (`permission` is Claude's --permission-mode or
 * Codex's --ask-for-approval, `sandbox` is Codex-only), and the server keeps
 * these *choices* rather than a built argv, so 다시 실행 reproduces the launch.
 */
export interface AILaunchOptions {
  continue?: 'last' | 'pick'
  permission?: string
  sandbox?: string
  dangerous?: boolean
  model?: string
  extra?: string[]
}

export interface AISession {
  id: string
  tool: AITool
  title: string
  run_as: string
  cwd: string
  /** the tool configuration directory this session runs against; absent = the tool's own */
  profile?: string
  /** the options this session was started with; absent = the tool was started bare */
  launch?: AILaunchOptions
  state: AISessionState
  /** service = the account's tmux server is a transient systemd unit and survives a panel restart; process = setsid only */
  persistence: 'service' | 'process'
  attached: boolean
  /** live on the tmux socket but no row — the DB was lost */
  unknown?: boolean
  created_at: string
  last_attached_at?: string
  ended_at?: string
}

export interface AIToolStatus {
  installed: boolean
  version: string
  path: string
  latest: string
  update_available: boolean
  logged_in: boolean
}

export interface AITools {
  /** supported = installed and at least min_version; below it sessions are refused */
  tmux: { installed: boolean; version: string; supported: boolean; min_version: string }
  systemd_run: boolean
  accounts: string[]
  panel_account: string
  account: string
  tools: Record<'claude' | 'codex' | 'gemini', AIToolStatus>
}

/**
 * One selectable tool configuration directory — CODEX_HOME for Codex,
 * CLAUDE_CONFIG_DIR for Claude — so one CLI can hold several logins. The
 * default profile has an empty name and is the tool's own directory: the
 * panel neither created nor can delete it.
 */
export interface AIProfile {
  name: string
  default: boolean
  path: string
  /** the tool's credential file is present; a hint, not a guarantee the token still works */
  logged_in: boolean
  /** newest session created on this profile, absent when it has never been used */
  last_used_at?: string
}

export interface AIProfiles {
  tool: string
  account: string
  profiles: AIProfile[]
}

export interface AIDirs {
  recent: string[]
  stacks: string[]
  home: string
}
