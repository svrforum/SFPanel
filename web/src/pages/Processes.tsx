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
} from 'lucide-react'
import { toast } from 'sonner'
import { useVirtualizer } from '@tanstack/react-virtual'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { useWebSocket } from '@/hooks/useWebSocket'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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
const ROW_HEIGHT = 44

export default function Processes() {
  const { t } = useTranslation()
  const [allProcesses, setAllProcesses] = useState<ProcessInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [searchQuery, setSearchQuery] = useState('')
  const [sortField, setSortField] = useState<SortField>('cpu')
  const [killTarget, setKillTarget] = useState<ProcessInfo | null>(null)
  const [killing, setKilling] = useState(false)
  const [sysMetrics, setSysMetrics] = useState<Metrics | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  // Real-time metrics via WebSocket
  const onMetrics = useCallback((data: Metrics) => {
    setSysMetrics(data)
  }, [])

  useWebSocket({ url: '/ws/metrics', onMessage: onMetrics })

  const fetchProcesses = useCallback(async () => {
    try {
      const data = await api.listProcesses()
      setAllProcesses(data.processes || [])
    } catch {
      toast.error(t('processes.fetchFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchProcesses()
  }, [fetchProcesses])

  // Auto-refresh every 10 seconds (server caches for 3s anyway)
  useEffect(() => {
    const interval = setInterval(fetchProcesses, 10000)
    return () => clearInterval(interval)
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

  // Virtual scrolling for large process lists
  const rowVirtualizer = useVirtualizer({
    count: sorted.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
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
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="bg-card rounded-2xl p-4 card-shadow">
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

          <div className="bg-card rounded-2xl p-4 card-shadow">
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

          <div className="bg-card rounded-2xl p-4 card-shadow">
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
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder={t('processes.searchPlaceholder')}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
          />
        </div>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={fetchProcesses} disabled={loading}>
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          {t('common.refresh')}
        </Button>
      </div>

      {/* Sort buttons */}
      <div className="flex items-center gap-2">
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

      {/* Process table with virtual scrolling */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-20">PID</TableHead>
              <TableHead>{t('processes.name')}</TableHead>
              <TableHead>{t('processes.user')}</TableHead>
              <TableHead className="w-20 text-right">CPU %</TableHead>
              <TableHead className="w-20 text-right">MEM %</TableHead>
              <TableHead className="w-24">{t('processes.status')}</TableHead>
              <TableHead className="text-right w-20">{t('common.actions')}</TableHead>
            </TableRow>
          </TableHeader>
        </Table>
        <div
          ref={scrollRef}
          className="overflow-auto"
          style={{ maxHeight: 'calc(100vh - 420px)' }}
        >
          <Table>
            <TableBody>
              {sorted.length === 0 && !loading && (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground py-8">
                    {searchQuery ? t('processes.noResults') : t('processes.empty')}
                  </TableCell>
                </TableRow>
              )}
              {sorted.length > 0 && (
                <>
                  <tr style={{ height: rowVirtualizer.getVirtualItems()[0]?.start ?? 0 }} />
                  {rowVirtualizer.getVirtualItems().map((virtualRow) => {
                    const proc = sorted[virtualRow.index]
                    if (!proc) return null
                    return (
                      <TableRow key={proc.pid} className="group">
                        <TableCell className="font-mono text-xs w-20">{proc.pid}</TableCell>
                        <TableCell>
                          <div>
                            <span className="font-medium">{proc.name}</span>
                            {proc.command !== proc.name && (
                              <p className="text-xs text-muted-foreground truncate max-w-[400px]" title={proc.command}>
                                {proc.command}
                              </p>
                            )}
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
                        <TableCell className="w-24">
                          <span className={getStatusStyle(proc.status)}>
                            {statusLabel(proc.status)}
                          </span>
                        </TableCell>
                        <TableCell className="text-right w-20">
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            className="opacity-0 group-hover:opacity-100 transition-opacity text-[#f04452] hover:text-[#f04452]/80"
                            title={t('processes.kill')}
                            onClick={() => setKillTarget(proc)}
                          >
                            <Skull className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                  <tr style={{ height: rowVirtualizer.getTotalSize() - (rowVirtualizer.getVirtualItems().at(-1)?.end ?? 0) }} />
                </>
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Kill confirmation dialog */}
      <Dialog open={!!killTarget} onOpenChange={(open) => !open && setKillTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('processes.killTitle')}</DialogTitle>
            <DialogDescription>
              {t('processes.killConfirm', { name: killTarget?.name, pid: killTarget?.pid })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2 text-sm">
            <p className="text-muted-foreground">{t('processes.killDescription')}</p>
          </div>
          <DialogFooter className="gap-2">
            <Button variant="outline" className="rounded-xl" onClick={() => setKillTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="outline"
              className="rounded-xl"
              onClick={() => handleKill('TERM')}
              disabled={killing}
            >
              {killing ? <Loader2 className="animate-spin h-4 w-4" /> : null}
              SIGTERM
            </Button>
            <Button
              variant="destructive"
              className="rounded-xl"
              onClick={() => handleKill('KILL')}
              disabled={killing}
            >
              {killing ? <Loader2 className="animate-spin h-4 w-4" /> : null}
              SIGKILL
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
