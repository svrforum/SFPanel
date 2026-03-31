import { useState, useEffect, useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Package,
  Download,
  RefreshCw,
  Search,
  Check,
  X,
  Server,
  Loader2,
  Trash2,
  CheckCircle2,
  AlertCircle,
  Settings2,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

import type { PackageUpdate as PackageInfo, PackageSearchResult, DockerStatus } from '@/types/api'

interface DevToolStatus {
  installed: boolean
  version: string
  [key: string]: unknown
}

interface LoadingState {
  docker: boolean
  dockerInstall: boolean
  node: boolean
  nodeInstall: boolean
  claude: boolean
  claudeInstall: boolean
  codex: boolean
  codexInstall: boolean
  gemini: boolean
  geminiInstall: boolean
  updates: boolean
  upgrade: boolean
  install: string | null
  remove: string | null
  search: boolean
}

interface OutputDialog {
  open: boolean
  title: string
  output: string
  done: boolean
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function Packages() {
  const { t } = useTranslation()

  // Docker status
  const [dockerStatus, setDockerStatus] = useState<DockerStatus | null>(null)

  // Dev tool statuses
  const [nodeStatus, setNodeStatus] = useState<DevToolStatus | null>(null)
  const [claudeStatus, setClaudeStatus] = useState<DevToolStatus | null>(null)
  const [codexStatus, setCodexStatus] = useState<DevToolStatus | null>(null)
  const [geminiStatus, setGeminiStatus] = useState<DevToolStatus | null>(null)

  // Node version management
  const [nodeVersionDialog, setNodeVersionDialog] = useState(false)
  const [nodeVersions, setNodeVersions] = useState<{ version: string; active: boolean; lts: boolean }[]>([])
  const [remoteLTS, setRemoteLTS] = useState<string[]>([])
  const [, setNodeCurrent] = useState('')
  const [nodeVersionLoading, setNodeVersionLoading] = useState(false)
  const [nodeSwitching, setNodeSwitching] = useState<string | null>(null)
  const [nodeInstallingVersion, setNodeInstallingVersion] = useState<string | null>(null)
  const [nodeRemoving, setNodeRemoving] = useState<string | null>(null)

  // System updates
  const [updates, setUpdates] = useState<PackageInfo[]>([])
  const [lastChecked, setLastChecked] = useState<string | null>(null)
  const [selectedPackages, setSelectedPackages] = useState<Set<string>>(new Set())

  // Search
  const [searchQuery, setSearchQuery] = useState('')
  const [searchResults, setSearchResults] = useState<PackageSearchResult[]>([])
  const [hasSearched, setHasSearched] = useState(false)

  // Loading
  const [loading, setLoading] = useState<LoadingState>({
    docker: false,
    dockerInstall: false,
    node: false,
    nodeInstall: false,
    claude: false,
    claudeInstall: false,
    codex: false,
    codexInstall: false,
    gemini: false,
    geminiInstall: false,
    updates: false,
    upgrade: false,
    install: null,
    remove: null,
    search: false,
  })

  // Output dialog
  const [outputDialog, setOutputDialog] = useState<OutputDialog>({
    open: false,
    title: '',
    output: '',
    done: false,
  })

  // Auto-scroll ref for output dialog
  const outputRef = useRef<HTMLDivElement>(null)

  // ---------------------------------------------------------------------------
  // Helpers
  // ---------------------------------------------------------------------------

  const setLoadingKey = useCallback(
    <K extends keyof LoadingState>(key: K, value: LoadingState[K]) => {
      setLoading((prev) => ({ ...prev, [key]: value }))
    },
    [],
  )

  const openOutput = useCallback((title: string) => {
    setOutputDialog({ open: true, title, output: '', done: false })
  }, [])

  const appendOutput = useCallback((text: string) => {
    setOutputDialog((prev) => ({ ...prev, output: prev.output + text }))
  }, [])

  const finishOutput = useCallback(() => {
    setOutputDialog((prev) => ({ ...prev, done: true }))
  }, [])

  // Auto-scroll output to bottom
  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight
    }
  }, [outputDialog.output])

  // ---------------------------------------------------------------------------
  // Docker
  // ---------------------------------------------------------------------------

  const fetchDockerStatus = useCallback(async () => {
    setLoadingKey('docker', true)
    try {
      const data = await api.getDockerStatus()
      setDockerStatus(data)
    } catch {
      toast.error(t('packages.dockerStatusFailed'))
    } finally {
      setLoadingKey('docker', false)
    }
  }, [setLoadingKey, t])

  const handleInstallDocker = useCallback(async () => {
    setLoadingKey('dockerInstall', true)
    openOutput(t('packages.installingDocker'))
    try {
      const token = api.getToken()
      const res = await fetch(`${api.apiBase}/packages/install-docker`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}` },
      })

      if (!res.ok || !res.body) {
        throw new Error('Failed to start Docker installation')
      }

      const reader = res.body.getReader()
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
            const data = line.slice(6)
            if (data === '[DONE]') {
              finishOutput()
            } else {
              appendOutput(data + '\n')
            }
          }
        }
      }

      toast.success(t('packages.dockerInstallSuccess'))
      finishOutput()
      await fetchDockerStatus()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('packages.dockerInstallFailed')
      appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
      finishOutput()
      toast.error(message)
    } finally {
      setLoadingKey('dockerInstall', false)
    }
  }, [setLoadingKey, openOutput, appendOutput, finishOutput, fetchDockerStatus, t])

  // ---------------------------------------------------------------------------
  // Dev Tools (Node.js, Claude Code, Codex)
  // ---------------------------------------------------------------------------

  const fetchNodeStatus = useCallback(async () => {
    setLoadingKey('node', true)
    try {
      const data = await api.getNodeStatus()
      setNodeStatus(data)
    } catch {
      // silent
    } finally {
      setLoadingKey('node', false)
    }
  }, [setLoadingKey])

  const fetchClaudeStatus = useCallback(async () => {
    setLoadingKey('claude', true)
    try {
      const data = await api.getClaudeStatus()
      setClaudeStatus(data)
    } catch {
      // silent
    } finally {
      setLoadingKey('claude', false)
    }
  }, [setLoadingKey])

  const fetchCodexStatus = useCallback(async () => {
    setLoadingKey('codex', true)
    try {
      const data = await api.getCodexStatus()
      setCodexStatus(data)
    } catch {
      // silent
    } finally {
      setLoadingKey('codex', false)
    }
  }, [setLoadingKey])

  const fetchGeminiStatus = useCallback(async () => {
    setLoadingKey('gemini', true)
    try {
      const data = await api.getGeminiStatus()
      setGeminiStatus(data)
    } catch {
      // silent
    } finally {
      setLoadingKey('gemini', false)
    }
  }, [setLoadingKey])

  const handleSSEInstall = useCallback(async (
    url: string,
    loadingKey: keyof LoadingState,
    title: string,
    successMsg: string,
    refreshFn: () => Promise<void>,
  ) => {
    setLoadingKey(loadingKey, true as LoadingState[typeof loadingKey])
    openOutput(title)
    try {
      const token = api.getToken()
      const nodeParam = api.currentNode ? `?node=${encodeURIComponent(api.currentNode)}` : ''
      const res = await fetch(`${api.apiBase}${url}${nodeParam}`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok || !res.body) throw new Error('Failed to start installation')

      const reader = res.body.getReader()
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
            const data = line.slice(6)
            if (data === '[DONE]') {
              finishOutput()
            } else {
              appendOutput(data + '\n')
            }
          }
        }
      }

      toast.success(successMsg)
      finishOutput()
      await refreshFn()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('packages.installFailed', { name: title })
      appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
      finishOutput()
      toast.error(message)
    } finally {
      setLoadingKey(loadingKey, false as LoadingState[typeof loadingKey])
    }
  }, [setLoadingKey, openOutput, appendOutput, finishOutput, t])

  const handleInstallNode = useCallback(() =>
    handleSSEInstall('/packages/install-node', 'nodeInstall', t('packages.installingNode'), t('packages.nodeInstallSuccess'), fetchNodeStatus),
  [handleSSEInstall, fetchNodeStatus, t])

  const handleInstallClaude = useCallback(() =>
    handleSSEInstall('/packages/install-claude', 'claudeInstall', t('packages.installingClaude'), t('packages.claudeInstallSuccess'), fetchClaudeStatus),
  [handleSSEInstall, fetchClaudeStatus, t])

  const handleInstallCodex = useCallback(() =>
    handleSSEInstall('/packages/install-codex', 'codexInstall', t('packages.installingCodex'), t('packages.codexInstallSuccess'), fetchCodexStatus),
  [handleSSEInstall, fetchCodexStatus, t])

  const handleInstallGemini = useCallback(() =>
    handleSSEInstall('/packages/install-gemini', 'geminiInstall', t('packages.installingGemini'), t('packages.geminiInstallSuccess'), fetchGeminiStatus),
  [handleSSEInstall, fetchGeminiStatus, t])

  // ---------------------------------------------------------------------------
  // Node.js Version Management
  // ---------------------------------------------------------------------------

  const fetchNodeVersions = useCallback(async () => {
    setNodeVersionLoading(true)
    try {
      const data = await api.getNodeVersions()
      setNodeVersions(data.versions || [])
      setRemoteLTS(data.remote_lts || [])
      setNodeCurrent(data.current || '')
    } catch {
      toast.error(t('packages.nodeVersionsFailed'))
    } finally {
      setNodeVersionLoading(false)
    }
  }, [t])

  const openNodeVersionDialog = useCallback(() => {
    setNodeVersionDialog(true)
    fetchNodeVersions()
  }, [fetchNodeVersions])

  const handleSwitchNodeVersion = useCallback(async (version: string) => {
    setNodeSwitching(version)
    try {
      await api.switchNodeVersion(version)
      toast.success(t('packages.nodeSwitched', { version }))
      await fetchNodeVersions()
      await fetchNodeStatus()
    } catch {
      toast.error(t('packages.nodeSwitchFailed'))
    } finally {
      setNodeSwitching(null)
    }
  }, [fetchNodeVersions, fetchNodeStatus, t])

  const handleInstallNodeVersion = useCallback(async (version: string) => {
    setNodeInstallingVersion(version)
    openOutput(t('packages.installingNodeVersion', { version }))
    try {
      const token = api.getToken()
      const res = await fetch(`${api.apiBase}/packages/node-install-version`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ version }),
      })
      if (!res.ok || !res.body) throw new Error('Failed to start installation')

      const reader = res.body.getReader()
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
            const data = line.slice(6)
            if (data === '[DONE]') finishOutput()
            else appendOutput(data + '\n')
          }
        }
      }
      finishOutput()
      toast.success(t('packages.nodeVersionInstalled', { version }))
      await fetchNodeVersions()
      await fetchNodeStatus()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('packages.installFailed', { name: 'Node.js' })
      appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
      finishOutput()
      toast.error(message)
    } finally {
      setNodeInstallingVersion(null)
    }
  }, [openOutput, appendOutput, finishOutput, fetchNodeVersions, fetchNodeStatus, t])

  const handleUninstallNodeVersion = useCallback(async (version: string) => {
    setNodeRemoving(version)
    try {
      await api.uninstallNodeVersion(version)
      toast.success(t('packages.nodeVersionRemoved', { version }))
      await fetchNodeVersions()
      await fetchNodeStatus()
    } catch {
      toast.error(t('packages.nodeVersionRemoveFailed'))
    } finally {
      setNodeRemoving(null)
    }
  }, [fetchNodeVersions, fetchNodeStatus, t])

  // ---------------------------------------------------------------------------
  // System updates
  // ---------------------------------------------------------------------------

  const handleCheckUpdates = useCallback(async () => {
    setLoadingKey('updates', true)
    try {
      const data = await api.checkUpdates()
      setUpdates(data.updates || [])
      setLastChecked(data.last_checked || new Date().toISOString())
      setSelectedPackages(new Set())
      if ((data.updates || []).length === 0) {
        toast.success(t('packages.noUpdatesAvailable'))
      } else {
        toast.info(t('packages.updatesFound', { count: data.updates.length }))
      }
    } catch {
      toast.error(t('packages.checkUpdatesFailed'))
    } finally {
      setLoadingKey('updates', false)
    }
  }, [setLoadingKey, t])

  const handleUpgradePackages = useCallback(
    async (packages?: string[]) => {
      const label = packages
        ? t('packages.upgradingSelected', { count: packages.length })
        : t('packages.upgradingAll')
      setLoadingKey('upgrade', true)
      openOutput(label)
      try {
        appendOutput(label + '...\n')
        const result = (await api.upgradePackages(packages)) as { output?: string }
        if (result?.output) {
          appendOutput(result.output)
        }
        appendOutput('\n' + t('packages.upgradeComplete') + '\n')
        finishOutput()
        toast.success(t('packages.upgradeComplete'))
        setSelectedPackages(new Set())
        await handleCheckUpdates()
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : t('packages.upgradeFailed')
        appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
        finishOutput()
        toast.error(message)
      } finally {
        setLoadingKey('upgrade', false)
      }
    },
    [setLoadingKey, openOutput, appendOutput, finishOutput, handleCheckUpdates, t],
  )

  // ---------------------------------------------------------------------------
  // Package search & install/remove
  // ---------------------------------------------------------------------------

  const handleSearch = useCallback(async () => {
    if (!searchQuery.trim()) return
    setLoadingKey('search', true)
    setHasSearched(true)
    try {
      const data = await api.searchPackages(searchQuery.trim())
      setSearchResults(data.packages || [])
      if ((data.packages || []).length === 0) {
        toast.info(t('packages.noSearchResults'))
      }
    } catch {
      toast.error(t('packages.searchFailed'))
    } finally {
      setLoadingKey('search', false)
    }
  }, [searchQuery, setLoadingKey, t])

  const handleInstallPackage = useCallback(
    async (name: string) => {
      setLoadingKey('install', name)
      openOutput(t('packages.installingPackage', { name }))
      try {
        appendOutput(t('packages.installStarted', { name }) + '\n')
        const result = (await api.installPackage(name)) as { output?: string }
        if (result?.output) {
          appendOutput(result.output)
        }
        appendOutput('\n' + t('packages.installSuccess', { name }) + '\n')
        finishOutput()
        toast.success(t('packages.installSuccess', { name }))
        // Update search results to reflect installed status
        setSearchResults((prev) =>
          prev.map((pkg) => (pkg.name === name ? { ...pkg, installed: true } : pkg)),
        )
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : t('packages.installFailed', { name })
        appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
        finishOutput()
        toast.error(message)
      } finally {
        setLoadingKey('install', null)
      }
    },
    [setLoadingKey, openOutput, appendOutput, finishOutput, t],
  )

  const handleRemovePackage = useCallback(
    async (name: string) => {
      setLoadingKey('remove', name)
      openOutput(t('packages.removingPackage', { name }))
      try {
        appendOutput(t('packages.removeStarted', { name }) + '\n')
        const result = (await api.removePackage(name)) as { output?: string }
        if (result?.output) {
          appendOutput(result.output)
        }
        appendOutput('\n' + t('packages.removeSuccess', { name }) + '\n')
        finishOutput()
        toast.success(t('packages.removeSuccess', { name }))
        // Update search results to reflect removed status
        setSearchResults((prev) =>
          prev.map((pkg) => (pkg.name === name ? { ...pkg, installed: false } : pkg)),
        )
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : t('packages.removeFailed', { name })
        appendOutput('\n' + t('packages.error') + ': ' + message + '\n')
        finishOutput()
        toast.error(message)
      } finally {
        setLoadingKey('remove', null)
      }
    },
    [setLoadingKey, openOutput, appendOutput, finishOutput, t],
  )

  // ---------------------------------------------------------------------------
  // Selection helpers
  // ---------------------------------------------------------------------------

  const togglePackageSelection = useCallback((name: string) => {
    setSelectedPackages((prev) => {
      const next = new Set(prev)
      if (next.has(name)) {
        next.delete(name)
      } else {
        next.add(name)
      }
      return next
    })
  }, [])

  const toggleSelectAll = useCallback(() => {
    setSelectedPackages((prev) => {
      if (prev.size === updates.length) {
        return new Set()
      }
      return new Set(updates.map((p) => p.name))
    })
  }, [updates])

  // ---------------------------------------------------------------------------
  // Initial load
  // ---------------------------------------------------------------------------

  useEffect(() => {
    fetchDockerStatus()
    fetchNodeStatus()
    fetchClaudeStatus()
    fetchCodexStatus()
    fetchGeminiStatus()
  }, [fetchDockerStatus, fetchNodeStatus, fetchClaudeStatus, fetchCodexStatus, fetchGeminiStatus])

  // ---------------------------------------------------------------------------
  // JSX
  // ---------------------------------------------------------------------------

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('packages.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('packages.subtitle')}</p>
      </div>

      {/* ------------------------------------------------------------------ */}
      {/* Docker Status Card                                                  */}
      {/* ------------------------------------------------------------------ */}
      <div className="bg-card rounded-2xl card-shadow">
        <div className="px-6 pt-5 pb-4">
          <h3 className="text-[15px] font-semibold flex items-center gap-2">
            <Server className="h-4 w-4" aria-hidden="true" />
            {t('packages.dockerStatus')}
          </h3>
          <p className="text-[13px] text-muted-foreground mt-1">
            {t('packages.dockerDescription')}
          </p>
        </div>
        <div className="px-6 pb-5">
          {loading.docker ? (
            <div className="flex items-center gap-2 text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
              <span className="text-[13px]">{t('packages.checkingDocker')}</span>
            </div>
          ) : dockerStatus === null ? (
            <div className="flex items-center gap-2 text-muted-foreground">
              <AlertCircle className="h-4 w-4" aria-hidden="true" />
              <span className="text-[13px]">{t('packages.dockerStatusUnavailable')}</span>
            </div>
          ) : !dockerStatus.installed ? (
            <div className="space-y-4">
              <div className="flex items-center gap-2 text-destructive">
                <X className="h-5 w-5" aria-hidden="true" />
                <span className="text-[13px] font-medium">
                  {t('packages.dockerNotInstalled')}
                </span>
              </div>
              <p className="text-[13px] text-muted-foreground">
                {t('packages.dockerNotInstalledHint')}
              </p>
              <Button
                size="lg"
                className="rounded-xl"
                onClick={handleInstallDocker}
                disabled={loading.dockerInstall}
              >
                {loading.dockerInstall ? (
                  <>
                    <Loader2 className="animate-spin" aria-hidden="true" />
                    {t('packages.installingDocker')}
                  </>
                ) : (
                  <>
                    <Download aria-hidden="true" />
                    {t('packages.installDocker')}
                  </>
                )}
              </Button>
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">
                  {t('packages.dockerInstalled')}
                </p>
                <div className="flex items-center gap-1.5">
                  <CheckCircle2 className="h-4 w-4 text-[#00c471]" aria-hidden="true" />
                  <span className="text-[13px] font-medium">{t('packages.yes')}</span>
                </div>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">
                  {t('packages.dockerVersion')}
                </p>
                <p className="text-[13px] font-medium font-mono">
                  {dockerStatus.version || t('packages.unknown')}
                </p>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">
                  {t('packages.dockerRunning')}
                </p>
                <div className="flex items-center gap-1.5">
                  {dockerStatus.running ? (
                    <>
                      <div className="h-2 w-2 rounded-full bg-[#00c471]" />
                      <span className="text-[13px] font-medium">{t('packages.running')}</span>
                    </>
                  ) : (
                    <>
                      <div className="h-2 w-2 rounded-full bg-[#f04452]" />
                      <span className="text-[13px] font-medium">{t('packages.stopped')}</span>
                    </>
                  )}
                </div>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">
                  {t('packages.dockerCompose')}
                </p>
                <div className="flex items-center gap-1.5">
                  {dockerStatus.compose_available ? (
                    <>
                      <Check className="h-4 w-4 text-[#00c471]" aria-hidden="true" />
                      <span className="text-[13px] font-medium">{t('packages.available')}</span>
                    </>
                  ) : (
                    <>
                      <X className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
                      <span className="text-[13px] font-medium">{t('packages.notAvailable')}</span>
                    </>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* ------------------------------------------------------------------ */}
      {/* Dev Tools Card                                                      */}
      {/* ------------------------------------------------------------------ */}
      <div className="bg-card rounded-2xl card-shadow">
        <div className="px-6 pt-5 pb-4">
          <h3 className="text-[15px] font-semibold flex items-center gap-2">
            <Package className="h-4 w-4" aria-hidden="true" />
            {t('packages.devTools')}
          </h3>
          <p className="text-[13px] text-muted-foreground mt-1">
            {t('packages.devToolsDescription')}
          </p>
        </div>
        <div className="px-6 pb-5 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {/* Node.js */}
          <div className="border rounded-xl p-4 space-y-3">
            <div className="flex items-center gap-2">
              <div className="h-8 w-8 rounded-lg bg-[#68a063]/10 flex items-center justify-center">
                <span className="text-[14px] font-bold text-[#68a063]">N</span>
              </div>
              <div>
                <p className="text-[13px] font-semibold">Node.js</p>
                <p className="text-[11px] text-muted-foreground">NVM + LTS</p>
              </div>
            </div>
            {loading.node ? (
              <div className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
                <span className="text-[12px]">{t('packages.checking')}</span>
              </div>
            ) : nodeStatus?.installed ? (
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <div className="space-y-1">
                    <div className="flex items-center gap-1.5">
                      <CheckCircle2 className="h-3.5 w-3.5 text-[#00c471]" aria-hidden="true" />
                      <span className="text-[12px] font-medium font-mono">{nodeStatus.version}</span>
                    </div>
                    {(nodeStatus as DevToolStatus & { npm_version?: string }).npm_version && (
                      <p className="text-[11px] text-muted-foreground">npm {(nodeStatus as DevToolStatus & { npm_version?: string }).npm_version}</p>
                    )}
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 p-0"
                    onClick={openNodeVersionDialog}
                    title={t('packages.nodeVersionManage')}
                  >
                    <Settings2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </div>
            ) : (
              <Button
                size="sm"
                className="rounded-xl w-full"
                onClick={handleInstallNode}
                disabled={loading.nodeInstall}
              >
                {loading.nodeInstall ? (
                  <>
                    <Loader2 className="animate-spin" aria-hidden="true" />
                    {t('packages.installing')}
                  </>
                ) : (
                  <>
                    <Download aria-hidden="true" />
                    {t('packages.installNode')}
                  </>
                )}
              </Button>
            )}
          </div>

          {/* Claude Code */}
          <div className="border rounded-xl p-4 space-y-3">
            <div className="flex items-center gap-2">
              <div className="h-8 w-8 rounded-lg bg-[#d97757]/10 flex items-center justify-center">
                <span className="text-[14px] font-bold text-[#d97757]">C</span>
              </div>
              <div>
                <p className="text-[13px] font-semibold">Claude Code</p>
                <p className="text-[11px] text-muted-foreground">Anthropic CLI</p>
              </div>
            </div>
            {loading.claude ? (
              <div className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
                <span className="text-[12px]">{t('packages.checking')}</span>
              </div>
            ) : claudeStatus?.installed ? (
              <div className="flex items-center gap-1.5">
                <CheckCircle2 className="h-3.5 w-3.5 text-[#00c471]" aria-hidden="true" />
                <span className="text-[12px] font-medium font-mono">{claudeStatus.version || t('packages.installed')}</span>
              </div>
            ) : (
              <Button
                size="sm"
                className="rounded-xl w-full"
                onClick={handleInstallClaude}
                disabled={loading.claudeInstall}
              >
                {loading.claudeInstall ? (
                  <>
                    <Loader2 className="animate-spin" aria-hidden="true" />
                    {t('packages.installing')}
                  </>
                ) : (
                  <>
                    <Download aria-hidden="true" />
                    {t('packages.installClaude')}
                  </>
                )}
              </Button>
            )}
          </div>

          {/* Codex */}
          <div className="border rounded-xl p-4 space-y-3">
            <div className="flex items-center gap-2">
              <div className="h-8 w-8 rounded-lg bg-[#10a37f]/10 flex items-center justify-center">
                <span className="text-[14px] font-bold text-[#10a37f]">X</span>
              </div>
              <div>
                <p className="text-[13px] font-semibold">Codex</p>
                <p className="text-[11px] text-muted-foreground">OpenAI CLI</p>
              </div>
            </div>
            {loading.codex ? (
              <div className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
                <span className="text-[12px]">{t('packages.checking')}</span>
              </div>
            ) : codexStatus?.installed ? (
              <div className="flex items-center gap-1.5">
                <CheckCircle2 className="h-3.5 w-3.5 text-[#00c471]" aria-hidden="true" />
                <span className="text-[12px] font-medium font-mono">{codexStatus.version || t('packages.installed')}</span>
              </div>
            ) : (
              <Button
                size="sm"
                className="rounded-xl w-full"
                onClick={handleInstallCodex}
                disabled={loading.codexInstall || !nodeStatus?.installed}
                title={!nodeStatus?.installed ? t('packages.nodeRequired') : ''}
              >
                {loading.codexInstall ? (
                  <>
                    <Loader2 className="animate-spin" aria-hidden="true" />
                    {t('packages.installing')}
                  </>
                ) : (
                  <>
                    <Download aria-hidden="true" />
                    {t('packages.installCodex')}
                  </>
                )}
              </Button>
            )}
            {!nodeStatus?.installed && !codexStatus?.installed && (
              <p className="text-[11px] text-muted-foreground">{t('packages.nodeRequired')}</p>
            )}
          </div>

          {/* Gemini CLI */}
          <div className="border rounded-xl p-4 space-y-3">
            <div className="flex items-center gap-2">
              <div className="h-8 w-8 rounded-lg bg-[#4285f4]/10 flex items-center justify-center">
                <span className="text-[14px] font-bold text-[#4285f4]">G</span>
              </div>
              <div>
                <p className="text-[13px] font-semibold">Gemini CLI</p>
                <p className="text-[11px] text-muted-foreground">Google CLI</p>
              </div>
            </div>
            {loading.gemini ? (
              <div className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
                <span className="text-[12px]">{t('packages.checking')}</span>
              </div>
            ) : geminiStatus?.installed ? (
              <div className="flex items-center gap-1.5">
                <CheckCircle2 className="h-3.5 w-3.5 text-[#00c471]" aria-hidden="true" />
                <span className="text-[12px] font-medium font-mono">{geminiStatus.version || t('packages.installed')}</span>
              </div>
            ) : (
              <Button
                size="sm"
                className="rounded-xl w-full"
                onClick={handleInstallGemini}
                disabled={loading.geminiInstall || !nodeStatus?.installed}
                title={!nodeStatus?.installed ? t('packages.nodeRequired') : ''}
              >
                {loading.geminiInstall ? (
                  <>
                    <Loader2 className="animate-spin" aria-hidden="true" />
                    {t('packages.installing')}
                  </>
                ) : (
                  <>
                    <Download aria-hidden="true" />
                    {t('packages.installGemini')}
                  </>
                )}
              </Button>
            )}
            {!nodeStatus?.installed && !geminiStatus?.installed && (
              <p className="text-[11px] text-muted-foreground">{t('packages.nodeRequired')}</p>
            )}
          </div>
        </div>
      </div>

      {/* ------------------------------------------------------------------ */}
      {/* System Updates Card                                                 */}
      {/* ------------------------------------------------------------------ */}
      <div className="bg-card rounded-2xl card-shadow">
        <div className="px-6 pt-5 pb-4">
          <h3 className="text-[15px] font-semibold flex items-center gap-2">
            <RefreshCw className="h-4 w-4" aria-hidden="true" />
            {t('packages.systemUpdates')}
          </h3>
          <p className="text-[13px] text-muted-foreground mt-1">
            {lastChecked
              ? t('packages.lastChecked', {
                  time: new Date(lastChecked).toLocaleString(),
                })
              : t('packages.neverChecked')}
          </p>
        </div>
        <div className="px-6 pb-5 space-y-4">
          {/* Action buttons */}
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              className="rounded-xl"
              onClick={handleCheckUpdates}
              disabled={loading.updates || loading.upgrade}
            >
              {loading.updates ? (
                <>
                  <Loader2 className="animate-spin" aria-hidden="true" />
                  {t('packages.checking')}
                </>
              ) : (
                <>
                  <RefreshCw aria-hidden="true" />
                  {t('packages.checkForUpdates')}
                </>
              )}
            </Button>
            <Button
              className="rounded-xl"
              onClick={() => handleUpgradePackages()}
              disabled={updates.length === 0 || loading.upgrade || loading.updates}
            >
              {loading.upgrade ? (
                <>
                  <Loader2 className="animate-spin" aria-hidden="true" />
                  {t('packages.upgrading')}
                </>
              ) : (
                <>
                  <Download aria-hidden="true" />
                  {t('packages.upgradeAll')}
                </>
              )}
            </Button>
            {selectedPackages.size > 0 && (
              <Button
                variant="secondary"
                className="rounded-xl"
                onClick={() => handleUpgradePackages(Array.from(selectedPackages))}
                disabled={loading.upgrade || loading.updates}
              >
                <Download aria-hidden="true" />
                {t('packages.upgradeSelected', { count: selectedPackages.size })}
              </Button>
            )}
            {updates.length > 0 && (
              <span className="text-[13px] text-muted-foreground ml-auto">
                {t('packages.updatesAvailable', { count: updates.length })}
              </span>
            )}
          </div>

          {/* Mobile updates card view */}
          <div className="md:hidden space-y-2">
            {loading.updates ? (
              <div className="flex items-center justify-center gap-2 text-muted-foreground py-8">
                <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
                <span className="text-[13px]">{t('packages.checkingForUpdates')}</span>
              </div>
            ) : updates.length === 0 ? (
              <div className="text-center text-[13px] text-muted-foreground py-8">
                {t('packages.noUpdates')}
              </div>
            ) : (
              updates.map((pkg) => (
                <div key={pkg.name} className="bg-card rounded-2xl p-4 card-shadow">
                  <div className="flex items-start gap-3">
                    <input
                      type="checkbox"
                      className="h-4 w-4 rounded border-gray-300 accent-[#3182f6] mt-0.5 shrink-0"
                      checked={selectedPackages.has(pkg.name)}
                      onChange={() => togglePackageSelection(pkg.name)}
                      aria-label={pkg.name}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-medium font-mono truncate">{pkg.name}</p>
                      <div className="flex items-center gap-2 mt-1 flex-wrap">
                        <span className="text-[11px] text-muted-foreground font-mono">{pkg.current_version}</span>
                        <span className="text-[11px] text-muted-foreground">→</span>
                        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                          {pkg.new_version}
                        </span>
                      </div>
                    </div>
                  </div>
                </div>
              ))
            )}
          </div>

          {/* Desktop updates table */}
          <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-10">
                    <input
                      type="checkbox"
                      className="h-4 w-4 rounded border-gray-300 accent-[#3182f6]"
                      checked={updates.length > 0 && selectedPackages.size === updates.length}
                      onChange={toggleSelectAll}
                      disabled={updates.length === 0}
                      aria-label={t('packages.selectAll')}
                    />
                  </TableHead>
                  <TableHead>{t('packages.packageName')}</TableHead>
                  <TableHead>{t('packages.currentVersion')}</TableHead>
                  <TableHead>{t('packages.newVersion')}</TableHead>
                  <TableHead>{t('packages.architecture')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loading.updates ? (
                  <TableRow>
                    <TableCell colSpan={5} className="text-center py-8">
                      <div className="flex items-center justify-center gap-2 text-muted-foreground">
                        <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
                        <span className="text-[13px]">{t('packages.checkingForUpdates')}</span>
                      </div>
                    </TableCell>
                  </TableRow>
                ) : updates.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={5}
                      className="text-center text-[13px] text-muted-foreground py-8"
                    >
                      {t('packages.noUpdates')}
                    </TableCell>
                  </TableRow>
                ) : (
                  updates.map((pkg) => (
                    <TableRow key={pkg.name}>
                      <TableCell>
                        <input
                          type="checkbox"
                          className="h-4 w-4 rounded border-gray-300 accent-[#3182f6]"
                          checked={selectedPackages.has(pkg.name)}
                          onChange={() => togglePackageSelection(pkg.name)}
                          aria-label={pkg.name}
                        />
                      </TableCell>
                      <TableCell className="font-medium font-mono text-[13px]">
                        {pkg.name}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-[13px] font-mono">
                        {pkg.current_version}
                      </TableCell>
                      <TableCell className="text-[13px] font-mono">
                        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                          {pkg.new_version}
                        </span>
                      </TableCell>
                      <TableCell className="text-muted-foreground text-[13px]">
                        {pkg.arch}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </div>
      </div>

      {/* ------------------------------------------------------------------ */}
      {/* Package Search & Install Card                                       */}
      {/* ------------------------------------------------------------------ */}
      <div className="bg-card rounded-2xl card-shadow">
        <div className="px-6 pt-5 pb-4">
          <h3 className="text-[15px] font-semibold flex items-center gap-2">
            <Package className="h-4 w-4" aria-hidden="true" />
            {t('packages.searchAndInstall')}
          </h3>
          <p className="text-[13px] text-muted-foreground mt-1">
            {t('packages.searchDescription')}
          </p>
        </div>
        <div className="px-6 pb-5 space-y-4">
          {/* Search bar */}
          <div className="flex items-center gap-2 max-w-xl">
            <div className="relative flex-1">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" aria-hidden="true" />
              <Input
                className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
                placeholder={t('packages.searchPlaceholder')}
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !loading.search) handleSearch()
                }}
                disabled={loading.search}
              />
            </div>
            <Button
              className="rounded-xl"
              onClick={handleSearch}
              disabled={loading.search || !searchQuery.trim()}
            >
              {loading.search ? (
                <Loader2 className="animate-spin" aria-hidden="true" />
              ) : (
                <Search aria-hidden="true" />
              )}
              {t('packages.search')}
            </Button>
          </div>

          {/* Search results */}
          {searchResults.length > 0 && (
            <>
              {/* Mobile search results */}
              <div className="md:hidden space-y-2">
                {searchResults.map((pkg) => (
                  <div key={pkg.name} className="bg-card rounded-2xl p-4 card-shadow">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2 flex-wrap">
                          <p className="text-[13px] font-medium font-mono">{pkg.name}</p>
                          {pkg.installed && (
                            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                              {t('packages.installed')}
                            </span>
                          )}
                        </div>
                        {pkg.description && (
                          <p className="text-[11px] text-muted-foreground mt-0.5 line-clamp-2">{pkg.description}</p>
                        )}
                      </div>
                      <div className="shrink-0">
                        {!pkg.installed ? (
                          <Button
                            size="sm"
                            className="rounded-xl"
                            onClick={() => handleInstallPackage(pkg.name)}
                            disabled={loading.install === pkg.name}
                          >
                            {loading.install === pkg.name ? (
                              <Loader2 className="animate-spin h-4 w-4" aria-hidden="true" />
                            ) : (
                              <Download className="h-4 w-4" aria-hidden="true" />
                            )}
                          </Button>
                        ) : (
                          <Button
                            size="sm"
                            variant="destructive"
                            className="rounded-xl"
                            onClick={() => handleRemovePackage(pkg.name)}
                            disabled={loading.remove === pkg.name}
                          >
                            {loading.remove === pkg.name ? (
                              <Loader2 className="animate-spin h-4 w-4" aria-hidden="true" />
                            ) : (
                              <Trash2 className="h-4 w-4" aria-hidden="true" />
                            )}
                          </Button>
                        )}
                      </div>
                    </div>
                  </div>
                ))}
              </div>

              {/* Desktop search results */}
              <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('packages.packageName')}</TableHead>
                      <TableHead>{t('packages.description')}</TableHead>
                      <TableHead className="text-right">{t('packages.actions')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {searchResults.map((pkg) => (
                      <TableRow key={pkg.name}>
                        <TableCell className="font-medium font-mono text-[13px]">
                          <div className="flex items-center gap-2">
                            {pkg.name}
                            {pkg.installed && (
                              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                                {t('packages.installed')}
                              </span>
                            )}
                          </div>
                        </TableCell>
                        <TableCell className="text-muted-foreground text-[13px] max-w-md truncate">
                          {pkg.description}
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex items-center justify-end gap-1">
                            {!pkg.installed ? (
                              <Button
                                size="sm"
                                className="rounded-xl"
                                onClick={() => handleInstallPackage(pkg.name)}
                                disabled={loading.install === pkg.name}
                              >
                                {loading.install === pkg.name ? (
                                  <>
                                    <Loader2 className="animate-spin" aria-hidden="true" />
                                    {t('packages.installing')}
                                  </>
                                ) : (
                                  <>
                                    <Download aria-hidden="true" />
                                    {t('packages.install')}
                                  </>
                                )}
                              </Button>
                            ) : (
                              <Button
                                size="sm"
                                variant="destructive"
                                className="rounded-xl"
                                onClick={() => handleRemovePackage(pkg.name)}
                                disabled={loading.remove === pkg.name}
                              >
                                {loading.remove === pkg.name ? (
                                  <>
                                    <Loader2 className="animate-spin" aria-hidden="true" />
                                    {t('packages.removing')}
                                  </>
                                ) : (
                                  <>
                                    <Trash2 aria-hidden="true" />
                                    {t('packages.remove')}
                                  </>
                                )}
                              </Button>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </>
          )}

          {/* Empty search state */}
          {searchResults.length === 0 && !loading.search && hasSearched && (
            <div className="text-center text-[13px] text-muted-foreground py-6">
              {t('packages.noSearchResults')}
            </div>
          )}
        </div>
      </div>

      {/* ------------------------------------------------------------------ */}
      {/* Node.js Version Management Dialog                                   */}
      {/* ------------------------------------------------------------------ */}
      <Dialog open={nodeVersionDialog} onOpenChange={setNodeVersionDialog}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('packages.nodeVersionManage')}</DialogTitle>
            <DialogDescription>{t('packages.nodeVersionDescription')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 max-h-96 overflow-y-auto">
            {nodeVersionLoading ? (
              <div className="flex items-center justify-center py-6 gap-2 text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                <span className="text-[13px]">{t('packages.checking')}</span>
              </div>
            ) : (
              <>
                {/* Installed versions */}
                <div className="space-y-2">
                  <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('packages.nodeInstalledVersions')}</p>
                  {nodeVersions.length === 0 && !(nodeStatus as DevToolStatus & { nvm_installed?: boolean })?.nvm_installed ? (
                    <div className="space-y-2">
                      <p className="text-[13px] text-muted-foreground">{t('packages.nodeNvmNotInstalled')}</p>
                      <Button
                        size="sm"
                        className="rounded-xl"
                        onClick={handleInstallNode}
                        disabled={loading.nodeInstall}
                      >
                        {loading.nodeInstall ? (
                          <>
                            <Loader2 className="animate-spin h-3 w-3" />
                            {t('packages.installing')}
                          </>
                        ) : (
                          <>
                            <Download className="h-3 w-3" />
                            {t('packages.nodeInstallNvm')}
                          </>
                        )}
                      </Button>
                    </div>
                  ) : nodeVersions.length === 0 ? (
                    <div className="space-y-2">
                      {nodeStatus?.installed && (
                        <div className="flex items-center justify-between rounded-xl border px-3 py-2">
                          <div className="flex items-center gap-2">
                            <span className="text-[13px] font-mono font-medium">{nodeStatus.version}</span>
                            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                              {t('packages.nodeActive')}
                            </span>
                          </div>
                        </div>
                      )}
                      <p className="text-[12px] text-muted-foreground">{t('packages.nodeNoOtherVersions')}</p>
                    </div>
                  ) : (
                    <div className="space-y-1.5">
                      {nodeVersions.map((v) => (
                        <div key={v.version} className="flex items-center justify-between rounded-xl border px-3 py-2">
                          <div className="flex items-center gap-2">
                            <span className="text-[13px] font-mono font-medium">{v.version}</span>
                            {v.active && (
                              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                                {t('packages.nodeActive')}
                              </span>
                            )}
                            {v.lts && (
                              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#3182f6]/10 text-[#3182f6]">
                                LTS
                              </span>
                            )}
                          </div>
                          <div className="flex items-center gap-1">
                            {!v.active && (
                              <>
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  className="h-7 text-[12px] rounded-lg"
                                  onClick={() => handleSwitchNodeVersion(v.version)}
                                  disabled={nodeSwitching !== null}
                                >
                                  {nodeSwitching === v.version ? (
                                    <Loader2 className="h-3 w-3 animate-spin" />
                                  ) : (
                                    t('packages.nodeUse')
                                  )}
                                </Button>
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                                  onClick={() => handleUninstallNodeVersion(v.version)}
                                  disabled={nodeRemoving !== null}
                                >
                                  {nodeRemoving === v.version ? (
                                    <Loader2 className="h-3 w-3 animate-spin" />
                                  ) : (
                                    <Trash2 className="h-3.5 w-3.5" />
                                  )}
                                </Button>
                              </>
                            )}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>

                {/* Available LTS versions to install */}
                {remoteLTS.length > 0 && (
                  <div className="space-y-2">
                    <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('packages.nodeAvailableLTS')}</p>
                    <div className="space-y-1.5">
                      {remoteLTS
                        .filter((v) => !nodeVersions.some((nv) => nv.version === v))
                        .map((v) => (
                          <div key={v} className="flex items-center justify-between rounded-xl border border-dashed px-3 py-2">
                            <span className="text-[13px] font-mono text-muted-foreground">{v}</span>
                            <Button
                              variant="outline"
                              size="sm"
                              className="h-7 text-[12px] rounded-lg"
                              onClick={() => handleInstallNodeVersion(v)}
                              disabled={nodeInstallingVersion !== null}
                            >
                              {nodeInstallingVersion === v ? (
                                <Loader2 className="h-3 w-3 animate-spin" />
                              ) : (
                                <>
                                  <Download className="h-3 w-3" />
                                  {t('packages.install')}
                                </>
                              )}
                            </Button>
                          </div>
                        ))}
                    </div>
                  </div>
                )}
              </>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={() => setNodeVersionDialog(false)}>
              {t('packages.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ------------------------------------------------------------------ */}
      {/* Operation Output Dialog                                             */}
      {/* ------------------------------------------------------------------ */}
      <Dialog
        open={outputDialog.open}
        onOpenChange={(open) => {
          if (!open && outputDialog.done) {
            setOutputDialog({ open: false, title: '', output: '', done: false })
          }
        }}
      >
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {!outputDialog.done && (
                <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
              )}
              {outputDialog.done && (
                <CheckCircle2 className="h-4 w-4 text-[#00c471]" aria-hidden="true" />
              )}
              {outputDialog.title}
            </DialogTitle>
            <DialogDescription>
              {outputDialog.done
                ? t('packages.operationComplete')
                : t('packages.operationRunning')}
            </DialogDescription>
          </DialogHeader>
          <div
            ref={outputRef}
            className="bg-zinc-950 text-zinc-100 rounded-xl p-4 max-h-96 overflow-y-auto"
          >
            <pre className="text-xs font-mono whitespace-pre-wrap break-words">
              {outputDialog.output || t('packages.waitingForOutput')}
            </pre>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              className="rounded-xl"
              onClick={() =>
                setOutputDialog({ open: false, title: '', output: '', done: false })
              }
              disabled={!outputDialog.done}
            >
              {outputDialog.done ? t('packages.close') : t('packages.pleaseWait')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
