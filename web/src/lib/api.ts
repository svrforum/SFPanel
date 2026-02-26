const API_BASE = '/api/v1'

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

  // System
  getSystemInfo() {
    return this.request<{ host: any; metrics: any }>('/system/info')
  }

  getTopProcesses() {
    return this.request<Array<{ pid: number; name: string; cpu: number; memory: number; status: string }>>('/system/processes')
  }

  getMetricsHistory() {
    return this.request<Array<{ time: number; cpu: number; mem_percent: number }>>('/system/metrics-history')
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
    return this.request<any[]>('/docker/containers')
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

  removeContainer(id: string) {
    return this.request(`/docker/containers/${id}`, { method: 'DELETE' })
  }

  // Docker Images
  getImages() {
    return this.request<any[]>('/docker/images')
  }

  pullImage(image: string) {
    return this.request('/docker/images/pull', {
      method: 'POST',
      body: JSON.stringify({ image }),
    })
  }

  removeImage(id: string) {
    return this.request(`/docker/images/${encodeURIComponent(id)}`, { method: 'DELETE' })
  }

  // Docker Volumes
  getVolumes() {
    return this.request<any[]>('/docker/volumes')
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
    return this.request<any[]>('/docker/networks')
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

  // Docker Compose
  getComposeProjects() {
    return this.request<any[]>('/docker/compose')
  }

  createComposeProject(name: string, yaml: string) {
    return this.request('/docker/compose', {
      method: 'POST',
      body: JSON.stringify({ name, yaml }),
    })
  }

  getComposeProject(project: string) {
    return this.request<{ project: any; yaml: string }>(`/docker/compose/${project}`)
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

  // File Manager
  listFiles(path: string) {
    return this.request<any[]>(`/files?path=${encodeURIComponent(path)}`)
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

  async uploadFile(destPath: string, file: File) {
    const formData = new FormData()
    formData.append('file', file)
    formData.append('path', destPath)

    const headers: Record<string, string> = {}
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`
    }

    const res = await fetch(`${API_BASE}/files/upload`, {
      method: 'POST',
      headers,
      body: formData,
    })

    const json = await res.json()
    if (!json.success) {
      throw new Error(json.error?.message || 'Upload failed')
    }
    return json.data
  }

  // Logs
  getLogSources() {
    return this.request<any[]>('/logs/sources')
  }

  readLog(source: string, lines: number = 100) {
    return this.request<{ source: string; lines: string[]; total_lines: number }>(
      `/logs/read?source=${encodeURIComponent(source)}&lines=${lines}`,
    )
  }

  // Cron Jobs
  getCronJobs() {
    return this.request<any[]>('/cron')
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
  getNetworkInterfaces() {
    return this.request<any[]>('/network/interfaces')
  }

  getNetworkInterface(name: string) {
    return this.request<any>(`/network/interfaces/${encodeURIComponent(name)}`)
  }

  configureInterface(name: string, config: any) {
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
    return this.request<any[]>('/network/routes')
  }

  getBonds() {
    return this.request<any[]>('/network/bonds')
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
    return this.request<{ updates: any[]; total: number; last_checked: string }>('/packages/updates')
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
    return this.request<{ packages: any[]; total: number; query: string }>(
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
}

export const api = new ApiClient()
