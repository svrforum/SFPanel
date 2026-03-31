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
}

export interface SystemInfo {
  host: HostInfo
  metrics: Metrics
}

export interface MetricsPoint {
  time: number
  cpu: number
  mem_percent: number
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
}

export interface DockerNetwork {
  Id: string
  Name: string
  Driver: string
  Scope: string
  in_use: boolean
  used_by: string[]
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
  gateway4: string
  gateway6: string
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
  mountpoint: string
  model: string
  serial: string
  rotational: boolean
  readonly: boolean
  transport: string
  state: string
  vendor: string
  children?: BlockDevice[]
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
export interface FileEntry {
  name: string
  path: string
  isDir: boolean
  size: number
  modTime: string
  mode: string
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
}

// Docker - Image Update Status
export interface ImageUpdateStatus {
  image: string
  current_id: string
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
export interface ServiceInfo {
  name: string
  description: string
  load_state: string
  active_state: string
  sub_state: string
  enabled: string
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
  compose_available: boolean
}

// Process
export interface ProcessInfo {
  pid: number
  name: string
  cpu: number
  memory: number
  status: string
  user: string
  command: string
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

export interface TuningStatus {
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
  env: AppStoreEnvDef[]
  features?: AppStoreFeature[]
  icon?: string
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

// Cluster
export interface ClusterNode {
  id: string
  name: string
  api_address: string
  grpc_address: string
  role: 'voter' | 'nonvoter'
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
}

export interface ClusterStatus {
  enabled: boolean
  name?: string
  node_count?: number
  leader_id?: string
  local_id?: string
  is_leader?: boolean
}

export interface ClusterNodesResponse {
  nodes: ClusterNode[]
  local_id: string
  is_leader: boolean
}

export interface ClusterTokenResponse {
  token: string
  expires_at: string
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

