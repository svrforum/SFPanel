import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Activity,
  Search,
  RefreshCw,
  Skull,
  Loader2,
  ArrowUpDown,
  Cpu,
  MemoryStick,
  HardDrive,
  Pause,
  Play,
  Gauge,
  List,
  ListTree,
  CornerDownRight,
  AlertCircle,
} from 'lucide-react'
import { toast } from 'sonner'
import { useVirtualizer } from '@tanstack/react-virtual'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { useWebSocket } from '@/hooks/useWebSocket'
import { useIsMobile } from '@/hooks/useIsMobile'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import type { Metrics, ProcessInfo } from '@/types/api'
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

type SortField = 'cpu' | 'memory' | 'pid' | 'name'
type ViewMode = 'list' | 'tree'
const ROW_HEIGHT = 44

// A process plus its computed tree depth (for flattened-DFS tree rendering).
type TreeRow = { proc: ProcessInfo; depth: number }

export default function Processes() {
  const { t } = useTranslation()
  const isMobile = useIsMobile()
  const [allProcesses, setAllProcesses] = useState<ProcessInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [sortField, setSortField] = useState<SortField>('cpu')
  const [killTarget, setKillTarget] = useState<ProcessInfo | null>(null)
  const [killing, setKilling] = useState(false)
  const [reniceTarget, setReniceTarget] = useState<ProcessInfo | null>(null)
  const [reniceValue, setReniceValue] = useState(0)
  const [renicing, setRenicing] = useState(false)
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [sysMetrics, setSysMetrics] = useState<Metrics | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const rowHeight = isMobile ? 68 : ROW_HEIGHT

  // Real-time metrics via WebSocket
  const onMetrics = useCallback((data: Metrics) => {
    setSysMetrics(data)
  }, [])

  useWebSocket({ url: '/ws/metrics', onMessage: onMetrics })

  const fetchProcesses = useCallback(async () => {
    try {
      setError(null)
      const data = await api.listProcesses()
      setAllProcesses(data.processes || [])
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err))
      toast.error(t('processes.fetchFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchProcesses()
  }, [fetchProcesses])

  // Auto-refresh every 15 seconds, pause when tab hidden
  useEffect(() => {
    const interval = setInterval(fetchProcesses, 15000)
    const handleVisibility = () => { if (!document.hidden) fetchProcesses() }
    document.addEventListener('visibilitychange', handleVisibility)
    return () => {
      clearInterval(interval)
      document.removeEventListener('visibilitychange', handleVisibility)
    }
  }, [fetchProcesses])

  // Client-side filtering
  const filtered = useMemo(() => {
    if (!searchQuery) return allProcesses
    const q = searchQuery.toLowerCase()
    return allProcesses.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.command.toLowerCase().includes(q) ||
        p.user.toLowerCase().includes(q) ||
        String(p.pid) === q
    )
  }, [allProcesses, searchQuery])

  // Client-side sorting
  const sorted = useMemo(() => {
    const arr = [...filtered]
    switch (sortField) {
      case 'cpu':
        arr.sort((a, b) => b.cpu - a.cpu)
        break
      case 'memory':
        arr.sort((a, b) => b.memory - a.memory)
        break
      case 'pid':
        arr.sort((a, b) => a.pid - b.pid)
        break
      case 'name':
        arr.sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase()))
        break
    }
    return arr
  }, [filtered, sortField])

  // Tree view: build a flattened depth-first parent→child ordering from the
  // (already search-filtered) set. Roots are processes whose ppid is not present
  // in the filtered set, or whose ppid is <= 1 (init/kernel). Children inherit
  // the active sort order among siblings.
  const treeRows = useMemo<TreeRow[]>(() => {
    if (viewMode !== 'tree') return []
    const present = new Set(filtered.map((p) => p.pid))
    const childrenByPpid = new Map<number, ProcessInfo[]>()
    for (const p of sorted) {
      const arr = childrenByPpid.get(p.ppid)
      if (arr) arr.push(p)
      else childrenByPpid.set(p.ppid, [p])
    }
    const roots = sorted.filter((p) => p.ppid <= 1 || !present.has(p.ppid))
    const rows: TreeRow[] = []
    const seen = new Set<number>()
    const visit = (proc: ProcessInfo, depth: number) => {
      if (seen.has(proc.pid)) return // guard against cycles
      seen.add(proc.pid)
      rows.push({ proc, depth })
      const kids = childrenByPpid.get(proc.pid)
      if (kids) for (const k of kids) visit(k, depth + 1)
    }
    for (const r of roots) visit(r, 0)
    // Any process not reached (e.g. orphaned by a cycle) is appended as a root.
    for (const p of sorted) if (!seen.has(p.pid)) visit(p, 0)
    return rows
  }, [viewMode, filtered, sorted])

  // Virtual scrolling for large process lists
  const rowVirtualizer = useVirtualizer({
    count: sorted.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: 20,
  })

  const handleKill = async (signal: string) => {
    if (!killTarget) return
    setKilling(true)
    try {
      await api.killProcess(killTarget.pid, signal)
      toast.success(t('processes.killSuccess', { pid: killTarget.pid, signal }))
      setKillTarget(null)
      await fetchProcesses()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('processes.killFailed')
      toast.error(message)
    } finally {
      setKilling(false)
    }
  }

  const openRenice = (proc: ProcessInfo) => {
    setReniceValue(proc.nice)
    setReniceTarget(proc)
  }

  const clampNice = (n: number) => Math.max(-20, Math.min(19, Math.round(n)))

  const handleRenice = async () => {
    if (!reniceTarget) return
    setRenicing(true)
    try {
      const nice = clampNice(reniceValue)
      await api.reniceProcess(reniceTarget.pid, nice)
      toast.success(t('processes.reniceSuccess', { pid: reniceTarget.pid, nice }))
      setReniceTarget(null)
      await fetchProcesses()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('processes.reniceFailed')
      toast.error(message)
    } finally {
      setRenicing(false)
    }
  }

  const getStatusStyle = (status: string) => {
    const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
    switch (status) {
      case 'running': return `${base} bg-[#00c471]/10 text-[#00c471]`
      case 'zombie': return `${base} bg-[#f04452]/10 text-[#f04452]`
      default: return `${base} bg-secondary text-muted-foreground`
    }
  }

  const statusLabel = (s: string) => {
    switch (s) {
      case 'running': return t('processes.running')
      case 'sleeping': return t('processes.sleeping')
      case 'zombie': return t('processes.zombie')
      case 'stopped': return t('processes.stopped')
      case 'idle': return t('processes.idle')
      default: return s
    }
  }

  const niceBadge = (nice: number) => {
    const base = 'inline-flex items-center gap-1 px-1.5 py-0.5 rounded-md text-[11px] font-mono'
    const tone =
      nice < 0 ? 'bg-[#f59e0b]/10 text-[#f59e0b]'
        : nice > 0 ? 'bg-secondary text-muted-foreground'
          : 'bg-primary/10 text-primary'
    return (
      <span className={`${base} ${tone}`} title={t('processes.nice')}>
        <Gauge className="h-3 w-3" />
        {nice}
      </span>
    )
  }

  const rowActions = (proc: ProcessInfo, visibility: string) => (
    <div className={`inline-flex items-center justify-end gap-0.5 ${visibility}`}>
      <Button
        variant="ghost"
        size="icon-xs"
        title={t('processes.renice')}
        onClick={() => openRenice(proc)}
      >
        <Gauge className="h-4 w-4" />
      </Button>
      <Button
        variant="ghost"
        size="icon-xs"
        className="text-[#f04452] hover:text-[#f04452]/80"
        title={t('processes.kill')}
        onClick={() => setKillTarget(proc)}
      >
        <Skull className="h-4 w-4" />
      </Button>
    </div>
  )

  // Shared desktop table row; `depth` indents the name cell in tree mode.
  const renderDesktopRow = (proc: ProcessInfo, depth = 0) => (
    <TableRow key={proc.pid} className="group">
      <TableCell className="font-mono text-xs w-20">{proc.pid}</TableCell>
      <TableCell>
        <div className="flex items-start gap-1" style={{ paddingLeft: depth * 16 }}>
          {depth > 0 && (
            <CornerDownRight className="h-3 w-3 mt-0.5 shrink-0 text-muted-foreground/50" />
          )}
          <div className="min-w-0">
            <span className="font-medium">{proc.name}</span>
            {proc.command !== proc.name && (
              <p className="text-xs text-muted-foreground truncate max-w-[400px]" title={proc.command}>
                {proc.command}
              </p>
            )}
          </div>
        </div>
      </TableCell>
      <TableCell className="text-xs">{proc.user}</TableCell>
      <TableCell className="text-right font-mono text-xs w-20">
        <span className={proc.cpu > 50 ? 'text-[#f04452] font-bold' : proc.cpu > 20 ? 'text-[#f59e0b]' : ''}>
          {proc.cpu.toFixed(1)}
        </span>
      </TableCell>
      <TableCell className="text-right font-mono text-xs w-20">
        <span className={proc.memory > 50 ? 'text-[#f04452] font-bold' : proc.memory > 20 ? 'text-[#f59e0b]' : ''}>
          {proc.memory.toFixed(1)}
        </span>
      </TableCell>
      <TableCell className="text-right font-mono text-xs w-24 text-muted-foreground">
        {formatBytes(proc.rss)}
      </TableCell>
      <TableCell className="w-16">{niceBadge(proc.nice)}</TableCell>
      <TableCell className="w-24">
        <span className={getStatusStyle(proc.status)}>
          {statusLabel(proc.status)}
        </span>
      </TableCell>
      <TableCell className="text-right w-24">
        {rowActions(proc, 'opacity-0 group-hover:opacity-100 transition-opacity')}
      </TableCell>
    </TableRow>
  )

  // Shared mobile card; `depth` indents in tree mode.
  const renderMobileCard = (proc: ProcessInfo, depth = 0) => (
    <div
      className="bg-card rounded-xl p-3 card-shadow flex items-center justify-between"
      style={{ marginLeft: depth * 14 }}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          {depth > 0 && <CornerDownRight className="h-3 w-3 shrink-0 text-muted-foreground/50" />}
          <p className="text-[13px] font-medium truncate">{proc.name}</p>
        </div>
        <div className="flex items-center gap-3 mt-1 text-[11px] text-muted-foreground flex-wrap">
          <span className="font-mono">PID {proc.pid}</span>
          <span className="flex items-center gap-1">
            <Cpu className="h-3 w-3" />
            <span className={proc.cpu > 50 ? 'text-[#f04452] font-bold' : proc.cpu > 20 ? 'text-[#f59e0b]' : ''}>
              {proc.cpu.toFixed(1)}%
            </span>
          </span>
          <span className="flex items-center gap-1">
            <MemoryStick className="h-3 w-3" />
            <span className={proc.memory > 50 ? 'text-[#f04452] font-bold' : proc.memory > 20 ? 'text-[#f59e0b]' : ''}>
              {proc.memory.toFixed(1)}%
            </span>
            <span className="text-muted-foreground">({formatBytes(proc.rss)})</span>
          </span>
          {niceBadge(proc.nice)}
        </div>
      </div>
      {rowActions(proc, 'ml-2')}
    </div>
  )

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[22px] font-bold tracking-tight flex items-center gap-2">
            <Activity className="h-5 w-5" />
            {t('processes.title')}
          </h1>
          <p className="text-[13px] text-muted-foreground mt-1">{t('processes.subtitle')}</p>
        </div>
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('processes.total', { count: allProcesses.length })}
        </span>
      </div>

      {/* Resource summary cards */}
      {sysMetrics && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-2 md:gap-4">
          <div className="bg-card rounded-2xl p-3 md:p-4 card-shadow">
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-xl bg-primary/10">
                <Cpu className="h-4 w-4 text-primary" />
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex items-center justify-between mb-1">
                  <span className="text-[13px] font-medium">{t('processes.cpuUsage')}</span>
                  <span className="text-[13px] font-bold">{sysMetrics.cpu.toFixed(1)}%</span>
                </div>
                <div className="h-1.5 rounded-full bg-secondary overflow-hidden">
                  <div
                    className="h-full rounded-full transition-all duration-500"
                    style={{
                      width: `${Math.min(100, sysMetrics.cpu)}%`,
                      backgroundColor: sysMetrics.cpu > 80 ? '#f04452' : sysMetrics.cpu > 50 ? '#f59e0b' : '#3182f6'
                    }}
                  />
                </div>
              </div>
            </div>
          </div>

          <div className="bg-card rounded-2xl p-3 md:p-4 card-shadow">
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-xl bg-[#00c471]/10">
                <MemoryStick className="h-4 w-4 text-[#00c471]" />
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex items-center justify-between mb-1">
                  <span className="text-[13px] font-medium">{t('processes.memUsage')}</span>
                  <span className="text-[13px] font-bold">{sysMetrics.mem_percent.toFixed(1)}%</span>
                </div>
                <div className="h-1.5 rounded-full bg-secondary overflow-hidden">
                  <div
                    className="h-full rounded-full transition-all duration-500"
                    style={{
                      width: `${Math.min(100, sysMetrics.mem_percent)}%`,
                      backgroundColor: sysMetrics.mem_percent > 80 ? '#f04452' : sysMetrics.mem_percent > 50 ? '#f59e0b' : '#00c471'
                    }}
                  />
                </div>
                <p className="text-[11px] text-muted-foreground mt-1">
                  {formatBytes(sysMetrics.mem_used)} / {formatBytes(sysMetrics.mem_total)}
                </p>
              </div>
            </div>
          </div>

          <div className="bg-card rounded-2xl p-3 md:p-4 card-shadow">
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-xl bg-[#f59e0b]/10">
                <HardDrive className="h-4 w-4 text-[#f59e0b]" />
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex items-center justify-between mb-1">
                  <span className="text-[13px] font-medium">{t('processes.swapUsage')}</span>
                  <span className="text-[13px] font-bold">
                    {sysMetrics.swap_total > 0 ? `${sysMetrics.swap_percent.toFixed(1)}%` : t('processes.swapDisabled')}
                  </span>
                </div>
                <div className="h-1.5 rounded-full bg-secondary overflow-hidden">
                  <div
                    className="h-full rounded-full transition-all duration-500"
                    style={{
                      width: `${Math.min(100, sysMetrics.swap_total > 0 ? sysMetrics.swap_percent : 0)}%`,
                      backgroundColor: sysMetrics.swap_percent > 80 ? '#f04452' : sysMetrics.swap_percent > 50 ? '#f59e0b' : '#3182f6'
                    }}
                  />
                </div>
                {sysMetrics.swap_total > 0 && (
                  <p className="text-[11px] text-muted-foreground mt-1">
                    {formatBytes(sysMetrics.swap_used)} / {formatBytes(sysMetrics.swap_total)}
                  </p>
                )}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Search and controls */}
      <div className="flex flex-col sm:flex-row gap-2 sm:items-center">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder={t('processes.searchPlaceholder')}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
          />
        </div>
        <div className="inline-flex items-center rounded-xl bg-secondary/50 p-0.5">
          <Button
            variant={viewMode === 'list' ? 'default' : 'ghost'}
            size="sm"
            className="h-8 rounded-lg text-xs"
            onClick={() => setViewMode('list')}
          >
            <List className="h-3.5 w-3.5 mr-1" />
            {t('processes.viewList')}
          </Button>
          <Button
            variant={viewMode === 'tree' ? 'default' : 'ghost'}
            size="sm"
            className="h-8 rounded-lg text-xs"
            onClick={() => setViewMode('tree')}
          >
            <ListTree className="h-3.5 w-3.5 mr-1" />
            {t('processes.viewTree')}
          </Button>
        </div>
        <Button variant="outline" size="sm" className="rounded-xl w-full sm:w-auto" onClick={fetchProcesses} disabled={loading}>
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          {t('common.refresh')}
        </Button>
      </div>

      {/* Sort buttons */}
      <div className="flex items-center gap-2 flex-wrap">
        <span className="text-xs text-muted-foreground">{t('processes.sortBy')}:</span>
        {(['cpu', 'memory', 'pid', 'name'] as SortField[]).map((field) => (
          <Button
            key={field}
            variant={sortField === field ? 'default' : 'outline'}
            size="sm"
            onClick={() => setSortField(field)}
            className="h-7 text-xs rounded-xl"
          >
            <ArrowUpDown className="h-3 w-3 mr-1" />
            {t(`processes.sort_${field}`)}
          </Button>
        ))}
        {searchQuery && (
          <span className="text-xs text-muted-foreground ml-2">
            {sorted.length} / {allProcesses.length}
          </span>
        )}
      </div>

      {/* Process list */}
      {error && allProcesses.length === 0 ? (
        <div className="bg-[#f04452]/10 text-[#f04452] rounded-xl p-3 flex items-start gap-2">
          <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-medium">{t('processes.loadError')}</p>
            <p className="text-[12px] opacity-80 mt-0.5 break-words">{error}</p>
          </div>
          <Button variant="outline" size="sm" className="rounded-xl shrink-0" onClick={fetchProcesses}>
            <RefreshCw className="h-3.5 w-3.5" />
            {t('common.retry')}
          </Button>
        </div>
      ) : loading && allProcesses.length === 0 ? (
        <div className="bg-card rounded-2xl p-3 card-shadow space-y-2">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full rounded-xl" />
          ))}
        </div>
      ) : sorted.length === 0 ? (
        <div className="bg-card rounded-2xl p-8 card-shadow text-center text-muted-foreground">
          {searchQuery ? t('processes.noResults') : t('processes.empty')}
        </div>
      ) : null}

      {sorted.length > 0 && (
        <>
          {/* Mobile card view */}
          <div className="md:hidden">
            {viewMode === 'list' ? (
              <div
                ref={isMobile ? scrollRef : undefined}
                className="overflow-auto space-y-2"
                style={{ maxHeight: 'calc(100vh - 420px)' }}
              >
                <div style={{ height: rowVirtualizer.getTotalSize(), position: 'relative' }}>
                  {rowVirtualizer.getVirtualItems().map((virtualRow) => {
                    const proc = sorted[virtualRow.index]
                    if (!proc) return null
                    return (
                      <div
                        key={proc.pid}
                        className="absolute left-0 right-0 px-0.5"
                        style={{ top: virtualRow.start, height: virtualRow.size }}
                      >
                        {renderMobileCard(proc)}
                      </div>
                    )
                  })}
                </div>
              </div>
            ) : (
              <div className="overflow-auto space-y-2" style={{ maxHeight: 'calc(100vh - 420px)' }}>
                {treeRows.map(({ proc, depth }) => (
                  <div key={proc.pid}>{renderMobileCard(proc, depth)}</div>
                ))}
              </div>
            )}
          </div>

          {/* Desktop table view */}
          <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-20">PID</TableHead>
                  <TableHead>{t('processes.name')}</TableHead>
                  <TableHead>{t('processes.user')}</TableHead>
                  <TableHead className="w-20 text-right">CPU %</TableHead>
                  <TableHead className="w-20 text-right">MEM %</TableHead>
                  <TableHead className="w-24 text-right">{t('processes.rss')}</TableHead>
                  <TableHead className="w-16">{t('processes.nice')}</TableHead>
                  <TableHead className="w-24">{t('processes.status')}</TableHead>
                  <TableHead className="text-right w-24">{t('common.actions')}</TableHead>
                </TableRow>
              </TableHeader>
            </Table>
            <div
              ref={isMobile || viewMode === 'tree' ? undefined : scrollRef}
              className="overflow-auto"
              style={{ maxHeight: 'calc(100vh - 420px)' }}
            >
              <Table>
                <TableBody>
                  {viewMode === 'list' ? (
                    <>
                      <tr style={{ height: rowVirtualizer.getVirtualItems()[0]?.start ?? 0 }} />
                      {rowVirtualizer.getVirtualItems().map((virtualRow) => {
                        const proc = sorted[virtualRow.index]
                        if (!proc) return null
                        return renderDesktopRow(proc)
                      })}
                      <tr style={{ height: rowVirtualizer.getTotalSize() - (rowVirtualizer.getVirtualItems().at(-1)?.end ?? 0) }} />
                    </>
                  ) : (
                    treeRows.map(({ proc, depth }) => renderDesktopRow(proc, depth))
                  )}
                </TableBody>
              </Table>
            </div>
          </div>
        </>
      )}

      {/* Kill confirmation dialog */}
      <Dialog open={!!killTarget} onOpenChange={(open) => !open && setKillTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('processes.killTitle')}</DialogTitle>
            <DialogDescription>
              {t('processes.killConfirm', { name: killTarget?.name, pid: killTarget?.pid })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 text-sm">
            <p className="text-muted-foreground">{t('processes.killDescription')}</p>

            {/* Job control: pause / resume (non-destructive) */}
            <div className="space-y-1.5">
              <span className="text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
                {t('processes.signalJobControl')}
              </span>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  className="rounded-xl flex-1"
                  onClick={() => handleKill('STOP')}
                  disabled={killing}
                >
                  {killing ? <Loader2 className="animate-spin h-4 w-4" /> : <Pause className="h-4 w-4" />}
                  {t('processes.signal_stop')}
                </Button>
                <Button
                  variant="outline"
                  className="rounded-xl flex-1"
                  onClick={() => handleKill('CONT')}
                  disabled={killing}
                >
                  {killing ? <Loader2 className="animate-spin h-4 w-4" /> : <Play className="h-4 w-4" />}
                  {t('processes.signal_cont')}
                </Button>
              </div>
            </div>

            {/* Destructive: terminate */}
            <div className="space-y-1.5">
              <span className="text-[11px] font-medium text-[#f04452] uppercase tracking-wide">
                {t('processes.signalDestructive')}
              </span>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  className="rounded-xl flex-1"
                  onClick={() => handleKill('TERM')}
                  disabled={killing}
                >
                  {killing ? <Loader2 className="animate-spin h-4 w-4" /> : null}
                  {t('processes.signal_term')}
                </Button>
                <Button
                  variant="destructive"
                  className="rounded-xl flex-1"
                  onClick={() => handleKill('KILL')}
                  disabled={killing}
                >
                  {killing ? <Loader2 className="animate-spin h-4 w-4" /> : null}
                  {t('processes.signal_kill')}
                </Button>
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" className="rounded-xl" onClick={() => setKillTarget(null)}>
              {t('common.cancel')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Renice dialog */}
      <Dialog open={!!reniceTarget} onOpenChange={(open) => !open && setReniceTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('processes.reniceTitle')}</DialogTitle>
            <DialogDescription>
              {t('processes.reniceConfirm', { name: reniceTarget?.name, pid: reniceTarget?.pid })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 text-sm">
            <p className="text-muted-foreground">{t('processes.reniceDescription')}</p>
            <div className="flex items-center justify-center gap-3">
              <Button
                variant="outline"
                size="icon"
                className="rounded-xl"
                onClick={() => setReniceValue((v) => clampNice(v - 1))}
                disabled={renicing || reniceValue <= -20}
              >
                -
              </Button>
              <Input
                type="number"
                min={-20}
                max={19}
                value={reniceValue}
                onChange={(e) => setReniceValue(clampNice(Number(e.target.value)))}
                className="w-20 h-10 text-center rounded-xl font-mono text-[15px]"
              />
              <Button
                variant="outline"
                size="icon"
                className="rounded-xl"
                onClick={() => setReniceValue((v) => clampNice(v + 1))}
                disabled={renicing || reniceValue >= 19}
              >
                +
              </Button>
            </div>
          </div>
          <DialogFooter className="gap-2">
            <Button variant="ghost" className="rounded-xl" onClick={() => setReniceTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button className="rounded-xl" onClick={handleRenice} disabled={renicing}>
              {renicing ? <Loader2 className="animate-spin h-4 w-4" /> : <Gauge className="h-4 w-4" />}
              {t('processes.renice')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
