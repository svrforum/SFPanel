import React, { useState, useEffect, useCallback, useRef } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Play, Square, RotateCw, Trash2, RefreshCw, Terminal, Info, Cpu, MemoryStick, Search, ChevronRight, ChevronDown, Network, HardDrive, Variable, Globe, Plus, Layers, Pause, CheckSquare, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import type { Container, ContainerStatsResult } from '@/types/api'
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
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Checkbox } from '@/components/ui/checkbox'
import ContainerLogs from '@/components/ContainerLogs'
import ContainerShell from '@/components/ContainerShell'

function formatPorts(ports: Container['Ports']): string {
  if (!ports || ports.length === 0) return '-'
  return ports
    .filter((p) => p.PublicPort)
    .map((p) => `${p.PublicPort}:${p.PrivatePort}/${p.Type}`)
    .join(', ') || ports.map((p) => `${p.PrivatePort}/${p.Type}`).join(', ')
}

function formatContainerName(names: string[]): string {
  if (!names || names.length === 0) return 'unknown'
  return names[0].replace(/^\//, '')
}

function timeAgo(timestamp: number): string {
  const seconds = Math.floor(Date.now() / 1000 - timestamp)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}


function statusBadge(state: string) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
  switch (state.toLowerCase()) {
    case 'running':
      return <span className={`${base} bg-[#00c471]/10 text-[#00c471]`}>running</span>
    case 'exited':
      return <span className={`${base} bg-[#f04452]/10 text-[#f04452]`}>exited</span>
    case 'created':
      return <span className={`${base} bg-secondary text-muted-foreground`}>created</span>
    case 'paused':
      return <span className={`${base} bg-[#f59e0b]/10 text-[#f59e0b]`}>paused</span>
    default:
      return <span className={`${base} bg-secondary text-muted-foreground`}>{state}</span>
  }
}

// Container stats display in table rows — receives stats from batch polling
function ContainerStatsCell({ stats, state }: { stats?: ContainerStatsResult; state: string }) {
  if (state !== 'running' || !stats) {
    return <span className="text-muted-foreground text-xs">-</span>
  }

  return (
    <div className="flex items-center gap-3 text-xs">
      <span className="flex items-center gap-1">
        <Cpu className="h-3 w-3 text-blue-500" />
        <span className={stats.cpu_percent > 80 ? 'text-red-500 font-medium' : ''}>{stats.cpu_percent.toFixed(1)}%</span>
      </span>
      <span className="flex items-center gap-1">
        <MemoryStick className="h-3 w-3 text-purple-500" />
        <span className={stats.mem_percent > 80 ? 'text-red-500 font-medium' : ''}>{stats.mem_percent.toFixed(1)}%</span>
      </span>
    </div>
  )
}

// Container inspect detail panel
function ContainerInspect({ containerId }: { containerId: string }) {
  const { t } = useTranslation()
  const [data, setData] = useState<any>(null)
  const [loading, setLoading] = useState(true)
  const [stats, setStats] = useState<any>(null)
  const statsInterval = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const inspectData = await api.inspectContainer(containerId)
        if (cancelled) return
        setData(inspectData)
        if (inspectData.state === 'running') {
          const statsData = await api.containerStats(containerId)
          if (cancelled) return
          setStats(statsData)
          statsInterval.current = setInterval(async () => {
            try {
              const s = await api.containerStats(containerId)
              if (!cancelled) setStats(s)
            } catch { /* ignore */ }
          }, 3000)
        }
      } catch (err: any) {
        if (!cancelled) toast.error(err.message || t('docker.containers.fetchFailed'))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => {
      cancelled = true
      if (statsInterval.current) clearInterval(statsInterval.current)
    }
  }, [containerId, t])

  if (loading) {
    return <div className="flex items-center justify-center py-8 text-muted-foreground">{t('common.loading')}</div>
  }

  if (!data) return null

  return (
    <div className="space-y-4 max-h-[500px] overflow-y-auto pr-1">
      {/* Resource Stats */}
      {stats && (
        <div className="grid grid-cols-2 gap-3">
          <div className="bg-secondary/30 rounded-xl py-3 px-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Cpu className="h-4 w-4 text-primary" />
                <span className="text-[13px] font-medium">CPU</span>
              </div>
              <span className="text-lg font-bold">{stats.cpu_percent.toFixed(1)}%</span>
            </div>
            <div className="mt-2 h-1.5 bg-secondary rounded-full overflow-hidden">
              <div
                className="h-full rounded-full transition-all duration-500"
                style={{
                  width: `${Math.min(stats.cpu_percent, 100)}%`,
                  backgroundColor: stats.cpu_percent > 80 ? '#f04452' : stats.cpu_percent > 50 ? '#f59e0b' : '#3182f6'
                }}
              />
            </div>
          </div>
          <div className="bg-secondary/30 rounded-xl py-3 px-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <MemoryStick className="h-4 w-4 text-[#8b5cf6]" />
                <span className="text-[13px] font-medium">{t('docker.containers.memory')}</span>
              </div>
              <span className="text-lg font-bold">{stats.mem_percent.toFixed(1)}%</span>
            </div>
            <div className="flex items-center justify-between text-[11px] text-muted-foreground mt-1">
              <span>{formatBytes(stats.mem_usage)}</span>
              <span>{formatBytes(stats.mem_limit)}</span>
            </div>
            <div className="mt-1 h-1.5 bg-secondary rounded-full overflow-hidden">
              <div
                className="h-full rounded-full transition-all duration-500"
                style={{
                  width: `${Math.min(stats.mem_percent, 100)}%`,
                  backgroundColor: stats.mem_percent > 80 ? '#f04452' : stats.mem_percent > 50 ? '#f59e0b' : '#8b5cf6'
                }}
              />
            </div>
          </div>
        </div>
      )}

      {/* General Info */}
      <div className="space-y-1">
        <h4 className="text-sm font-semibold flex items-center gap-1.5">
          <Info className="h-3.5 w-3.5" />
          {t('docker.containers.generalInfo')}
        </h4>
        <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm bg-muted/30 rounded-lg p-3">
          <div className="text-muted-foreground">{t('docker.containers.image')}</div>
          <div className="font-mono text-xs truncate">{data.image}</div>
          <div className="text-muted-foreground">{t('docker.containers.command')}</div>
          <div className="font-mono text-xs truncate" title={data.cmd || data.entrypoint}>{data.cmd || data.entrypoint || '-'}</div>
          <div className="text-muted-foreground">{t('docker.containers.workingDir')}</div>
          <div className="font-mono text-xs">{data.working_dir || '/'}</div>
          <div className="text-muted-foreground">{t('docker.containers.hostname')}</div>
          <div className="font-mono text-xs">{data.hostname}</div>
          <div className="text-muted-foreground">{t('docker.containers.startedAt')}</div>
          <div className="text-xs">{data.started_at ? new Date(data.started_at).toLocaleString() : '-'}</div>
        </div>
      </div>

      {/* Ports */}
      {data.ports && data.ports.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold flex items-center gap-1.5">
            <Globe className="h-3.5 w-3.5" />
            {t('docker.containers.portBindings')} ({data.ports.length})
          </h4>
          <div className="bg-muted/30 rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b">
                  <th className="text-left px-3 py-1.5 text-xs text-muted-foreground font-medium">{t('docker.containers.hostPort')}</th>
                  <th className="text-left px-3 py-1.5 text-xs text-muted-foreground font-medium"><ChevronRight className="h-3 w-3 inline" /></th>
                  <th className="text-left px-3 py-1.5 text-xs text-muted-foreground font-medium">{t('docker.containers.containerPort')}</th>
                  <th className="text-left px-3 py-1.5 text-xs text-muted-foreground font-medium">{t('docker.containers.protocol')}</th>
                </tr>
              </thead>
              <tbody>
                {data.ports.map((p: any, i: number) => (
                  <tr key={i} className="border-b last:border-0">
                    <td className="px-3 py-1 font-mono text-xs">{p.host_port ? `${p.host_ip || '0.0.0.0'}:${p.host_port}` : '-'}</td>
                    <td className="px-3 py-1"><ChevronRight className="h-3 w-3 text-muted-foreground" /></td>
                    <td className="px-3 py-1 font-mono text-xs">{p.container_port}</td>
                    <td className="px-3 py-1 text-xs text-muted-foreground">{p.protocol}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Mounts */}
      {data.mounts && data.mounts.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold flex items-center gap-1.5">
            <HardDrive className="h-3.5 w-3.5" />
            {t('docker.containers.volumes')} ({data.mounts.length})
          </h4>
          <div className="space-y-1">
            {data.mounts.map((m: any, i: number) => (
              <div key={i} className="bg-muted/30 rounded-lg px-3 py-2 text-xs font-mono flex items-center gap-2">
                <span className="inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border shrink-0">{m.type}</span>
                <span className="truncate" title={m.source}>{m.source}</span>
                <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
                <span className="truncate" title={m.destination}>{m.destination}</span>
                <span className={`inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium ml-auto shrink-0 ${m.rw === 'true' ? 'bg-secondary text-secondary-foreground' : 'border border-border'}`}>
                  {m.rw === 'true' ? 'rw' : 'ro'}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Networks */}
      {data.networks && data.networks.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold flex items-center gap-1.5">
            <Network className="h-3.5 w-3.5" />
            {t('docker.containers.networkInfo')} ({data.networks.length})
          </h4>
          <div className="space-y-1">
            {data.networks.map((n: any, i: number) => (
              <div key={i} className="bg-muted/30 rounded-lg px-3 py-2 text-xs flex items-center gap-4">
                <span className="font-medium">{n.name}</span>
                <span className="font-mono text-muted-foreground">IP: {n.ip_address || '-'}</span>
                <span className="font-mono text-muted-foreground">GW: {n.gateway || '-'}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Environment Variables */}
      {data.env && data.env.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold flex items-center gap-1.5">
            <Variable className="h-3.5 w-3.5" />
            {t('docker.containers.envVars')} ({data.env.length})
          </h4>
          <div className="bg-muted/30 rounded-lg p-3 max-h-[200px] overflow-y-auto">
            {data.env.map((e: string, i: number) => {
              const eqIdx = e.indexOf('=')
              const key = eqIdx >= 0 ? e.substring(0, eqIdx) : e
              const val = eqIdx >= 0 ? e.substring(eqIdx + 1) : ''
              return (
                <div key={i} className="text-xs font-mono py-0.5 flex">
                  <span className="text-blue-400 shrink-0">{key}</span>
                  <span className="text-muted-foreground mx-1">=</span>
                  <span className="text-foreground truncate" title={val}>{val}</span>
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

// Extracted row component for reuse in grouped/standalone sections
function ContainerRow({
  container: c,
  actionLoading,
  onDetail,
  onTerminal,
  onStart,
  onStop,
  onPause,
  onUnpause,
  onRestart,
  onDelete,
  showService,
  statsMap,
  batchMode,
  selected,
  onToggleSelect,
  t,
}: {
  container: Container
  actionLoading: string | null
  onDetail: (c: Container) => void
  onTerminal: (c: Container) => void
  onStart: (id: string) => void
  onStop: (c: Container) => void
  onPause: (id: string) => void
  onUnpause: (id: string) => void
  onRestart: (c: Container) => void
  onDelete: (c: Container) => void
  showService?: boolean
  statsMap: Record<string, ContainerStatsResult>
  batchMode?: boolean
  selected?: boolean
  onToggleSelect?: (id: string) => void
  t: (key: string, opts?: Record<string, unknown>) => string
}) {
  const serviceName = c.Labels?.['com.docker.compose.service']
  return (
    <TableRow>
      {batchMode && (
        <TableCell className="w-10">
          <Checkbox
            checked={selected}
            onCheckedChange={() => onToggleSelect?.(c.Id)}
          />
        </TableCell>
      )}
      <TableCell
        className="font-medium cursor-pointer hover:underline"
        onClick={() => onDetail(c)}
      >
        <div>
          {formatContainerName(c.Names)}
          {showService && serviceName && (
            <div className="text-[11px] text-muted-foreground font-normal">{serviceName}</div>
          )}
        </div>
      </TableCell>
      <TableCell className="text-muted-foreground text-xs font-mono max-w-[150px] truncate" title={c.Image}>
        {c.Image}
      </TableCell>
      <TableCell>{statusBadge(c.State)}</TableCell>
      <TableCell>
        <ContainerStatsCell stats={statsMap[c.Id]} state={c.State} />
      </TableCell>
      <TableCell className="text-muted-foreground text-xs font-mono max-w-[160px] truncate" title={formatPorts(c.Ports)}>
        {formatPorts(c.Ports)}
      </TableCell>
      <TableCell className="text-muted-foreground text-xs whitespace-nowrap">{timeAgo(c.Created)}</TableCell>
      <TableCell className="text-right whitespace-nowrap">
        <div className="flex items-center justify-end gap-1">
          <Button
            variant="ghost"
            size="icon-xs"
            title={t('docker.containers.inspect')}
            onClick={() => onDetail(c)}
          >
            <Info />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            title={t('docker.containers.terminal')}
            onClick={() => onTerminal(c)}
          >
            <Terminal />
          </Button>
          {c.State === 'running' ? (
            <>
              <Button
                variant="ghost"
                size="icon-xs"
                title={t('docker.containers.pause')}
                disabled={actionLoading === c.Id}
                onClick={() => onPause(c.Id)}
              >
                <Pause />
              </Button>
              <Button
                variant="ghost"
                size="icon-xs"
                title={t('docker.containers.stop')}
                disabled={actionLoading === c.Id}
                onClick={() => onStop(c)}
              >
                <Square />
              </Button>
            </>
          ) : c.State === 'paused' ? (
            <Button
              variant="ghost"
              size="icon-xs"
              title={t('docker.containers.unpause')}
              disabled={actionLoading === c.Id}
              onClick={() => onUnpause(c.Id)}
            >
              <Play />
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="icon-xs"
              title={t('docker.containers.start')}
              disabled={actionLoading === c.Id}
              onClick={() => onStart(c.Id)}
            >
              <Play />
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-xs"
            title={t('docker.containers.restart')}
            disabled={actionLoading === c.Id}
            onClick={() => onRestart(c)}
          >
            <RotateCw />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            title={t('common.delete')}
            disabled={actionLoading === c.Id}
            onClick={() => onDelete(c)}
          >
            <Trash2 />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}

export default function DockerContainers() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [containers, setContainers] = useState<Container[]>([])
  const [loading, setLoading] = useState(true)
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [selectedContainer, setSelectedContainer] = useState<Container | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [detailTab, setDetailTab] = useState<string>('inspect')
  const [deleteTarget, setDeleteTarget] = useState<Container | null>(null)
  const [confirmAction, setConfirmAction] = useState<{ action: 'stop' | 'restart'; container: Container } | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [filterState, setFilterState] = useState<'all' | 'running' | 'stopped'>('all')
  const [collapsedStacks, setCollapsedStacks] = useState<Set<string>>(new Set())
  const [statsMap, setStatsMap] = useState<Record<string, ContainerStatsResult>>({})
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [batchMode, setBatchMode] = useState(false)
  const [stackActionLoading, setStackActionLoading] = useState<string | null>(null)
  const [confirmStackAction, setConfirmStackAction] = useState<{ action: 'down' | 'restart' | 'delete'; stackName: string } | null>(null)

  const fetchContainers = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getContainers()
      setContainers(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.containers.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchContainers()
  }, [fetchContainers])

  // Batch poll stats for all running containers every 5 seconds
  useEffect(() => {
    const hasRunning = containers.some(c => c.State === 'running')
    if (!hasRunning) {
      setStatsMap({})
      return
    }

    const fetchBatchStats = async () => {
      try {
        const results = await api.containerStatsBatch()
        const map: Record<string, ContainerStatsResult> = {}
        if (results) {
          for (const s of results) {
            map[s.id] = s
          }
        }
        setStatsMap(map)
      } catch {
        // ignore — stats are non-critical
      }
    }

    fetchBatchStats()
    const interval = setInterval(fetchBatchStats, 5000)
    return () => clearInterval(interval)
  }, [containers])

  const handleAction = async (
    action: 'start' | 'stop' | 'restart',
    containerId: string
  ) => {
    setActionLoading(containerId)
    try {
      if (action === 'start') await api.startContainer(containerId)
      else if (action === 'stop') await api.stopContainer(containerId)
      else if (action === 'restart') await api.restartContainer(containerId)
      toast.success(t('docker.containers.actionSuccess', { action }))
      await fetchContainers()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.containers.actionFailed', { action })
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setActionLoading(deleteTarget.Id)
    try {
      await api.removeContainer(deleteTarget.Id)
      toast.success(t('docker.containers.deleted'))
      setDeleteTarget(null)
      await fetchContainers()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.containers.deleteFailed')
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  const handlePause = async (id: string) => {
    setActionLoading(id)
    try {
      await api.pauseContainer(id)
      toast.success(t('docker.containers.paused'))
      await fetchContainers()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.containers.pauseFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  const handleUnpause = async (id: string) => {
    setActionLoading(id)
    try {
      await api.unpauseContainer(id)
      toast.success(t('docker.containers.unpaused'))
      await fetchContainers()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.containers.unpauseFailed'))
    } finally {
      setActionLoading(null)
    }
  }



  const handleStackAction = async (action: 'up' | 'down' | 'restart' | 'delete', stackName: string) => {
    setStackActionLoading(stackName)
    try {
      if (action === 'up') await api.composeUp(stackName)
      else if (action === 'down') await api.composeDown(stackName)
      else if (action === 'restart') {
        await api.composeDown(stackName)
        await api.composeUp(stackName)
      } else if (action === 'delete') {
        await api.deleteComposeProject(stackName, { removeVolumes: false })
      }
      toast.success(t('docker.containers.stackActionSuccess', { action, name: stackName }))
      await fetchContainers()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.containers.stackActionFailed'))
    } finally {
      setStackActionLoading(null)
    }
  }

  const handleBatchAction = async (action: 'start' | 'stop' | 'restart' | 'remove') => {
    if (selectedIds.size === 0) return
    setActionLoading('batch')
    try {
      const ids = Array.from(selectedIds)
      for (const id of ids) {
        try {
          if (action === 'start') await api.startContainer(id)
          else if (action === 'stop') await api.stopContainer(id)
          else if (action === 'restart') await api.restartContainer(id)
          else if (action === 'remove') await api.removeContainer(id)
        } catch { /* continue on individual failures */ }
      }
      toast.success(t('docker.containers.batchSuccess', { count: ids.length, action }))
      setSelectedIds(new Set())
      setBatchMode(false)
      await fetchContainers()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.containers.batchFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  const toggleSelect = (id: string) => {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const toggleSelectAll = () => {
    if (selectedIds.size === filteredContainers.length) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(filteredContainers.map(c => c.Id)))
    }
  }

  const openDetail = (container: Container) => {
    setSelectedContainer(container)
    setDetailTab('inspect')
    setDetailOpen(true)
  }

  const openTerminal = (container: Container) => {
    setSelectedContainer(container)
    setDetailTab('shell')
    setDetailOpen(true)
  }

  // Filtered containers
  const filteredContainers = containers.filter((c) => {
    const matchesSearch = searchQuery === '' ||
      formatContainerName(c.Names).toLowerCase().includes(searchQuery.toLowerCase()) ||
      c.Image.toLowerCase().includes(searchQuery.toLowerCase())
    const matchesState = filterState === 'all' ||
      (filterState === 'running' && c.State === 'running') ||
      (filterState === 'stopped' && c.State !== 'running')
    return matchesSearch && matchesState
  })

  // Group filtered containers by compose project
  const groupedContainers = (() => {
    const stacks = new Map<string, Container[]>()
    const standalone: Container[] = []
    for (const c of filteredContainers) {
      const project = c.Labels?.['com.docker.compose.project']
      if (project) {
        if (!stacks.has(project)) stacks.set(project, [])
        stacks.get(project)!.push(c)
      } else {
        standalone.push(c)
      }
    }
    // Sort stacks by name
    const sortedStacks = Array.from(stacks.entries()).sort((a, b) => a[0].localeCompare(b[0]))
    return { stacks: sortedStacks, standalone }
  })()

  const toggleStack = (name: string) => {
    setCollapsedStacks(prev => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const runningCount = containers.filter(c => c.State === 'running').length
  const stoppedCount = containers.length - runningCount

  return (
    <div className="space-y-4">
      {/* Summary cards */}
      <div className="grid grid-cols-3 gap-3">
        <div
          className={`cursor-pointer rounded-2xl p-4 transition-all duration-200 ${filterState === 'all' ? 'bg-primary/10 ring-1 ring-primary/30' : 'bg-card card-shadow hover:card-shadow-hover'}`}
          onClick={() => setFilterState('all')}
        >
          <span className="text-[13px] text-muted-foreground">{t('docker.containers.total')}</span>
          <div className="text-2xl font-bold mt-1">{containers.length}</div>
        </div>
        <div
          className={`cursor-pointer rounded-2xl p-4 transition-all duration-200 ${filterState === 'running' ? 'bg-[#00c471]/10 ring-1 ring-[#00c471]/30' : 'bg-card card-shadow hover:card-shadow-hover'}`}
          onClick={() => setFilterState('running')}
        >
          <span className="text-[13px] text-[#00c471]">{t('docker.containers.running')}</span>
          <div className="text-2xl font-bold text-[#00c471] mt-1">{runningCount}</div>
        </div>
        <div
          className={`cursor-pointer rounded-2xl p-4 transition-all duration-200 ${filterState === 'stopped' ? 'bg-[#f04452]/10 ring-1 ring-[#f04452]/30' : 'bg-card card-shadow hover:card-shadow-hover'}`}
          onClick={() => setFilterState('stopped')}
        >
          <span className="text-[13px] text-[#f04452]">{t('docker.containers.stopped')}</span>
          <div className="text-2xl font-bold text-[#f04452] mt-1">{stoppedCount}</div>
        </div>
      </div>

      {/* Toolbar */}
      <div className="flex flex-col sm:flex-row sm:items-center gap-2">
        <div className="relative flex-1 sm:max-w-xs">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder={t('docker.containers.searchPlaceholder')}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
          />
        </div>
        <div className="hidden sm:block flex-1" />
        {batchMode && selectedIds.size > 0 && (
          <div className="flex items-center gap-1 flex-wrap">
            <span className="text-[13px] text-muted-foreground mr-1">
              {t('docker.containers.selected', { count: selectedIds.size })}
            </span>
            <Button variant="outline" size="sm" className="rounded-xl" onClick={() => handleBatchAction('start')} disabled={actionLoading === 'batch'}>
              <Play />
              {t('docker.containers.batchStart')}
            </Button>
            <Button variant="outline" size="sm" className="rounded-xl" onClick={() => handleBatchAction('stop')} disabled={actionLoading === 'batch'}>
              <Square />
              {t('docker.containers.batchStop')}
            </Button>
            <Button variant="outline" size="sm" className="rounded-xl" onClick={() => handleBatchAction('restart')} disabled={actionLoading === 'batch'}>
              <RotateCw />
              {t('docker.containers.batchRestart')}
            </Button>
            <Button variant="destructive" size="sm" className="rounded-xl" onClick={() => handleBatchAction('remove')} disabled={actionLoading === 'batch'}>
              <Trash2 />
              {t('docker.containers.batchDelete')}
            </Button>
          </div>
        )}
        <div className="flex items-center gap-2">
          <p className="text-[13px] text-muted-foreground mr-2 hidden sm:block">
            {t('docker.containers.count', { count: filteredContainers.length })}
          </p>
          <Button
            variant={batchMode ? 'default' : 'outline'}
            size="sm"
            onClick={() => { setBatchMode(!batchMode); setSelectedIds(new Set()) }}
            className="rounded-xl hidden sm:inline-flex"
          >
            <CheckSquare />
            {t('docker.containers.batchMode')}
          </Button>
          <Button variant="outline" size="sm" onClick={fetchContainers} disabled={loading} className="rounded-xl">
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            <span className="hidden sm:inline">{t('common.refresh')}</span>
          </Button>
          <Button size="sm" onClick={() => navigate('/docker/stacks?new=1')} className="rounded-xl">
            <Plus />
            <span className="hidden sm:inline">{t('docker.stacks.newStack')}</span>
          </Button>
        </div>
      </div>

      {/* Mobile card view */}
      <div className="md:hidden space-y-2">
        {filteredContainers.length === 0 && !loading && (
          <div className="text-center text-muted-foreground py-8">
            {searchQuery ? t('docker.containers.noResults') : t('docker.containers.empty')}
          </div>
        )}
        {/* Stack groups */}
        {groupedContainers.stacks.map(([stackName, stackContainers]) => {
          const isCollapsed = collapsedStacks.has(stackName)
          const stackRunning = stackContainers.filter(c => c.State === 'running').length
          const isStackLoading = stackActionLoading === stackName
          return (
            <React.Fragment key={`mobile-stack-${stackName}`}>
              <div
                className="bg-secondary/30 rounded-2xl p-3 cursor-pointer"
                onClick={() => toggleStack(stackName)}
              >
                <div className="flex items-center gap-2 min-w-0">
                  {isCollapsed ? (
                    <ChevronRight className="h-4 w-4 text-muted-foreground shrink-0" />
                  ) : (
                    <ChevronDown className="h-4 w-4 text-muted-foreground shrink-0" />
                  )}
                  <Layers className="h-4 w-4 text-primary shrink-0" />
                  <span className="text-[13px] font-semibold truncate">{stackName}</span>
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                    {stackRunning}/{stackContainers.length}
                  </span>
                  <div className="flex-1 min-w-0" />
                  <div className="flex items-center gap-1 shrink-0" onClick={(e) => e.stopPropagation()}>
                    {stackRunning === 0 ? (
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t('docker.compose.up')}
                        disabled={isStackLoading}
                        onClick={() => handleStackAction('up', stackName)}
                      >
                        {isStackLoading ? <Loader2 className="animate-spin" /> : <Play />}
                      </Button>
                    ) : (
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t('docker.compose.down')}
                        disabled={isStackLoading}
                        onClick={() => setConfirmStackAction({ action: 'down', stackName })}
                      >
                        {isStackLoading ? <Loader2 className="animate-spin" /> : <Square />}
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('docker.containers.restart')}
                      disabled={isStackLoading}
                      onClick={() => setConfirmStackAction({ action: 'restart', stackName })}
                    >
                      <RotateCw />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('common.delete')}
                      disabled={isStackLoading}
                      onClick={() => setConfirmStackAction({ action: 'delete', stackName })}
                    >
                      <Trash2 className="text-destructive" />
                    </Button>
                  </div>
                </div>
              </div>
              {!isCollapsed && stackContainers.map((c) => {
                const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
                const statusClasses = c.State === 'running' ? `${base} bg-[#00c471]/10 text-[#00c471]`
                  : c.State === 'exited' ? `${base} bg-[#f04452]/10 text-[#f04452]`
                  : c.State === 'paused' ? `${base} bg-[#f59e0b]/10 text-[#f59e0b]`
                  : `${base} bg-secondary text-muted-foreground`
                const stats = statsMap[c.Id]
                return (
                  <div key={c.Id} className="bg-card rounded-2xl p-4 card-shadow ml-4">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0 flex-1 cursor-pointer" onClick={() => openDetail(c)}>
                        <p className="text-[13px] font-semibold truncate">
                          {formatContainerName(c.Names)}
                        </p>
                        <p className="text-[11px] text-muted-foreground truncate mt-0.5">{c.Image}</p>
                      </div>
                      <span className={statusClasses}>
                        {c.State}
                      </span>
                    </div>
                    {c.State === 'running' && stats && (
                      <div className="flex items-center gap-3 text-xs mt-2">
                        <span className="flex items-center gap-1">
                          <Cpu className="h-3 w-3 text-blue-500" />
                          <span className={stats.cpu_percent > 80 ? 'text-red-500 font-medium' : ''}>{stats.cpu_percent.toFixed(1)}%</span>
                        </span>
                        <span className="flex items-center gap-1">
                          <MemoryStick className="h-3 w-3 text-purple-500" />
                          <span className={stats.mem_percent > 80 ? 'text-red-500 font-medium' : ''}>{stats.mem_percent.toFixed(1)}%</span>
                        </span>
                      </div>
                    )}
                    {c.Ports && c.Ports.length > 0 && formatPorts(c.Ports) !== '-' && (
                      <p className="text-[11px] text-muted-foreground font-mono mt-2 truncate">
                        {formatPorts(c.Ports)}
                      </p>
                    )}
                    <div className="flex items-center gap-1 mt-3 justify-end">
                      <Button variant="ghost" size="icon-xs" title={t('docker.containers.inspect')} onClick={() => openDetail(c)}>
                        <Info />
                      </Button>
                      <Button variant="ghost" size="icon-xs" title={t('docker.containers.terminal')} onClick={() => openTerminal(c)}>
                        <Terminal />
                      </Button>
                      {c.State === 'running' ? (
                        <>
                          <Button variant="ghost" size="icon-xs" title={t('docker.containers.pause')} disabled={actionLoading === c.Id} onClick={() => handlePause(c.Id)}>
                            <Pause />
                          </Button>
                          <Button variant="ghost" size="icon-xs" title={t('docker.containers.stop')} disabled={actionLoading === c.Id} onClick={() => setConfirmAction({ action: 'stop', container: c })}>
                            <Square />
                          </Button>
                        </>
                      ) : c.State === 'paused' ? (
                        <Button variant="ghost" size="icon-xs" title={t('docker.containers.unpause')} disabled={actionLoading === c.Id} onClick={() => handleUnpause(c.Id)}>
                          <Play />
                        </Button>
                      ) : (
                        <Button variant="ghost" size="icon-xs" title={t('docker.containers.start')} disabled={actionLoading === c.Id} onClick={() => handleAction('start', c.Id)}>
                          <Play />
                        </Button>
                      )}
                      <Button variant="ghost" size="icon-xs" title={t('docker.containers.restart')} disabled={actionLoading === c.Id} onClick={() => setConfirmAction({ action: 'restart', container: c })}>
                        <RotateCw />
                      </Button>
                      <Button variant="ghost" size="icon-xs" title={t('common.delete')} disabled={actionLoading === c.Id} onClick={() => setDeleteTarget(c)}>
                        <Trash2 />
                      </Button>
                    </div>
                  </div>
                )
              })}
            </React.Fragment>
          )
        })}
        {/* Standalone section header (only if stacks exist) */}
        {groupedContainers.stacks.length > 0 && groupedContainers.standalone.length > 0 && (
          <div className="px-1 py-2">
            <span className="text-[13px] font-semibold text-muted-foreground">{t('docker.containers.standalone')}</span>
          </div>
        )}
        {/* Standalone containers */}
        {groupedContainers.standalone.map((c) => {
          const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
          const statusClasses = c.State === 'running' ? `${base} bg-[#00c471]/10 text-[#00c471]`
            : c.State === 'exited' ? `${base} bg-[#f04452]/10 text-[#f04452]`
            : c.State === 'paused' ? `${base} bg-[#f59e0b]/10 text-[#f59e0b]`
            : `${base} bg-secondary text-muted-foreground`
          const stats = statsMap[c.Id]
          return (
            <div key={c.Id} className="bg-card rounded-2xl p-4 card-shadow">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0 flex-1 cursor-pointer" onClick={() => openDetail(c)}>
                  <p className="text-[13px] font-semibold truncate">
                    {formatContainerName(c.Names)}
                  </p>
                  <p className="text-[11px] text-muted-foreground truncate mt-0.5">{c.Image}</p>
                </div>
                <span className={statusClasses}>
                  {c.State}
                </span>
              </div>
              {c.State === 'running' && stats && (
                <div className="flex items-center gap-3 text-xs mt-2">
                  <span className="flex items-center gap-1">
                    <Cpu className="h-3 w-3 text-blue-500" />
                    <span className={stats.cpu_percent > 80 ? 'text-red-500 font-medium' : ''}>{stats.cpu_percent.toFixed(1)}%</span>
                  </span>
                  <span className="flex items-center gap-1">
                    <MemoryStick className="h-3 w-3 text-purple-500" />
                    <span className={stats.mem_percent > 80 ? 'text-red-500 font-medium' : ''}>{stats.mem_percent.toFixed(1)}%</span>
                  </span>
                </div>
              )}
              {c.Ports && c.Ports.length > 0 && formatPorts(c.Ports) !== '-' && (
                <p className="text-[11px] text-muted-foreground font-mono mt-2 truncate">
                  {formatPorts(c.Ports)}
                </p>
              )}
              <div className="flex items-center gap-1 mt-3 justify-end">
                <Button variant="ghost" size="icon-xs" title={t('docker.containers.inspect')} onClick={() => openDetail(c)}>
                  <Info />
                </Button>
                <Button variant="ghost" size="icon-xs" title={t('docker.containers.terminal')} onClick={() => openTerminal(c)}>
                  <Terminal />
                </Button>
                {c.State === 'running' ? (
                  <>
                    <Button variant="ghost" size="icon-xs" title={t('docker.containers.pause')} disabled={actionLoading === c.Id} onClick={() => handlePause(c.Id)}>
                      <Pause />
                    </Button>
                    <Button variant="ghost" size="icon-xs" title={t('docker.containers.stop')} disabled={actionLoading === c.Id} onClick={() => setConfirmAction({ action: 'stop', container: c })}>
                      <Square />
                    </Button>
                  </>
                ) : c.State === 'paused' ? (
                  <Button variant="ghost" size="icon-xs" title={t('docker.containers.unpause')} disabled={actionLoading === c.Id} onClick={() => handleUnpause(c.Id)}>
                    <Play />
                  </Button>
                ) : (
                  <Button variant="ghost" size="icon-xs" title={t('docker.containers.start')} disabled={actionLoading === c.Id} onClick={() => handleAction('start', c.Id)}>
                    <Play />
                  </Button>
                )}
                <Button variant="ghost" size="icon-xs" title={t('docker.containers.restart')} disabled={actionLoading === c.Id} onClick={() => setConfirmAction({ action: 'restart', container: c })}>
                  <RotateCw />
                </Button>
                <Button variant="ghost" size="icon-xs" title={t('common.delete')} disabled={actionLoading === c.Id} onClick={() => setDeleteTarget(c)}>
                  <Trash2 />
                </Button>
              </div>
            </div>
          )
        })}
      </div>

      {/* Desktop table view */}
      <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden overflow-x-auto">
      <Table className="table-fixed w-full min-w-[800px]">
        <TableHeader>
          <TableRow className="border-border/50">
            {batchMode && (
              <TableHead className="w-10">
                <Checkbox
                  checked={filteredContainers.length > 0 && selectedIds.size === filteredContainers.length}
                  onCheckedChange={toggleSelectAll}
                />
              </TableHead>
            )}
            <TableHead className="text-[11px] w-[17%]">{t('docker.containers.name')}</TableHead>
            <TableHead className="text-[11px] w-[14%]">{t('docker.containers.image')}</TableHead>
            <TableHead className="text-[11px] w-[7%]">{t('docker.containers.status')}</TableHead>
            <TableHead className="text-[11px] w-[11%]">{t('docker.containers.resources')}</TableHead>
            <TableHead className="text-[11px] w-[16%]">{t('docker.containers.ports')}</TableHead>
            <TableHead className="text-[11px] w-[8%]">{t('common.created')}</TableHead>
            <TableHead className="text-right text-[11px] w-[27%]">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {filteredContainers.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={batchMode ? 8 : 7} className="text-center text-muted-foreground py-8">
                {searchQuery ? t('docker.containers.noResults') : t('docker.containers.empty')}
              </TableCell>
            </TableRow>
          )}
          {/* Stack groups */}
          {groupedContainers.stacks.map(([stackName, stackContainers]) => {
            const isCollapsed = collapsedStacks.has(stackName)
            const stackRunning = stackContainers.filter(c => c.State === 'running').length
            const isStackLoading = stackActionLoading === stackName
            return (
              <React.Fragment key={`stack-${stackName}`}>
                <TableRow
                  className="bg-secondary/30 hover:bg-secondary/50 cursor-pointer border-border/50"
                  onClick={() => toggleStack(stackName)}
                >
                  <TableCell colSpan={batchMode ? 8 : 7}>
                    <div className="flex items-center gap-2 min-w-0">
                      {isCollapsed ? (
                        <ChevronRight className="h-4 w-4 text-muted-foreground shrink-0" />
                      ) : (
                        <ChevronDown className="h-4 w-4 text-muted-foreground shrink-0" />
                      )}
                      <Layers className="h-4 w-4 text-primary shrink-0" />
                      <span className="text-[13px] font-semibold truncate">{stackName}</span>
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                        {stackRunning}/{stackContainers.length}
                      </span>
                      <div className="flex-1 min-w-0" />
                      <div className="flex items-center gap-1 shrink-0" onClick={(e) => e.stopPropagation()}>
                        {stackRunning === 0 ? (
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={t('docker.compose.up')}
                            disabled={isStackLoading}
                            onClick={() => handleStackAction('up', stackName)}
                          >
                            {isStackLoading ? <Loader2 className="animate-spin" /> : <Play />}
                          </Button>
                        ) : (
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={t('docker.compose.down')}
                            disabled={isStackLoading}
                            onClick={() => setConfirmStackAction({ action: 'down', stackName })}
                          >
                            {isStackLoading ? <Loader2 className="animate-spin" /> : <Square />}
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={t('docker.containers.restart')}
                          disabled={isStackLoading}
                          onClick={() => setConfirmStackAction({ action: 'restart', stackName })}
                        >
                          <RotateCw />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={t('common.delete')}
                          disabled={isStackLoading}
                          onClick={() => setConfirmStackAction({ action: 'delete', stackName })}
                        >
                          <Trash2 className="text-destructive" />
                        </Button>
                      </div>
                    </div>
                  </TableCell>
                </TableRow>
                {!isCollapsed && stackContainers.map((c) => (
                  <ContainerRow
                    key={c.Id}
                    container={c}
                    actionLoading={actionLoading}
                    onDetail={openDetail}
                    onTerminal={openTerminal}
                    onStart={(id) => handleAction('start', id)}
                    onStop={(ct) => setConfirmAction({ action: 'stop', container: ct })}
                    onPause={handlePause}
                    onUnpause={handleUnpause}
                    onRestart={(ct) => setConfirmAction({ action: 'restart', container: ct })}
                    onDelete={setDeleteTarget}

                    showService
                    statsMap={statsMap}
                    batchMode={batchMode}
                    selected={selectedIds.has(c.Id)}
                    onToggleSelect={toggleSelect}
                    t={t}
                  />
                ))}
              </React.Fragment>
            )
          })}
          {/* Standalone section header (only if stacks exist) */}
          {groupedContainers.stacks.length > 0 && groupedContainers.standalone.length > 0 && (
            <TableRow className="bg-secondary/30 border-border/50">
              <TableCell colSpan={batchMode ? 8 : 7}>
                <div className="flex items-center gap-2">
                  <span className="text-[13px] font-semibold text-muted-foreground">{t('docker.containers.standalone')}</span>
                </div>
              </TableCell>
            </TableRow>
          )}
          {/* Standalone containers */}
          {groupedContainers.standalone.map((c) => (
            <ContainerRow
              key={c.Id}
              container={c}
              actionLoading={actionLoading}
              onDetail={openDetail}
              onTerminal={openTerminal}
              onStart={(id) => handleAction('start', id)}
              onStop={(ct) => setConfirmAction({ action: 'stop', container: ct })}
              onPause={handlePause}
              onUnpause={handleUnpause}
              onRestart={(ct) => setConfirmAction({ action: 'restart', container: ct })}
              onDelete={setDeleteTarget}
              statsMap={statsMap}
              batchMode={batchMode}
              selected={selectedIds.has(c.Id)}
              onToggleSelect={toggleSelect}
              t={t}
            />
          ))}
        </TableBody>
      </Table>
      </div>

      {/* Container detail dialog */}
      <Dialog open={detailOpen} onOpenChange={setDetailOpen}>
        <DialogContent className="sm:max-w-4xl max-h-[90vh]">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {selectedContainer && statusBadge(selectedContainer.State)}
              {selectedContainer
                ? formatContainerName(selectedContainer.Names)
                : 'Container'}
            </DialogTitle>
            <DialogDescription className="font-mono text-xs">
              {selectedContainer?.Image} &middot; {selectedContainer?.Id.substring(0, 12)}
            </DialogDescription>
          </DialogHeader>
          {selectedContainer && (
            <Tabs defaultValue={detailTab} key={detailTab}>
              <TabsList>
                <TabsTrigger value="inspect">
                  <Info className="h-3.5 w-3.5 mr-1" />
                  {t('docker.containers.inspect')}
                </TabsTrigger>
                <TabsTrigger value="logs">
                  <Terminal className="h-3.5 w-3.5 mr-1" />
                  {t('docker.containers.logs')}
                </TabsTrigger>
                <TabsTrigger value="shell">
                  <Terminal className="h-3.5 w-3.5 mr-1" />
                  {t('docker.containers.shell')}
                </TabsTrigger>
              </TabsList>
              <TabsContent value="inspect">
                <ContainerInspect containerId={selectedContainer.Id} />
              </TabsContent>
              <TabsContent value="logs">
                <ContainerLogs containerId={selectedContainer.Id} />
              </TabsContent>
              <TabsContent value="shell">
                <ContainerShell containerId={selectedContainer.Id} />
              </TabsContent>
            </Tabs>
          )}
        </DialogContent>
      </Dialog>

      {/* Stop/Restart confirmation dialog */}
      <Dialog open={!!confirmAction} onOpenChange={(open) => !open && setConfirmAction(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirmAction?.action === 'stop' ? t('docker.containers.stopTitle') : t('docker.containers.restartTitle')}
            </DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey={confirmAction?.action === 'stop' ? 'docker.containers.stopConfirm' : 'docker.containers.restartConfirm'}
                values={{ name: confirmAction ? formatContainerName(confirmAction.container.Names) : '' }}
                components={{ strong: <span className="font-semibold" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmAction(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant={confirmAction?.action === 'stop' ? 'destructive' : 'default'}
              onClick={() => {
                if (confirmAction) {
                  handleAction(confirmAction.action, confirmAction.container.Id)
                  setConfirmAction(null)
                }
              }}
              disabled={actionLoading === confirmAction?.container.Id}
            >
              {confirmAction?.action === 'stop' ? t('docker.containers.stop') : t('docker.containers.restart')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Stack action confirmation dialog */}
      <Dialog open={!!confirmStackAction} onOpenChange={(open) => !open && setConfirmStackAction(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirmStackAction?.action === 'delete' ? t('common.delete') : confirmStackAction?.action === 'down' ? t('docker.compose.down') : t('docker.containers.restart')}
            </DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey={confirmStackAction?.action === 'delete' ? 'docker.containers.stackDeleteConfirm' : confirmStackAction?.action === 'down' ? 'docker.containers.stackDownConfirm' : 'docker.containers.stackRestartConfirm'}
                values={{ name: confirmStackAction?.stackName ?? '' }}
                components={{ strong: <span className="font-semibold" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmStackAction(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant={confirmStackAction?.action === 'down' || confirmStackAction?.action === 'delete' ? 'destructive' : 'default'}
              onClick={() => {
                if (confirmStackAction) {
                  handleStackAction(confirmStackAction.action, confirmStackAction.stackName)
                  setConfirmStackAction(null)
                }
              }}
              disabled={stackActionLoading === confirmStackAction?.stackName}
            >
              {confirmStackAction?.action === 'delete' ? t('common.delete') : confirmStackAction?.action === 'down' ? t('docker.compose.down') : t('docker.containers.restart')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.containers.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="docker.containers.deleteConfirm"
                values={{ name: deleteTarget ? formatContainerName(deleteTarget.Names) : '' }}
                components={{ strong: <span className="font-semibold" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={actionLoading === deleteTarget?.Id}
            >
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
