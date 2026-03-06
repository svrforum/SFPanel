import { useState, useEffect, useCallback } from 'react'
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
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { useWebSocket } from '@/hooks/useWebSocket'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { Metrics } from '@/types/api'
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

interface ProcessInfo {
  pid: number
  name: string
  cpu: number
  memory: number
  status: string
  user: string
  command: string
}

type SortField = 'cpu' | 'memory' | 'pid' | 'name'

export default function Processes() {
  const { t } = useTranslation()
  const [processes, setProcesses] = useState<ProcessInfo[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [searchQuery, setSearchQuery] = useState('')
  const [sortField, setSortField] = useState<SortField>('cpu')
  const [killTarget, setKillTarget] = useState<ProcessInfo | null>(null)
  const [killing, setKilling] = useState(false)
  const [sysMetrics, setSysMetrics] = useState<Metrics | null>(null)

  // Real-time metrics via WebSocket
  const onMetrics = useCallback((data: Metrics) => {
    setSysMetrics(data)
  }, [])

  useWebSocket({ url: '/ws/metrics', onMessage: onMetrics })

  const fetchProcesses = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.listProcesses(searchQuery, sortField)
      setProcesses(data.processes || [])
      setTotal(data.total)
    } catch {
      toast.error(t('processes.fetchFailed'))
    } finally {
      setLoading(false)
    }
  }, [searchQuery, sortField, t])

  useEffect(() => {
    fetchProcesses()
  }, [fetchProcesses])

  // Auto-refresh every 5 seconds
  useEffect(() => {
    const interval = setInterval(fetchProcesses, 5000)
    return () => clearInterval(interval)
  }, [fetchProcesses])

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

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault()
    fetchProcesses()
  }

  const toggleSort = (field: SortField) => {
    setSortField(field)
  }

  const getStatusStyle = (status: string) => {
    const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
    switch (status) {
      case 'running': return `${base} bg-[#00c471]/10 text-[#00c471]`
      case 'sleeping': return `${base} bg-secondary text-muted-foreground`
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
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">{t('processes.total', { count: total })}</span>
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
                      backgroundColor: sysMetrics.swap_percent > 80 ? '#f04452' : sysMetrics.swap_percent > 50 ? '#f59e0b' : '#f59e0b'
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
        <form onSubmit={handleSearch} className="flex-1 flex gap-2">
          <div className="relative flex-1">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
            <Input
              placeholder={t('processes.searchPlaceholder')}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="pl-9"
            />
          </div>
          <Button type="submit" variant="outline" size="sm">
            <Search className="h-4 w-4" />
          </Button>
        </form>
        <Button variant="outline" size="sm" onClick={fetchProcesses} disabled={loading}>
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
            onClick={() => toggleSort(field)}
            className="h-7 text-xs"
          >
            <ArrowUpDown className="h-3 w-3 mr-1" />
            {t(`processes.sort_${field}`)}
          </Button>
        ))}
      </div>

      {/* Process table */}
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
          <TableBody>
            {processes.length === 0 && !loading && (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground py-8">
                  {searchQuery ? t('processes.noResults') : t('processes.empty')}
                </TableCell>
              </TableRow>
            )}
            {processes.map((proc) => (
              <TableRow key={proc.pid} className="group">
                <TableCell className="font-mono text-xs">{proc.pid}</TableCell>
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
                <TableCell className="text-right font-mono text-xs">
                  <span className={proc.cpu > 50 ? 'text-red-500 font-bold' : proc.cpu > 20 ? 'text-yellow-500' : ''}>
                    {proc.cpu.toFixed(1)}
                  </span>
                </TableCell>
                <TableCell className="text-right font-mono text-xs">
                  <span className={proc.memory > 50 ? 'text-red-500 font-bold' : proc.memory > 20 ? 'text-yellow-500' : ''}>
                    {proc.memory.toFixed(1)}
                  </span>
                </TableCell>
                <TableCell>
                  <span className={getStatusStyle(proc.status)}>
                    {statusLabel(proc.status)}
                  </span>
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    className="opacity-0 group-hover:opacity-100 transition-opacity text-red-500 hover:text-red-600"
                    title={t('processes.kill')}
                    onClick={() => setKillTarget(proc)}
                  >
                    <Skull className="h-4 w-4" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
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
            <Button variant="outline" onClick={() => setKillTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="outline"
              onClick={() => handleKill('TERM')}
              disabled={killing}
            >
              {killing ? <Loader2 className="animate-spin h-4 w-4" /> : null}
              SIGTERM
            </Button>
            <Button
              variant="destructive"
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
