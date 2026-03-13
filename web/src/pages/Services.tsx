import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Cog,
  Search,
  RefreshCw,
  Play,
  Square,
  RotateCw,
  FileText,
  Loader2,
  MoreHorizontal,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { ServiceInfo, ServiceDeps } from '@/types/api'
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
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

type FilterType = 'all' | 'running' | 'failed' | 'inactive'

export default function Services() {
  const { t } = useTranslation()
  const [allServices, setAllServices] = useState<ServiceInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [searchQuery, setSearchQuery] = useState('')
  const [filter, setFilter] = useState<FilterType>('all')
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [logService, setLogService] = useState<string | null>(null)
  const [logs, setLogs] = useState('')
  const [logsLoading, setLogsLoading] = useState(false)
  const [serviceDeps, setServiceDeps] = useState<ServiceDeps | null>(null)
  const logContainerRef = useRef<HTMLDivElement>(null)

  const fetchServices = useCallback(async () => {
    try {
      const data = await api.listServices()
      setAllServices(data.services || [])
    } catch {
      toast.error(t('services.actionFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchServices()
  }, [fetchServices])

  // Auto-refresh every 15 seconds
  useEffect(() => {
    const interval = setInterval(fetchServices, 15000)
    return () => clearInterval(interval)
  }, [fetchServices])

  // Client-side filtering
  const filtered = useMemo(() => {
    let list = allServices

    // Filter by type
    if (filter !== 'all') {
      list = list.filter((s) => {
        switch (filter) {
          case 'running':
            return s.active_state === 'active' && s.sub_state === 'running'
          case 'failed':
            return s.active_state === 'failed'
          case 'inactive':
            return s.active_state === 'inactive'
          default:
            return true
        }
      })
    }

    // Filter by search query
    if (searchQuery) {
      const q = searchQuery.toLowerCase()
      list = list.filter(
        (s) =>
          s.name.toLowerCase().includes(q) ||
          s.description.toLowerCase().includes(q)
      )
    }

    // Sort by name
    return [...list].sort((a, b) =>
      a.name.toLowerCase().localeCompare(b.name.toLowerCase())
    )
  }, [allServices, filter, searchQuery])

  const handleAction = async (name: string, action: 'start' | 'stop' | 'restart' | 'enable' | 'disable') => {
    setActionLoading(`${name}:${action}`)
    try {
      switch (action) {
        case 'start':
          await api.startService(name)
          toast.success(t('services.startSuccess'))
          break
        case 'stop':
          await api.stopService(name)
          toast.success(t('services.stopSuccess'))
          break
        case 'restart':
          await api.restartService(name)
          toast.success(t('services.restartSuccess'))
          break
        case 'enable':
          await api.enableService(name)
          toast.success(t('services.enableSuccess'))
          break
        case 'disable':
          await api.disableService(name)
          toast.success(t('services.disableSuccess'))
          break
      }
      await fetchServices()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('services.actionFailed')
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  const handleViewLogs = async (name: string) => {
    setLogService(name)
    setLogs('')
    setServiceDeps(null)
    setLogsLoading(true)
    try {
      const [logsData, depsData] = await Promise.all([
        api.getServiceLogs(name, 200),
        api.getServiceDeps(name),
      ])
      setLogs(logsData.logs || '')
      setServiceDeps(depsData)
      setTimeout(() => {
        if (logContainerRef.current) {
          logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
        }
      }, 0)
    } catch {
      setLogs('Failed to load logs')
    } finally {
      setLogsLoading(false)
    }
  }

  const getActiveStateStyle = (activeState: string, subState: string) => {
    const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
    if (activeState === 'active' && subState === 'running') {
      return `${base} bg-[#00c471]/10 text-[#00c471]`
    }
    if (activeState === 'failed') {
      return `${base} bg-[#f04452]/10 text-[#f04452]`
    }
    if (activeState === 'activating' || activeState === 'deactivating') {
      return `${base} bg-[#f59e0b]/10 text-[#f59e0b]`
    }
    return `${base} bg-muted text-muted-foreground`
  }

  const getEnabledStyle = (enabled: string) => {
    const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
    switch (enabled) {
      case 'enabled':
        return `${base} bg-[#00c471]/10 text-[#00c471]`
      case 'static':
        return `${base} bg-[#3182f6]/10 text-[#3182f6]`
      case 'masked':
        return `${base} bg-[#f04452]/10 text-[#f04452]`
      default:
        return `${base} bg-muted text-muted-foreground`
    }
  }

  const filters: { key: FilterType; labelKey: string }[] = [
    { key: 'all', labelKey: 'services.filterAll' },
    { key: 'running', labelKey: 'services.filterRunning' },
    { key: 'failed', labelKey: 'services.filterFailed' },
    { key: 'inactive', labelKey: 'services.filterInactive' },
  ]

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[22px] font-bold tracking-tight flex items-center gap-2">
            <Cog className="h-5 w-5" />
            {t('services.title')}
          </h1>
          <p className="text-[13px] text-muted-foreground mt-1">{t('services.subtitle')}</p>
        </div>
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('services.count', { count: allServices.length })}
        </span>
      </div>

      {/* Search and controls */}
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder={t('services.search')}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
          />
        </div>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={fetchServices} disabled={loading}>
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          {t('common.refresh')}
        </Button>
      </div>

      {/* Filter buttons */}
      <div className="flex items-center gap-2">
        {filters.map((f) => (
          <Button
            key={f.key}
            variant={filter === f.key ? 'default' : 'outline'}
            size="sm"
            onClick={() => setFilter(f.key)}
            className="h-7 text-xs rounded-xl"
          >
            {t(f.labelKey)}
          </Button>
        ))}
        {(searchQuery || filter !== 'all') && (
          <span className="text-xs text-muted-foreground ml-2">
            {filtered.length} / {allServices.length}
          </span>
        )}
      </div>

      {/* Services table */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <Table className="table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[30%]">{t('services.name')}</TableHead>
              <TableHead className="hidden md:table-cell">{t('services.description')}</TableHead>
              <TableHead className="w-24">{t('services.status')}</TableHead>
              <TableHead className="w-24">{t('services.boot')}</TableHead>
              <TableHead className="text-right w-14">{t('services.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 && !loading && (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                  {t('services.noServices')}
                </TableCell>
              </TableRow>
            )}
            {filtered.map((svc) => (
              <TableRow key={svc.name} className="group">
                <TableCell>
                  <span className="font-medium text-[13px] truncate block" title={svc.name}>{svc.name}</span>
                </TableCell>
                <TableCell className="hidden md:table-cell">
                  <span className="text-[13px] text-muted-foreground truncate block" title={svc.description}>
                    {svc.description}
                  </span>
                </TableCell>
                <TableCell>
                  <span className={getActiveStateStyle(svc.active_state, svc.sub_state)}>
                    {svc.sub_state || svc.active_state}
                  </span>
                </TableCell>
                <TableCell>
                  <span className={getEnabledStyle(svc.enabled)}>
                    {svc.enabled}
                  </span>
                </TableCell>
                <TableCell className="text-right">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        className="opacity-0 group-hover:opacity-100 transition-opacity"
                        disabled={actionLoading?.startsWith(svc.name + ':') || false}
                      >
                        {actionLoading?.startsWith(svc.name + ':') ? (
                          <Loader2 className="h-4 w-4 animate-spin" />
                        ) : (
                          <MoreHorizontal className="h-4 w-4" />
                        )}
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem onClick={() => handleAction(svc.name, 'start')} disabled={svc.active_state === 'active'}>
                        <Play className="h-4 w-4 mr-2" />
                        {t('services.start')}
                      </DropdownMenuItem>
                      <DropdownMenuItem onClick={() => handleAction(svc.name, 'stop')} disabled={svc.active_state === 'inactive'}>
                        <Square className="h-4 w-4 mr-2" />
                        {t('services.stop')}
                      </DropdownMenuItem>
                      <DropdownMenuItem onClick={() => handleAction(svc.name, 'restart')}>
                        <RotateCw className="h-4 w-4 mr-2" />
                        {t('services.restart')}
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      {svc.enabled === 'enabled' ? (
                        <DropdownMenuItem onClick={() => handleAction(svc.name, 'disable')}>
                          {t('services.disable')}
                        </DropdownMenuItem>
                      ) : svc.enabled === 'disabled' ? (
                        <DropdownMenuItem onClick={() => handleAction(svc.name, 'enable')}>
                          {t('services.enable')}
                        </DropdownMenuItem>
                      ) : null}
                      <DropdownMenuSeparator />
                      <DropdownMenuItem onClick={() => handleViewLogs(svc.name)}>
                        <FileText className="h-4 w-4 mr-2" />
                        {t('services.viewLogs')}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Log dialog */}
      <Dialog open={!!logService} onOpenChange={(open) => !open && setLogService(null)}>
        <DialogContent className="max-w-3xl max-h-[80vh]">
          <DialogHeader>
            <DialogTitle>{t('services.logsFor', { name: logService })}</DialogTitle>
          </DialogHeader>
          {/* Dependency info */}
          {serviceDeps && (serviceDeps.required_by?.length || serviceDeps.requires?.length || serviceDeps.wanted_by?.length) ? (
            <div className="space-y-2">
              {serviceDeps.required_by && serviceDeps.required_by.length > 0 && (
                <div className="p-3 bg-amber-500/10 rounded-xl">
                  <p className="text-[11px] font-medium text-amber-500">{t('services.dependents')}</p>
                  <p className="text-[13px] mt-1">{serviceDeps.required_by.join(', ')}</p>
                </div>
              )}
              {serviceDeps.requires && serviceDeps.requires.length > 0 && (
                <div className="p-3 bg-[#3182f6]/10 rounded-xl">
                  <p className="text-[11px] font-medium text-[#3182f6]">{t('services.requires')}</p>
                  <p className="text-[13px] mt-1">{serviceDeps.requires.join(', ')}</p>
                </div>
              )}
              {serviceDeps.wanted_by && serviceDeps.wanted_by.length > 0 && (
                <div className="p-3 bg-muted rounded-xl">
                  <p className="text-[11px] font-medium text-muted-foreground">{t('services.wantedBy')}</p>
                  <p className="text-[13px] mt-1">{serviceDeps.wanted_by.join(', ')}</p>
                </div>
              )}
            </div>
          ) : null}

          <div ref={logContainerRef} className="bg-[#1a1a2e] rounded-xl p-4 overflow-auto max-h-[60vh]">
            {logsLoading ? (
              <div className="flex items-center justify-center py-8">
                <Loader2 className="h-5 w-5 animate-spin text-gray-400" />
              </div>
            ) : (
              <pre className="text-[12px] leading-5 text-gray-300 font-mono whitespace-pre-wrap break-all">
                {logs || t('services.noLogs')}
              </pre>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
