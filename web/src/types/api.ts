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

// Docker
export interface Container {
  Id: string
  Names: string[]
  Image: string
  State: string
  Status: string
  Ports: ContainerPort[]
  Created: number
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
}

export interface DockerVolume {
  Name: string
  Driver: string
  Mountpoint: string
  CreatedAt: string
}

export interface DockerNetwork {
  Id: string
  Name: string
  Driver: string
  Scope: string
}

export interface ComposeProject {
  id: number
  name: string
  yaml_path: string
  status: string
  created_at: string
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

// Cron Jobs
export interface CronJob {
  id: number
  schedule: string
  command: string
  enabled: boolean
  raw: string
  type: 'job' | 'env' | 'comment'
}

