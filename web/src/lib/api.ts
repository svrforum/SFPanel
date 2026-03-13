import type {
  HostInfo,
  Metrics,
  Container,
  DockerImage,
  DockerVolume,
  DockerNetwork,
  FileEntry,
  CronJob,
  NetworkInterfaceInfo,
  InterfaceDetail,
  InterfaceConfig,
  NetworkRoute,
  PackageUpdate,
  PackageSearchResult,
  BlockDevice,
  SmartInfo,
  IOStat,
  DiskUsageEntry,
  Filesystem,
  ExpandCandidate,
  PhysicalVolume,
  VolumeGroup,
  LogicalVolume,
  RAIDArray,
  SwapInfo,
  ServiceInfo,
  ServiceDeps,
  DashboardOverview,
  NetworkStatus,
  UpdateCheckResult,
  AuditLogsResponse,
} from '@/types/api'

const API_BASE = '/api/v1'
const WS_AUTH_PROTOCOL_PREFIX = 'sfpanel.jwt.'

class ApiClient {
  private token: string | null = null

  constructor() {
    this.token = localStorage.getItem('token')
  }

  setToken(token: string) {
    this.token = token
    localStorage.setItem('token', token)
  }

  clearToken() {
    this.token = null
    localStorage.removeItem('token')
  }

  getToken(): string | null {
    return this.token
  }

  getWebSocketProtocols(): string[] {
    if (!this.token) return []
    return [`${WS_AUTH_PROTOCOL_PREFIX}${this.token}`]
  }

  isAuthenticated(): boolean {
    return !!this.token
  }

  private async request<T>(path: string, options: RequestInit = {}): Promise<T> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...((options.headers as Record<string, string>) || {}),
    }

    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }

    const res = await fetch(`${API_BASE}${path}`, {
      ...options,
      headers,
    })

    const json = await res.json()

    if (!json.success) {
      throw new Error(json.error?.message || 'Unknown error')
    }

    return json.data as T
  }

  // Auth
  login(username: string, password: string, totpCode?: string) {
    return this.request<{ token: string }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password, totp_code: totpCode }),
    })
  }

  getSetupStatus() {
    return this.request<{ setup_required: boolean }>('/auth/setup-status')
  }

  setupAdmin(username: string, password: string) {
    return this.request<{ token: string }>('/auth/setup', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    })
  }

  changePassword(currentPassword: string, newPassword: string) {
    return this.request('/auth/change-password', {
      method: 'POST',
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
    })
  }

  get2FAStatus() {
    return this.request<{ enabled: boolean }>('/auth/2fa/status')
  }

  setup2FA() {
    return this.request<{ secret: string; url: string }>('/auth/2fa/setup', { method: 'POST' })
  }

  verify2FA(secret: string, code: string) {
    return this.request('/auth/2fa/verify', {
      method: 'POST',
      body: JSON.stringify({ secret, code }),
    })
  }

  // Settings
  getSettings() {
    return this.request<Record<string, string>>('/settings')
  }

  updateSettings(settings: Record<string, string>) {
    return this.request('/settings', {
      method: 'PUT',
      body: JSON.stringify({ settings }),
    })
  }

  // Audit Logs
  getAuditLogs(page = 1, limit = 50) {
    return this.request<AuditLogsResponse>(`/audit/logs?page=${page}&limit=${limit}`)
  }

  clearAuditLogs() {
    return this.request('/audit/logs', { method: 'DELETE' })
  }

  // System
  getSystemInfo() {
    return this.request<{ host: HostInfo; metrics: Metrics; version?: string }>('/system/info')
  }

  getTopProcesses() {
    return this.request<Array<{ pid: number; name: string; cpu: number; memory: number; status: string }>>('/system/processes')
  }

  getMetricsHistory() {
    return this.request<Array<{ time: number; cpu: number; mem_percent: number }>>('/system/metrics-history')
  }

  getDashboardOverview() {
    return this.request<DashboardOverview>('/system/overview')
  }

  // System update
  checkUpdate() {
    return this.request<UpdateCheckResult>('/system/update-check')
  }

  async runUpdateStream(
    onProgress: (event: { step: string; message: string }) => void
  ): Promise<void> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
    }
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }

    const res = await fetch(`${API_BASE}/system/update`, {
      method: 'POST',
      headers,
    })
    if (!res.ok) throw new Error('Update failed')
    const reader = res.body?.getReader()
    if (!reader) throw new Error('No stream')
    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        if (line.startsWith('data: ')) {
          try { onProgress(JSON.parse(line.slice(6))) } catch { /* skip */ }
        }
      }
    }
  }

  // System backup/restore
  async downloadBackup(): Promise<Blob> {
    const headers: Record<string, string> = {}
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }
    const res = await fetch(`${API_BASE}/system/backup`, {
      method: 'POST',
      headers,
    })
    if (!res.ok) throw new Error(`Backup failed (${res.status})`)
    return res.blob()
  }

  async restoreBackup(file: File): Promise<void> {
    const headers: Record<string, string> = {}
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }
    const formData = new FormData()
    formData.append('backup', file)
    const res = await fetch(`${API_BASE}/system/restore`, {
      method: 'POST',
      headers,
      body: formData,
    })
    const json = await res.json()
    if (!json.success) throw new Error(json.error?.message || 'Restore failed')
  }

  listProcesses(query?: string, sort?: string) {
    const params = new URLSearchParams()
    if (query) params.set('q', query)
    if (sort) params.set('sort', sort)
    return this.request<{ processes: Array<{ pid: number; name: string; cpu: number; memory: number; status: string; user: string; command: string }>; total: number }>(
      `/system/processes/list?${params.toString()}`
    )
  }

  killProcess(pid: number, signal: string = 'TERM') {
    return this.request(`/system/processes/${pid}/kill`, {
      method: 'POST',
      body: JSON.stringify({ signal }),
    })
  }

  // Docker Containers
  getContainers() {
    return this.request<Container[]>('/docker/containers')
  }

  startContainer(id: string) {
    return this.request(`/docker/containers/${id}/start`, { method: 'POST' })
  }

  stopContainer(id: string) {
    return this.request(`/docker/containers/${id}/stop`, { method: 'POST' })
  }

  restartContainer(id: string) {
    return this.request(`/docker/containers/${id}/restart`, { method: 'POST' })
  }

  inspectContainer(id: string) {
    return this.request<{
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
      ports: Array<{ container_port: string; protocol: string; host_ip: string; host_port: string }>
      env: string[]
      mounts: Array<{ type: string; source: string; destination: string; mode: string; rw: string }>
      networks: Array<{ name: string; ip_address: string; gateway: string; mac_address: string }>
    }>(`/docker/containers/${id}/inspect`)
  }

  containerStats(id: string) {
    return this.request<{
      cpu_percent: number
      mem_usage: number
      mem_limit: number
      mem_percent: number
    }>(`/docker/containers/${id}/stats`)
  }

  containerStatsBatch() {
    return this.request<import('@/types/api').ContainerStatsResult[]>('/docker/containers/stats/batch')
  }

  removeContainer(id: string) {
    return this.request(`/docker/containers/${id}`, { method: 'DELETE' })
  }

  // Docker Images
  getImages() {
    return this.request<DockerImage[]>('/docker/images')
  }

  pullImage(image: string) {
    return this.request('/docker/images/pull', {
      method: 'POST',
      body: JSON.stringify({ image }),
    })
  }

  async pullImageStream(
    imageName: string,
    onProgress: (event: { status: string; progress?: string; id?: string }) => void
  ): Promise<void> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
    }
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }

    const res = await fetch(`${API_BASE}/docker/images/pull`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ image: imageName }),
    })
    if (!res.ok) throw new Error('Pull failed')
    const reader = res.body?.getReader()
    if (!reader) throw new Error('No stream')
    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        if (line.startsWith('data: ')) {
          try { onProgress(JSON.parse(line.slice(6))) } catch { /* skip malformed */ }
        }
      }
    }
  }

  removeImage(id: string) {
    return this.request(`/docker/images/${encodeURIComponent(id)}`, { method: 'DELETE' })
  }

  // Docker Volumes
  getVolumes() {
    return this.request<DockerVolume[]>('/docker/volumes')
  }

  createVolume(name: string) {
    return this.request('/docker/volumes', {
      method: 'POST',
      body: JSON.stringify({ name }),
    })
  }

  removeVolume(name: string) {
    return this.request(`/docker/volumes/${name}`, { method: 'DELETE' })
  }

  // Docker Networks
  getNetworks() {
    return this.request<DockerNetwork[]>('/docker/networks')
  }

  createNetwork(name: string, driver: string = 'bridge') {
    return this.request('/docker/networks', {
      method: 'POST',
      body: JSON.stringify({ name, driver }),
    })
  }

  removeNetwork(id: string) {
    return this.request(`/docker/networks/${id}`, { method: 'DELETE' })
  }

  // Docker - Container Creation
  createContainer(config: import('@/types/api').ContainerCreateConfig) {
    return this.request<{ id: string; message: string }>('/docker/containers', {
      method: 'POST',
      body: JSON.stringify(config),
    })
  }

  // Docker - Prune
  pruneContainers() {
    return this.request<import('@/types/api').PruneReport>('/docker/prune/containers', { method: 'POST' })
  }

  pruneImages() {
    return this.request<import('@/types/api').PruneReport>('/docker/prune/images', { method: 'POST' })
  }

  pruneVolumes() {
    return this.request<import('@/types/api').PruneReport>('/docker/prune/volumes', { method: 'POST' })
  }

  pruneNetworks() {
    return this.request<import('@/types/api').PruneReport>('/docker/prune/networks', { method: 'POST' })
  }

  pruneAll() {
    return this.request<import('@/types/api').PruneAllReport>('/docker/prune/all', { method: 'POST' })
  }

  // Docker - Hub Search
  searchDockerHub(query: string, limit: number = 25) {
    return this.request<import('@/types/api').DockerHubSearchResult[]>(
      `/docker/images/search?q=${encodeURIComponent(query)}&limit=${limit}`
    )
  }

  // Docker Compose
  getComposeProjects() {
    return this.request<import('@/types/api').ComposeProjectWithStatus[]>('/docker/compose')
  }

  createComposeProject(name: string, yaml: string) {
    return this.request('/docker/compose', {
      method: 'POST',
      body: JSON.stringify({ name, yaml }),
    })
  }

  getComposeProject(project: string) {
    return this.request<{ project: import('@/types/api').ComposeProject; yaml: string }>(`/docker/compose/${project}`)
  }

  updateComposeProject(project: string, yaml: string) {
    return this.request(`/docker/compose/${project}`, {
      method: 'PUT',
      body: JSON.stringify({ yaml }),
    })
  }

  deleteComposeProject(project: string) {
    return this.request(`/docker/compose/${project}`, { method: 'DELETE' })
  }

  composeUp(project: string) {
    return this.request(`/docker/compose/${project}/up`, { method: 'POST' })
  }

  composeDown(project: string) {
    return this.request(`/docker/compose/${project}/down`, { method: 'POST' })
  }

  getComposeServices(project: string) {
    return this.request<import('@/types/api').ComposeService[]>(`/docker/compose/${project}/services`)
  }

  restartComposeService(project: string, service: string) {
    return this.request(`/docker/compose/${project}/services/${service}/restart`, { method: 'POST' })
  }

  stopComposeService(project: string, service: string) {
    return this.request(`/docker/compose/${project}/services/${service}/stop`, { method: 'POST' })
  }

  startComposeService(project: string, service: string) {
    return this.request(`/docker/compose/${project}/services/${service}/start`, { method: 'POST' })
  }

  getComposeServiceLogs(project: string, service: string, tail: number = 100) {
    return this.request<{ logs: string }>(`/docker/compose/${project}/services/${service}/logs?tail=${tail}`)
  }

  getComposeEnv(project: string) {
    return this.request<{ content: string }>(`/docker/compose/${project}/env`)
  }

  updateComposeEnv(project: string, content: string) {
    return this.request(`/docker/compose/${project}/env`, {
      method: 'PUT',
      body: JSON.stringify({ content }),
    })
  }

  // File Manager
  listFiles(path: string) {
    return this.request<FileEntry[]>(`/files?path=${encodeURIComponent(path)}`)
  }

  readFile(path: string) {
    return this.request<{ content: string; size: number }>(`/files/read?path=${encodeURIComponent(path)}`)
  }

  writeFile(path: string, content: string) {
    return this.request('/files/write', {
      method: 'POST',
      body: JSON.stringify({ path, content }),
    })
  }

  createDir(path: string) {
    return this.request('/files/mkdir', {
      method: 'POST',
      body: JSON.stringify({ path }),
    })
  }

  deletePath(path: string) {
    return this.request(`/files?path=${encodeURIComponent(path)}`, { method: 'DELETE' })
  }

  renamePath(oldPath: string, newPath: string) {
    return this.request('/files/rename', {
      method: 'POST',
      body: JSON.stringify({ old_path: oldPath, new_path: newPath }),
    })
  }

  uploadFile(destPath: string, file: File, onProgress?: (percent: number) => void): Promise<void> {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest()
      xhr.open('POST', `${API_BASE}/files/upload`)

      if (this.token) {
        xhr.setRequestHeader('Authorization', `Bearer ${this.token}`)
      }

      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable && onProgress) {
          onProgress(Math.round((e.loaded / e.total) * 100))
        }
      }

      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve()
        } else {
          try {
            const json = JSON.parse(xhr.responseText)
            reject(new Error(json.error?.message || 'Upload failed'))
          } catch {
            reject(new Error(`Upload failed (${xhr.status})`))
          }
        }
      }

      xhr.onerror = () => reject(new Error('Network error'))

      const formData = new FormData()
      formData.append('path', destPath)
      formData.append('file', file)
      xhr.send(formData)
    })
  }

  async downloadFile(path: string): Promise<Blob> {
    const headers: Record<string, string> = {}
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }

    const res = await fetch(`${API_BASE}/files/download?path=${encodeURIComponent(path)}`, { headers })

    if (!res.ok) {
      throw new Error(`Download failed (${res.status})`)
    }

    return res.blob()
  }

  // Logs
  getLogSources() {
    return this.request<Array<{ id: string; name: string; path: string; size: number; exists: boolean; custom: boolean; custom_id?: number }>>('/logs/sources')
  }

  readLog(source: string, lines: number = 100) {
    return this.request<{ source: string; lines: string[]; total_lines: number }>(
      `/logs/read?source=${encodeURIComponent(source)}&lines=${lines}`,
    )
  }

  addCustomLogSource(name: string, path: string) {
    return this.request<{ id: number; source: { id: string; name: string; path: string; size: number; exists: boolean; custom: boolean; custom_id: number } }>('/logs/custom-sources', {
      method: 'POST',
      body: JSON.stringify({ name, path }),
    })
  }

  deleteCustomLogSource(id: number) {
    return this.request<{ message: string }>(`/logs/custom-sources/${id}`, { method: 'DELETE' })
  }

  // Cron Jobs
  getCronJobs() {
    return this.request<CronJob[]>('/cron')
  }

  createCronJob(schedule: string, command: string) {
    return this.request('/cron', {
      method: 'POST',
      body: JSON.stringify({ schedule, command }),
    })
  }

  updateCronJob(id: number, schedule: string, command: string, enabled: boolean) {
    return this.request(`/cron/${id}`, {
      method: 'PUT',
      body: JSON.stringify({ schedule, command, enabled }),
    })
  }

  deleteCronJob(id: number) {
    return this.request(`/cron/${id}`, { method: 'DELETE' })
  }

  // Network
  getNetworkStatus() {
    return this.request<NetworkStatus>('/network/status')
  }

  getNetworkInterfaces() {
    return this.request<NetworkInterfaceInfo[]>('/network/interfaces')
  }

  getNetworkInterface(name: string) {
    return this.request<InterfaceDetail>(`/network/interfaces/${encodeURIComponent(name)}`)
  }

  configureInterface(name: string, config: InterfaceConfig) {
    return this.request(`/network/interfaces/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify(config),
    })
  }

  applyNetworkConfig() {
    return this.request<{ message: string }>('/network/apply', {
      method: 'POST',
    })
  }

  getDNSConfig() {
    return this.request<{ servers: string[]; search: string[] }>('/network/dns')
  }

  configureDNS(config: { servers: string[] }) {
    return this.request('/network/dns', {
      method: 'PUT',
      body: JSON.stringify(config),
    })
  }

  getRoutes() {
    return this.request<NetworkRoute[]>('/network/routes')
  }

  getBonds() {
    return this.request<NetworkInterfaceInfo[]>('/network/bonds')
  }

  createBond(data: { name: string; mode: string; slaves: string[]; primary?: string }) {
    return this.request('/network/bonds', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  deleteBond(name: string) {
    return this.request(`/network/bonds/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    })
  }

  // Packages
  checkUpdates() {
    return this.request<{ updates: PackageUpdate[]; total: number; last_checked: string }>('/packages/updates')
  }

  upgradePackages(packages?: string[]) {
    return this.request('/packages/upgrade', {
      method: 'POST',
      body: JSON.stringify({ packages }),
    })
  }

  installPackage(name: string) {
    return this.request('/packages/install', {
      method: 'POST',
      body: JSON.stringify({ name }),
    })
  }

  removePackage(name: string) {
    return this.request('/packages/remove', {
      method: 'POST',
      body: JSON.stringify({ name }),
    })
  }

  searchPackages(query: string) {
    return this.request<{ packages: PackageSearchResult[]; total: number; query: string }>(
      `/packages/search?q=${encodeURIComponent(query)}`,
    )
  }

  getDockerStatus() {
    return this.request<{ installed: boolean; version: string; running: boolean; compose_available: boolean }>(
      '/packages/docker-status',
    )
  }

  installDocker() {
    return this.request('/packages/install-docker', { method: 'POST' })
  }

  // Disk Management - Tool Status
  checkSmartmontools() {
    return this.request<{ installed: boolean }>('/disks/smartmontools-status')
  }

  installSmartmontools() {
    return this.request<{ message: string; output: string }>('/disks/install-smartmontools', {
      method: 'POST',
    })
  }

  // Disk Management - Overview
  getDiskOverview() {
    return this.request<BlockDevice[]>('/disks/overview')
  }

  getDiskSmart(device: string) {
    return this.request<SmartInfo>(`/disks/${encodeURIComponent(device)}/smart`)
  }

  getDiskIOStats() {
    return this.request<IOStat[]>('/disks/iostat')
  }

  getDiskUsage(path: string, depth: number = 1) {
    return this.request<DiskUsageEntry>('/disks/usage', {
      method: 'POST',
      body: JSON.stringify({ path, depth }),
    })
  }

  // Disk Management - Partitions
  getPartitions(device: string) {
    return this.request<{ device: string; partitions: Array<{ number: number; start: string; end: string; size: string; type: string; filesystem: string; flags: string[] }> }>(`/disks/${encodeURIComponent(device)}/partitions`)
  }

  createPartition(device: string, data: { start: string; end: string; fs_type: string }) {
    return this.request(`/disks/${encodeURIComponent(device)}/partitions`, {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  deletePartition(device: string, partition: string) {
    return this.request(`/disks/${encodeURIComponent(device)}/partitions/${encodeURIComponent(partition)}`, {
      method: 'DELETE',
    })
  }

  // Disk Management - Filesystems
  getFilesystems() {
    return this.request<Filesystem[]>('/filesystems')
  }

  formatPartition(data: { device: string; fs_type: string; label?: string }) {
    return this.request('/filesystems/format', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  mountFilesystem(data: { device: string; mount_point: string; fs_type?: string; options?: string }) {
    return this.request('/filesystems/mount', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  unmountFilesystem(mountPoint: string) {
    return this.request('/filesystems/unmount', {
      method: 'POST',
      body: JSON.stringify({ mount_point: mountPoint }),
    })
  }

  resizeFilesystem(data: { device: string }) {
    return this.request('/filesystems/resize', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  checkFilesystemExpand() {
    return this.request<ExpandCandidate[]>('/filesystems/expand-check')
  }

  expandFilesystem(source: string) {
    return this.request<{ message: string; steps: string[] }>('/filesystems/expand', {
      method: 'POST',
      body: JSON.stringify({ source }),
    })
  }

  // Disk Management - LVM
  getLVMPVs() {
    return this.request<PhysicalVolume[]>('/lvm/pvs')
  }

  getLVMVGs() {
    return this.request<VolumeGroup[]>('/lvm/vgs')
  }

  getLVMLVs() {
    return this.request<LogicalVolume[]>('/lvm/lvs')
  }

  createPV(device: string) {
    return this.request('/lvm/pvs', {
      method: 'POST',
      body: JSON.stringify({ device }),
    })
  }

  createVG(name: string, pvs: string[]) {
    return this.request('/lvm/vgs', {
      method: 'POST',
      body: JSON.stringify({ name, pvs }),
    })
  }

  createLV(name: string, vg: string, size: string) {
    return this.request('/lvm/lvs', {
      method: 'POST',
      body: JSON.stringify({ name, vg, size }),
    })
  }

  removePV(name: string) {
    return this.request(`/lvm/pvs/${encodeURIComponent(name)}`, { method: 'DELETE' })
  }

  removeVG(name: string) {
    return this.request(`/lvm/vgs/${encodeURIComponent(name)}`, { method: 'DELETE' })
  }

  removeLV(vg: string, name: string) {
    return this.request(`/lvm/lvs/${encodeURIComponent(vg)}/${encodeURIComponent(name)}`, { method: 'DELETE' })
  }

  resizeLV(data: { vg: string; name: string; size: string }) {
    return this.request('/lvm/lvs/resize', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  // Disk Management - RAID
  getRAIDArrays() {
    return this.request<RAIDArray[]>('/raid')
  }

  getRAIDDetail(name: string) {
    return this.request<RAIDArray>(`/raid/${encodeURIComponent(name)}`)
  }

  createRAID(data: { name: string; level: string; devices: string[] }) {
    return this.request('/raid', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  deleteRAID(name: string) {
    return this.request(`/raid/${encodeURIComponent(name)}`, { method: 'DELETE' })
  }

  addRAIDDisk(name: string, device: string) {
    return this.request(`/raid/${encodeURIComponent(name)}/add`, {
      method: 'POST',
      body: JSON.stringify({ device }),
    })
  }

  removeRAIDDisk(name: string, device: string) {
    return this.request(`/raid/${encodeURIComponent(name)}/remove`, {
      method: 'POST',
      body: JSON.stringify({ device }),
    })
  }

  // Disk Management - Swap
  getSwapInfo() {
    return this.request<SwapInfo>('/swap')
  }

  createSwap(data: { type: string; path?: string; size_mb?: number; device?: string }) {
    return this.request('/swap', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  removeSwap(path: string) {
    return this.request('/swap', {
      method: 'DELETE',
      body: JSON.stringify({ path }),
    })
  }

  setSwappiness(value: number) {
    return this.request('/swap/swappiness', {
      method: 'PUT',
      body: JSON.stringify({ value }),
    })
  }

  checkSwapResize(path: string) {
    return this.request<{
      current_size_mb: number
      disk_free_mb: number
      max_size_mb: number
      swap_used_mb: number
      ram_free_mb: number
      swapoff_safe: boolean
    }>(`/swap/resize-check?path=${encodeURIComponent(path)}`)
  }

  resizeSwap(data: { path: string; new_size_mb: number }) {
    return this.request<{
      success: boolean
      steps: Array<{ name: string; status: string; output: string }>
      message?: string
    }>('/swap/resize', {
      method: 'PUT',
      body: JSON.stringify(data),
    })
  }

  // WireGuard VPN
  getWireGuardStatus() {
    return this.request<import('@/types/api').WireGuardStatus>('/network/wireguard/status')
  }

  installWireGuard() {
    return this.request<{ message: string }>('/network/wireguard/install', { method: 'POST' })
  }

  getWireGuardInterfaces() {
    return this.request<import('@/types/api').WireGuardInterface[]>('/network/wireguard/interfaces')
  }

  getWireGuardInterface(name: string) {
    return this.request<import('@/types/api').WireGuardInterface>(`/network/wireguard/interfaces/${encodeURIComponent(name)}`)
  }

  wireGuardInterfaceUp(name: string) {
    return this.request<{ message: string }>(`/network/wireguard/interfaces/${encodeURIComponent(name)}/up`, { method: 'POST' })
  }

  wireGuardInterfaceDown(name: string) {
    return this.request<{ message: string }>(`/network/wireguard/interfaces/${encodeURIComponent(name)}/down`, { method: 'POST' })
  }

  createWireGuardConfig(name: string, content: string) {
    return this.request<{ message: string }>('/network/wireguard/configs', {
      method: 'POST',
      body: JSON.stringify({ name, content }),
    })
  }

  getWireGuardConfig(name: string) {
    return this.request<{ name: string; content: string }>(`/network/wireguard/configs/${encodeURIComponent(name)}`)
  }

  updateWireGuardConfig(name: string, content: string) {
    return this.request<{ message: string }>(`/network/wireguard/configs/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify({ content }),
    })
  }

  deleteWireGuardConfig(name: string) {
    return this.request<{ message: string }>(`/network/wireguard/configs/${encodeURIComponent(name)}`, { method: 'DELETE' })
  }

  // Tailscale VPN
  getTailscaleStatus() {
    return this.request<import('@/types/api').TailscaleStatus>('/network/tailscale/status')
  }

  // installTailscale uses SSE streaming — call via fetch directly in component

  checkTailscaleUpdate() {
    return this.request<{ current_version: string; update_available: boolean; new_version: string; apt_output: string }>('/network/tailscale/update-check')
  }

  tailscaleUp(authKey?: string, exitNode?: string) {
    return this.request<{ message: string } | { needs_auth: boolean; auth_url: string }>('/network/tailscale/up', {
      method: 'POST',
      body: JSON.stringify({ auth_key: authKey, exit_node: exitNode }),
    })
  }

  tailscaleDown() {
    return this.request<{ message: string }>('/network/tailscale/down', { method: 'POST' })
  }

  tailscaleLogout() {
    return this.request<{ message: string }>('/network/tailscale/logout', { method: 'POST' })
  }

  getTailscalePeers() {
    return this.request<import('@/types/api').TailscalePeer[]>('/network/tailscale/peers')
  }

  setTailscalePreferences(options: { exit_node?: string; accept_routes?: boolean; advertise_exit_node?: boolean }) {
    return this.request<{ message: string }>('/network/tailscale/preferences', {
      method: 'PUT',
      body: JSON.stringify(options),
    })
  }

  // Firewall (UFW)
  getFirewallStatus() {
    return this.request<{ active: boolean; default_incoming: string; default_outgoing: string }>('/firewall/status')
  }

  enableFirewall() {
    return this.request<{ message: string }>('/firewall/enable', { method: 'POST' })
  }

  disableFirewall() {
    return this.request<{ message: string }>('/firewall/disable', { method: 'POST' })
  }

  getFirewallRules() {
    return this.request<{ rules: Array<{ number: number; to: string; action: string; from: string; comment: string; v6: boolean }>; total: number }>('/firewall/rules')
  }

  addFirewallRule(data: { action: string; port: string; protocol: string; from: string; to: string; comment: string }) {
    return this.request<{ message: string; output: string }>('/firewall/rules', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  deleteFirewallRule(number: number) {
    return this.request<{ message: string }>(`/firewall/rules/${number}`, { method: 'DELETE' })
  }

  getListeningPorts() {
    return this.request<{ ports: Array<{ protocol: string; address: string; port: number; pid: number; process: string }>; total: number }>('/firewall/ports')
  }

  // Fail2ban
  getFail2banStatus() {
    return this.request<{ installed: boolean; running: boolean; version: string }>('/fail2ban/status')
  }

  installFail2ban() {
    return this.request<{ message: string }>('/fail2ban/install', { method: 'POST' })
  }

  getFail2banTemplates() {
    return this.request<{ templates: Array<{ id: string; name: string; description: string; filter: string; log_path: string; max_retry: number; ban_time: number; find_time: number; available: boolean }> }>('/fail2ban/templates')
  }

  createFail2banJail(data: { id: string; max_retry: number; ban_time: number; find_time: number; log_path?: string; name?: string; filter?: string; ignoreip?: string }) {
    return this.request<{ message: string }>('/fail2ban/jails', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  deleteFail2banJail(name: string) {
    return this.request<{ message: string }>(`/fail2ban/jails/${encodeURIComponent(name)}`, { method: 'DELETE' })
  }

  getFail2banJails() {
    return this.request<{ jails: Array<{ name: string; enabled: boolean; filter: string; banned_count: number; total_banned: number; banned_ips: string[]; max_retry: number; ban_time: string; find_time: string; ignoreip: string }>; total: number }>('/fail2ban/jails')
  }

  getFail2banJailDetail(name: string) {
    return this.request<{ name: string; enabled: boolean; filter: string; banned_count: number; total_banned: number; banned_ips: string[]; max_retry: number; ban_time: string; find_time: string; ignoreip: string }>(`/fail2ban/jails/${encodeURIComponent(name)}`)
  }

  updateFail2banJailConfig(name: string, config: { max_retry?: number; ban_time?: string; find_time?: string; ignoreip?: string }) {
    return this.request<{ message: string }>(`/fail2ban/jails/${encodeURIComponent(name)}/config`, {
      method: 'PUT',
      body: JSON.stringify(config),
    })
  }

  enableFail2banJail(name: string) {
    return this.request<{ message: string }>(`/fail2ban/jails/${encodeURIComponent(name)}/enable`, { method: 'POST' })
  }

  disableFail2banJail(name: string) {
    return this.request<{ message: string }>(`/fail2ban/jails/${encodeURIComponent(name)}/disable`, { method: 'POST' })
  }

  unbanFail2banIP(jail: string, ip: string) {
    return this.request<{ message: string }>(`/fail2ban/jails/${encodeURIComponent(jail)}/unban`, {
      method: 'POST',
      body: JSON.stringify({ ip }),
    })
  }

  // Docker Firewall (DOCKER-USER chain)
  getDockerFirewall() {
    return this.request<{
      ports: Array<{
        container_name: string
        container_ip: string
        host_port: number
        container_port: number
        protocol: string
        host_ip: string
      }>
      rules: Array<{
        number: number
        port: number
        protocol: string
        source: string
        action: string
      }>
    }>('/firewall/docker')
  }

  addDockerUserRule(data: { port: number; protocol: string; source: string; action: string }) {
    return this.request<{ message: string }>('/firewall/docker/rules', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  deleteDockerUserRule(number: number) {
    return this.request<{ message: string }>(`/firewall/docker/rules/${number}`, { method: 'DELETE' })
  }

  // Systemd Services
  listServices(params?: { q?: string; sort?: string; type?: string }) {
    const query = new URLSearchParams()
    if (params?.q) query.set('q', params.q)
    if (params?.sort) query.set('sort', params.sort)
    if (params?.type) query.set('type', params.type)
    const qs = query.toString()
    return this.request<{ services: ServiceInfo[]; total: number }>(`/system/services${qs ? `?${qs}` : ''}`)
  }

  startService(name: string) {
    return this.request<{ message: string }>(`/system/services/${name}/start`, { method: 'POST' })
  }

  stopService(name: string) {
    return this.request<{ message: string }>(`/system/services/${name}/stop`, { method: 'POST' })
  }

  restartService(name: string) {
    return this.request<{ message: string }>(`/system/services/${name}/restart`, { method: 'POST' })
  }

  enableService(name: string) {
    return this.request<{ message: string }>(`/system/services/${name}/enable`, { method: 'POST' })
  }

  disableService(name: string) {
    return this.request<{ message: string }>(`/system/services/${name}/disable`, { method: 'POST' })
  }

  getServiceLogs(name: string, lines?: number) {
    const qs = lines ? `?lines=${lines}` : ''
    return this.request<{ logs: string }>(`/system/services/${name}/logs${qs}`)
  }

  getServiceDeps(name: string) {
    return this.request<ServiceDeps>(`/system/services/${name}/deps`)
  }
}

export const api = new ApiClient()
