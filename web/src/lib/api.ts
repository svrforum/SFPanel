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
  TuningStatus,
  AppStoreCategory,
  AppStoreApp,
  AppStoreAppDetail,
  AppStoreInstalledApp,
  ProcessInfo,
  ClusterStatus,
  ClusterOverview,
  ClusterNodesResponse,
  ClusterTokenResponse,
  ClusterEventsResponse,
  ClusterInterfacesResponse,
  ClusterInitResponse,
} from '@/types/api'

const API_BASE = '/api/v1'

class ApiClient {
  private token: string | null = null
  private _currentNode: string | null = null

  constructor() {
    this.token = localStorage.getItem('token')
    this._currentNode = localStorage.getItem('sfpanel_current_node')
  }

  get currentNode(): string | null {
    return this._currentNode
  }

  setCurrentNode(nodeId: string | null) {
    this._currentNode = nodeId
    if (nodeId) {
      localStorage.setItem('sfpanel_current_node', nodeId)
    } else {
      localStorage.removeItem('sfpanel_current_node')
    }
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

  isAuthenticated(): boolean {
    return !!this.token
  }

  private async request<T>(path: string, options: RequestInit & { local?: boolean } = {}): Promise<T> {
    const { local, ...fetchOptions } = options
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...((fetchOptions.headers as Record<string, string>) || {}),
    }

    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }

    let url = `${API_BASE}${path}`
    if (this._currentNode && !local) {
      const separator = url.includes('?') ? '&' : '?'
      url += `${separator}node=${encodeURIComponent(this._currentNode)}`
    }

    const res = await fetch(url, {
      ...fetchOptions,
      headers,
    })

    if (res.status === 401 && !path.startsWith('/auth/')) {
      this.clearToken()
      window.location.href = '/login'
      throw new Error('Session expired')
    }

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

  // System tuning
  getTuningStatus() {
    return this.request<TuningStatus>('/system/tuning')
  }

  applyTuning(categories?: string[]) {
    return this.request<{ message: string; output: string; timeout: number }>('/system/tuning/apply', {
      method: 'POST',
      body: JSON.stringify({ categories: categories || [] }),
    })
  }

  confirmTuning() {
    return this.request<{ message: string }>('/system/tuning/confirm', {
      method: 'POST',
    })
  }

  resetTuning() {
    return this.request<{ message: string }>('/system/tuning/reset', {
      method: 'POST',
    })
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

  listProcesses() {
    return this.request<{ processes: ProcessInfo[]; total: number }>(
      '/system/processes/list'
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

  pauseContainer(id: string) {
    return this.request(`/docker/containers/${encodeURIComponent(id)}/pause`, { method: 'POST' })
  }

  unpauseContainer(id: string) {
    return this.request(`/docker/containers/${encodeURIComponent(id)}/unpause`, { method: 'POST' })
  }

  // Docker Images
  getImages() {
    return this.request<DockerImage[]>('/docker/images')
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

  checkImageUpdates() {
    return this.request<import('@/types/api').ImageUpdateStatus[]>('/docker/images/updates')
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

  inspectNetwork(id: string) {
    return this.request<import('@/types/api').NetworkInspectDetail>(`/docker/networks/${encodeURIComponent(id)}/inspect`)
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

  deleteComposeProject(project: string, options?: { removeImages?: boolean; removeVolumes?: boolean }) {
    const params = new URLSearchParams()
    if (options?.removeImages) params.set('removeImages', 'true')
    if (options?.removeVolumes) params.set('removeVolumes', 'true')
    const qs = params.toString()
    return this.request(`/docker/compose/${project}${qs ? '?' + qs : ''}`, { method: 'DELETE' })
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

  validateCompose(project: string) {
    return this.request<import('@/types/api').ComposeValidationResult>(`/docker/compose/${encodeURIComponent(project)}/validate`, { method: 'POST' })
  }

  checkStackUpdates(project: string) {
    return this.request<import('@/types/api').StackUpdateCheck>(`/docker/compose/${encodeURIComponent(project)}/check-updates`, { method: 'POST' })
  }

  updateStack(project: string) {
    return this.request<{ output: string }>(`/docker/compose/${encodeURIComponent(project)}/update`, { method: 'POST' })
  }

  rollbackStack(project: string) {
    return this.request<{ output: string }>(`/docker/compose/${encodeURIComponent(project)}/rollback`, { method: 'POST' })
  }

  hasRollback(project: string) {
    return this.request<{ has_rollback: boolean }>(`/docker/compose/${encodeURIComponent(project)}/has-rollback`)
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

  getNodeStatus() {
    return this.request<{ installed: boolean; version: string; nvm_installed: boolean; npm_version: string }>(
      '/packages/node-status',
    )
  }

  installNode() {
    return this.request('/packages/install-node', { method: 'POST' })
  }

  getNodeVersions() {
    return this.request<{ versions: { version: string; active: boolean; lts: boolean }[]; current: string; remote_lts: string[] }>(
      '/packages/node-versions',
    )
  }

  switchNodeVersion(version: string) {
    return this.request<{ switched: string; output: string }>('/packages/node-switch', {
      method: 'POST',
      body: JSON.stringify({ version }),
    })
  }

  installNodeVersion(version: string) {
    return this.request('/packages/node-install-version', {
      method: 'POST',
      body: JSON.stringify({ version }),
    })
  }

  uninstallNodeVersion(version: string) {
    return this.request<{ removed: string; output: string }>('/packages/node-uninstall-version', {
      method: 'POST',
      body: JSON.stringify({ version }),
    })
  }

  getClaudeStatus() {
    return this.request<{ installed: boolean; version: string }>('/packages/claude-status')
  }

  installClaude() {
    return this.request('/packages/install-claude', { method: 'POST' })
  }

  getCodexStatus() {
    return this.request<{ installed: boolean; version: string }>('/packages/codex-status')
  }

  installCodex() {
    return this.request('/packages/install-codex', { method: 'POST' })
  }

  getGeminiStatus() {
    return this.request<{ installed: boolean; version: string }>('/packages/gemini-status')
  }

  installGemini() {
    return this.request('/packages/install-gemini', { method: 'POST' })
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
  listServices() {
    return this.request<{ services: ServiceInfo[]; total: number }>('/system/services')
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

  // App Store
  getAppStoreCategories() {
    return this.request<AppStoreCategory[]>('/appstore/categories')
  }

  getAppStoreApps(category?: string) {
    const query = category ? `?category=${category}` : ''
    return this.request<AppStoreApp[]>(`/appstore/apps${query}`)
  }

  getAppStoreApp(id: string) {
    return this.request<AppStoreAppDetail>(`/appstore/apps/${id}`)
  }

  getInstalledApps() {
    return this.request<AppStoreInstalledApp[]>('/appstore/installed')
  }

  refreshAppStore() {
    return this.request<{ message: string; apps: number; categories: number }>('/appstore/refresh', { method: 'POST' })
  }

  // Cluster
  getClusterStatus(local?: boolean) {
    return this.request<ClusterStatus>('/cluster/status', { local })
  }

  getClusterInterfaces() {
    return this.request<ClusterInterfacesResponse>('/cluster/interfaces')
  }

  initCluster(name: string, advertiseAddress: string) {
    return this.request<ClusterInitResponse>('/cluster/init', {
      method: 'POST',
      body: JSON.stringify({ name, advertise_address: advertiseAddress }),
    })
  }

  getClusterOverview() {
    return this.request<ClusterOverview>('/cluster/overview')
  }

  getClusterNodes(local?: boolean) {
    return this.request<ClusterNodesResponse>('/cluster/nodes', { local })
  }

  createClusterToken(ttl?: string) {
    return this.request<ClusterTokenResponse>('/cluster/token', {
      method: 'POST',
      body: JSON.stringify({ ttl: ttl || '' }),
    })
  }

  removeClusterNode(nodeId: string) {
    return this.request<{ removed: string }>(`/cluster/nodes/${encodeURIComponent(nodeId)}`, {
      method: 'DELETE',
    })
  }

  getClusterEvents(limit?: number) {
    const params = limit ? `?limit=${limit}` : ''
    return this.request<ClusterEventsResponse>(`/cluster/events${params}`)
  }

  updateClusterNodeLabels(nodeId: string, labels: Record<string, string>) {
    return this.request<{ node_id: string; labels: Record<string, string> }>(`/cluster/nodes/${encodeURIComponent(nodeId)}/labels`, {
      method: 'PATCH',
      body: JSON.stringify({ labels }),
    })
  }

  transferClusterLeadership(targetNodeId: string) {
    return this.request<{ message: string; target_node_id: string }>('/cluster/leader-transfer', {
      method: 'POST',
      body: JSON.stringify({ target_node_id: targetNodeId }),
    })
  }

  disbandCluster() {
    return this.request<{ message: string }>('/cluster/disband', {
      method: 'POST',
    })
  }

  // Build a WebSocket URL with auth token and optional node parameter
  buildWsUrl(path: string, extraParams?: Record<string, string>): string {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const params = new URLSearchParams()
    if (this.token) params.set('token', this.token)
    if (this._currentNode) params.set('node', this._currentNode)
    if (extraParams) {
      for (const [k, v] of Object.entries(extraParams)) {
        params.set(k, v)
      }
    }
    return `${protocol}//${window.location.host}${path}?${params.toString()}`
  }
}

export const api = new ApiClient()
