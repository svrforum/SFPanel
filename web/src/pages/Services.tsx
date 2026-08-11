import { useState, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
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
import type { ServiceInfo } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ListLoadState } from '@/components/ListLoadState'
import { StatusPill, type StatusPillTone } from '@/components/StatusPill'
import { ServiceDetailDialog } from '@/pages/services/components/ServiceDetailDialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

type FilterType = 'all' | 'running' | 'failed' | 'inactive'

const activeStateTone = (activeState: string, subState: string): StatusPillTone => {
  if (activeState === 'active' && subState === 'running') return 'success'
  if (activeState === 'failed') return 'destructive'
  if (activeState === 'activating' || activeState === 'deactivating') return 'warning'
  return 'muted'
}

const enabledTone = (enabled: string): StatusPillTone => {
  switch (enabled) {
    case 'enabled':
      return 'success'
    case 'static':
      return 'primary'
    case 'masked':
      return 'destructive'
    default:
      return 'muted'
  }
}

export default function Services() {
  const { t } = useTranslation()
  const [allServices, setAllServices] = useState<ServiceInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [filter, setFilter] = useState<FilterType>('all')
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [logService, setLogService] = useState<string | null>(null)

  const fetchServices = useCallback(async () => {
    try {
      setError(null)
      const data = await api.listServices()
      setAllServices(data.services || [])
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err))
      toast.error(t('services.actionFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  // Fetch on mount + every 15s while the tab is visible (paused when hidden).
  useVisibleInterval(fetchServices, 15000)

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

  // Full action menu (start/stop/restart, boot enable/disable, logs) — shared
  // by the desktop table cell and the mobile card so both expose the same set.
  const renderActionsMenu = (svc: ServiceInfo, triggerClassName?: string) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-xs"
          aria-label={t('services.actions')}
          className={triggerClassName}
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
        <DropdownMenuItem onClick={() => setLogService(svc.name)}>
          <FileText className="h-4 w-4 mr-2" />
          {t('services.viewLogs')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )

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

      {/* Load error / loading skeleton (first load only) */}
      {allServices.length === 0 && (
        <ListLoadState
          loading={loading}
          error={error}
          errorTitle={t('services.loadError')}
          onRetry={fetchServices}
        />
      )}

      {/* Mobile card view */}
      <div className="md:hidden space-y-2">
        {filtered.length === 0 && !loading && !error && (
          <div className="text-center text-muted-foreground py-8 text-[13px]">
            {t('services.noServices')}
          </div>
        )}
        {filtered.map((svc) => (
          <div key={svc.name} className="bg-card rounded-2xl p-4 card-shadow">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0 flex-1">
                <p className="text-[13px] font-medium truncate" title={svc.name}>{svc.name}</p>
                {svc.description && (
                  <p className="text-[11px] text-muted-foreground truncate mt-0.5" title={svc.description}>
                    {svc.description}
                  </p>
                )}
                <div className="flex items-center gap-2 mt-2">
                  <StatusPill tone={activeStateTone(svc.active_state, svc.sub_state)}>
                    {svc.sub_state || svc.active_state}
                  </StatusPill>
                  <StatusPill tone={enabledTone(svc.enabled)}>
                    {svc.enabled}
                  </StatusPill>
                </div>
              </div>
              <div className="flex items-center gap-1 shrink-0">
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title={t('services.start')}
                  aria-label={t('services.start')}
                  disabled={svc.active_state === 'active' || !!actionLoading?.startsWith(svc.name + ':')}
                  onClick={() => handleAction(svc.name, 'start')}
                >
                  <Play className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title={t('services.stop')}
                  aria-label={t('services.stop')}
                  disabled={svc.active_state === 'inactive' || !!actionLoading?.startsWith(svc.name + ':')}
                  onClick={() => handleAction(svc.name, 'stop')}
                >
                  <Square className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title={t('services.restart')}
                  aria-label={t('services.restart')}
                  disabled={!!actionLoading?.startsWith(svc.name + ':')}
                  onClick={() => handleAction(svc.name, 'restart')}
                >
                  <RotateCw className="h-4 w-4" />
                </Button>
                {renderActionsMenu(svc)}
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Services table (desktop) */}
      <div className={`bg-card rounded-2xl card-shadow overflow-hidden ${(error || loading) && allServices.length === 0 ? 'hidden' : 'hidden md:block'}`}>
        <Table className="table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[30%]">{t('services.name')}</TableHead>
              <TableHead>{t('services.description')}</TableHead>
              <TableHead className="w-24">{t('services.status')}</TableHead>
              <TableHead className="w-24">{t('services.boot')}</TableHead>
              <TableHead className="text-right w-14">{t('services.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 && !loading && !error && (
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
                <TableCell>
                  <span className="text-[13px] text-muted-foreground truncate block" title={svc.description}>
                    {svc.description}
                  </span>
                </TableCell>
                <TableCell>
                  <StatusPill tone={activeStateTone(svc.active_state, svc.sub_state)}>
                    {svc.sub_state || svc.active_state}
                  </StatusPill>
                </TableCell>
                <TableCell>
                  <StatusPill tone={enabledTone(svc.enabled)}>
                    {svc.enabled}
                  </StatusPill>
                </TableCell>
                <TableCell className="text-right">
                  {renderActionsMenu(svc, 'opacity-100 md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100 transition-opacity')}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Log / unit / dependency dialog */}
      <ServiceDetailDialog serviceName={logService} onClose={() => setLogService(null)} />
    </div>
  )
}
