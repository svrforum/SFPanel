import { useState, useEffect, useCallback } from 'react'
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

interface PackageInfo {
  name: string
  current_version: string
  new_version: string
  arch: string
}

interface SearchResult {
  name: string
  description: string
  version?: string
  installed?: boolean
}

interface DockerStatus {
  installed: boolean
  version: string
  running: boolean
  compose_available: boolean
}

interface LoadingState {
  docker: boolean
  dockerInstall: boolean
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

  // System updates
  const [updates, setUpdates] = useState<PackageInfo[]>([])
  const [lastChecked, setLastChecked] = useState<string | null>(null)
  const [selectedPackages, setSelectedPackages] = useState<Set<string>>(new Set())

  // Search
  const [searchQuery, setSearchQuery] = useState('')
  const [searchResults, setSearchResults] = useState<SearchResult[]>([])

  // Loading
  const [loading, setLoading] = useState<LoadingState>({
    docker: false,
    dockerInstall: false,
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
    setOutputDialog((prev) => ({ ...prev, output: prev.output + text + '\n' }))
  }, [])

  const finishOutput = useCallback(() => {
    setOutputDialog((prev) => ({ ...prev, done: true }))
  }, [])

  // ---------------------------------------------------------------------------
  // Docker
  // ---------------------------------------------------------------------------

  const fetchDockerStatus = useCallback(async () => {
    setLoadingKey('docker', true)
    try {
      const data = await api.getDockerStatus()
      setDockerStatus(data as DockerStatus)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('packages.dockerStatusFailed')
      toast.error(message)
    } finally {
      setLoadingKey('docker', false)
    }
  }, [setLoadingKey, t])

  const handleInstallDocker = useCallback(async () => {
    setLoadingKey('dockerInstall', true)
    openOutput(t('packages.installingDocker'))
    try {
      const token = api.getToken()
      const res = await fetch('/api/v1/packages/install-docker', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${token}`,
        },
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
      appendOutput('\n' + t('packages.error') + ': ' + message)
      finishOutput()
      toast.error(message)
    } finally {
      setLoadingKey('dockerInstall', false)
    }
  }, [setLoadingKey, openOutput, appendOutput, finishOutput, fetchDockerStatus, t])

  // ---------------------------------------------------------------------------
  // System updates
  // ---------------------------------------------------------------------------

  const handleCheckUpdates = useCallback(async () => {
    setLoadingKey('updates', true)
    try {
      const data = await api.checkUpdates() as {
        updates: PackageInfo[]
        total: number
        last_checked: string
      }
      setUpdates(data.updates || [])
      setLastChecked(data.last_checked || new Date().toISOString())
      setSelectedPackages(new Set())
      if ((data.updates || []).length === 0) {
        toast.success(t('packages.noUpdatesAvailable'))
      } else {
        toast.info(t('packages.updatesFound', { count: data.updates.length }))
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('packages.checkUpdatesFailed')
      toast.error(message)
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
        const result = await api.upgradePackages(packages) as { output?: string }
        if (result?.output) {
          appendOutput(result.output)
        }
        appendOutput('\n' + t('packages.upgradeComplete'))
        finishOutput()
        toast.success(t('packages.upgradeComplete'))
        setSelectedPackages(new Set())
        // Refresh update list
        await handleCheckUpdates()
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : t('packages.upgradeFailed')
        appendOutput('\n' + t('packages.error') + ': ' + message)
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
    try {
      const data = await api.searchPackages(searchQuery.trim()) as {
        packages: SearchResult[]
        total: number
        query: string
      }
      setSearchResults(data.packages || [])
      if ((data.packages || []).length === 0) {
        toast.info(t('packages.noSearchResults'))
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('packages.searchFailed')
      toast.error(message)
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
        const result = await api.installPackage(name) as { output?: string }
        if (result?.output) {
          appendOutput(result.output)
        }
        appendOutput('\n' + t('packages.installSuccess', { name }))
        finishOutput()
        toast.success(t('packages.installSuccess', { name }))
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : t('packages.installFailed', { name })
        appendOutput('\n' + t('packages.error') + ': ' + message)
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
        const result = await api.removePackage(name) as { output?: string }
        if (result?.output) {
          appendOutput(result.output)
        }
        appendOutput('\n' + t('packages.removeSuccess', { name }))
        finishOutput()
        toast.success(t('packages.removeSuccess', { name }))
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : t('packages.removeFailed', { name })
        appendOutput('\n' + t('packages.error') + ': ' + message)
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
  }, [fetchDockerStatus])

  // ---------------------------------------------------------------------------
  // Render helpers
  // ---------------------------------------------------------------------------

  function formatTimestamp(iso: string): string {
    try {
      return new Date(iso).toLocaleString()
    } catch {
      return iso
    }
  }

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
            <Server className="h-4 w-4" />
            {t('packages.dockerStatus')}
          </h3>
          <p className="text-[13px] text-muted-foreground mt-1">{t('packages.dockerDescription')}</p>
        </div>
        <div className="px-6 pb-5">
          {loading.docker ? (
            <div className="flex items-center gap-2 text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" />
              {t('packages.checkingDocker')}
            </div>
          ) : dockerStatus === null ? (
            <div className="flex items-center gap-2 text-muted-foreground">
              <AlertCircle className="h-4 w-4" />
              {t('packages.dockerStatusUnavailable')}
            </div>
          ) : !dockerStatus.installed ? (
            /* Docker not installed */
            <div className="space-y-4">
              <div className="flex items-center gap-2 text-destructive">
                <X className="h-5 w-5" />
                <span className="font-medium">{t('packages.dockerNotInstalled')}</span>
              </div>
              <p className="text-sm text-muted-foreground">
                {t('packages.dockerNotInstalledHint')}
              </p>
              <Button
                size="lg"
                onClick={handleInstallDocker}
                disabled={loading.dockerInstall}
              >
                {loading.dockerInstall ? (
                  <>
                    <Loader2 className="animate-spin" />
                    {t('packages.installingDocker')}
                  </>
                ) : (
                  <>
                    <Download />
                    {t('packages.installDocker')}
                  </>
                )}
              </Button>
            </div>
          ) : (
            /* Docker installed */
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">{t('packages.dockerInstalled')}</p>
                <div className="flex items-center gap-1.5">
                  <CheckCircle2 className="h-4 w-4 text-green-500" />
                  <span className="text-sm font-medium">{t('packages.yes')}</span>
                </div>
              </div>
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">{t('packages.dockerVersion')}</p>
                <p className="text-sm font-medium font-mono">
                  {dockerStatus.version || t('packages.unknown')}
                </p>
              </div>
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">{t('packages.dockerRunning')}</p>
                <div className="flex items-center gap-1.5">
                  {dockerStatus.running ? (
                    <>
                      <div className="h-2 w-2 rounded-full bg-green-500" />
                      <span className="text-sm font-medium">{t('packages.running')}</span>
                    </>
                  ) : (
                    <>
                      <div className="h-2 w-2 rounded-full bg-red-500" />
                      <span className="text-sm font-medium">{t('packages.stopped')}</span>
                    </>
                  )}
                </div>
              </div>
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">{t('packages.dockerCompose')}</p>
                <div className="flex items-center gap-1.5">
                  {dockerStatus.compose_available ? (
                    <>
                      <Check className="h-4 w-4 text-green-500" />
                      <span className="text-sm font-medium">{t('packages.available')}</span>
                    </>
                  ) : (
                    <>
                      <X className="h-4 w-4 text-muted-foreground" />
                      <span className="text-sm font-medium">{t('packages.notAvailable')}</span>
                    </>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* ------------------------------------------------------------------ */}
      {/* System Updates Card                                                 */}
      {/* ------------------------------------------------------------------ */}
      <div className="bg-card rounded-2xl card-shadow">
        <div className="px-6 pt-5 pb-4">
          <h3 className="text-[15px] font-semibold flex items-center gap-2">
            <RefreshCw className="h-4 w-4" />
            {t('packages.systemUpdates')}
          </h3>
          <p className="text-[13px] text-muted-foreground mt-1">
            {lastChecked
              ? t('packages.lastChecked', { time: formatTimestamp(lastChecked) })
              : t('packages.neverChecked')}
          </p>
        </div>
        <div className="px-6 pb-5 space-y-4">
          {/* Action buttons */}
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              onClick={handleCheckUpdates}
              disabled={loading.updates || loading.upgrade}
            >
              {loading.updates ? (
                <>
                  <Loader2 className="animate-spin" />
                  {t('packages.checking')}
                </>
              ) : (
                <>
                  <RefreshCw />
                  {t('packages.checkForUpdates')}
                </>
              )}
            </Button>
            <Button
              onClick={() => handleUpgradePackages()}
              disabled={updates.length === 0 || loading.upgrade || loading.updates}
            >
              {loading.upgrade ? (
                <>
                  <Loader2 className="animate-spin" />
                  {t('packages.upgrading')}
                </>
              ) : (
                <>
                  <Download />
                  {t('packages.upgradeAll')}
                </>
              )}
            </Button>
            {selectedPackages.size > 0 && (
              <Button
                variant="secondary"
                onClick={() => handleUpgradePackages(Array.from(selectedPackages))}
                disabled={loading.upgrade || loading.updates}
              >
                <Download />
                {t('packages.upgradeSelected', { count: selectedPackages.size })}
              </Button>
            )}
            {updates.length > 0 && (
              <span className="text-sm text-muted-foreground ml-auto">
                {t('packages.updatesAvailable', { count: updates.length })}
              </span>
            )}
          </div>

          {/* Updates table */}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-gray-300"
                    checked={updates.length > 0 && selectedPackages.size === updates.length}
                    onChange={toggleSelectAll}
                    disabled={updates.length === 0}
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
                      <Loader2 className="h-4 w-4 animate-spin" />
                      {t('packages.checkingForUpdates')}
                    </div>
                  </TableCell>
                </TableRow>
              ) : updates.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                    {t('packages.noUpdates')}
                  </TableCell>
                </TableRow>
              ) : (
                updates.map((pkg) => (
                  <TableRow key={pkg.name}>
                    <TableCell>
                      <input
                        type="checkbox"
                        className="h-4 w-4 rounded border-gray-300"
                        checked={selectedPackages.has(pkg.name)}
                        onChange={() => togglePackageSelection(pkg.name)}
                      />
                    </TableCell>
                    <TableCell className="font-medium font-mono text-sm">
                      {pkg.name}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm font-mono">
                      {pkg.current_version}
                    </TableCell>
                    <TableCell className="text-sm font-mono">
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">{pkg.new_version}</span>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {pkg.arch}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* ------------------------------------------------------------------ */}
      {/* Package Search & Install Card                                       */}
      {/* ------------------------------------------------------------------ */}
      <div className="bg-card rounded-2xl card-shadow">
        <div className="px-6 pt-5 pb-4">
          <h3 className="text-[15px] font-semibold flex items-center gap-2">
            <Package className="h-4 w-4" />
            {t('packages.searchAndInstall')}
          </h3>
          <p className="text-[13px] text-muted-foreground mt-1">{t('packages.searchDescription')}</p>
        </div>
        <div className="px-6 pb-5 space-y-4">
          {/* Search bar */}
          <div className="flex items-center gap-2 max-w-xl">
            <Input
              placeholder={t('packages.searchPlaceholder')}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSearch()
              }}
              disabled={loading.search}
            />
            <Button onClick={handleSearch} disabled={loading.search || !searchQuery.trim()}>
              {loading.search ? (
                <Loader2 className="animate-spin" />
              ) : (
                <Search />
              )}
              {t('packages.search')}
            </Button>
          </div>

          {/* Search results */}
          {searchResults.length > 0 && (
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
                    <TableCell className="font-medium font-mono text-sm">
                      <div className="flex items-center gap-2">
                        {pkg.name}
                        {pkg.installed && (
                          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">{t('packages.installed')}</span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm max-w-md truncate">
                      {pkg.description}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        {!pkg.installed ? (
                          <Button
                            size="sm"
                            onClick={() => handleInstallPackage(pkg.name)}
                            disabled={loading.install === pkg.name}
                          >
                            {loading.install === pkg.name ? (
                              <>
                                <Loader2 className="animate-spin" />
                                {t('packages.installing')}
                              </>
                            ) : (
                              <>
                                <Download />
                                {t('packages.install')}
                              </>
                            )}
                          </Button>
                        ) : (
                          <Button
                            size="sm"
                            variant="destructive"
                            onClick={() => handleRemovePackage(pkg.name)}
                            disabled={loading.remove === pkg.name}
                          >
                            {loading.remove === pkg.name ? (
                              <>
                                <Loader2 className="animate-spin" />
                                {t('packages.removing')}
                              </>
                            ) : (
                              <>
                                <Trash2 />
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
          )}

          {/* Empty search state (only after a search has been performed) */}
          {searchResults.length === 0 && !loading.search && searchQuery.trim() !== '' && (
            <div className="text-center text-muted-foreground py-6">
              {t('packages.noSearchResults')}
            </div>
          )}
        </div>
      </div>

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
              {!outputDialog.done && <Loader2 className="h-4 w-4 animate-spin" />}
              {outputDialog.done && <CheckCircle2 className="h-4 w-4 text-green-500" />}
              {outputDialog.title}
            </DialogTitle>
            <DialogDescription>
              {outputDialog.done
                ? t('packages.operationComplete')
                : t('packages.operationRunning')}
            </DialogDescription>
          </DialogHeader>
          <div className="bg-zinc-950 text-zinc-100 rounded-lg p-4 max-h-96 overflow-y-auto">
            <pre className="text-xs font-mono whitespace-pre-wrap break-words">
              {outputDialog.output || t('packages.waitingForOutput')}
            </pre>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
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
