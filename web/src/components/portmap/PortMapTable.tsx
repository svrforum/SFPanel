import { useEffect, useState, useCallback, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { AlertTriangle, ExternalLink, RefreshCw, Search } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { ListLoadState } from '@/components/ListLoadState'
import { api } from '@/lib/api'
import type { PortMapRow } from '@/types/api'
import { filterPortRows, type ProtoFilter } from './portRows'

// Risk shared by the desktop table and the mobile cards:
// anywhere = firewall opens the port to Anywhere in front of a container,
// noRule = a host process listens with no firewall rule and no container.
type Risk = 'anywhere' | 'noRule' | null

function rowRisk(r: PortMapRow): Risk {
  const hasContainer = !!(r.containers && r.containers.length > 0)
  if (r.firewall && r.firewall.scope === 'Anywhere' && hasContainer) return 'anywhere'
  if (!r.firewall && !hasContainer && r.process) return 'noRule'
  return null
}

// The left edge only: border-destructive set all four sides' colour and drew
// a red line under the row on a wide screen.
const RISK_BORDER: Record<'anywhere' | 'noRule', string> = {
  anywhere: 'border-l-2 border-l-amber-500',
  noRule: 'border-l-2 border-l-destructive',
}

// The risk in words beside the colour, which alone carried it.
function RiskBadge({ risk }: { risk: Risk }) {
  const { t } = useTranslation()
  if (!risk) return null
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-1.5 py-0.5 text-[12px] font-medium ${
        risk === 'anywhere' ? 'bg-warning/10 text-warning' : 'bg-destructive/10 text-destructive'
      }`}
    >
      <AlertTriangle className="h-3 w-3" aria-hidden="true" />
      {risk === 'anywhere' ? t('firewall.portmap.riskAnywhere') : t('firewall.portmap.riskNoRule')}
    </span>
  )
}

function FirewallCell({
  firewall,
  className,
}: {
  firewall: PortMapRow['firewall']
  className?: string
}) {
  if (!firewall) return <span className="text-muted-foreground">—</span>
  return (
    <span className={`inline-flex items-center gap-1 ${className ?? ''}`}>
      <span
        className={
          firewall.action === 'DENY' || firewall.action === 'REJECT'
            ? 'text-destructive'
            : 'text-success'
        }
      >
        {firewall.action}
      </span>
      <span className="text-muted-foreground">{firewall.scope}</span>
    </span>
  )
}

function ContainerLinks({
  containers,
  align,
}: {
  containers: PortMapRow['containers']
  align?: 'end'
}) {
  if (!containers || containers.length === 0) {
    return <span className="text-muted-foreground">—</span>
  }
  // One link per container: the same one came twice (once per address it
  // publishes on), as two 18px links 2px apart.
  const unique = containers.filter((c, i) => containers.findIndex((x) => x.id === c.id) === i)
  return (
    <div className={`flex flex-col gap-1 ${align === 'end' ? 'items-end' : ''}`}>
      {unique.map((c) => (
        <Link
          key={c.id}
          to={`/docker/containers?container=${encodeURIComponent(c.id)}`}
          className="inline-flex min-h-6 items-center gap-1 hover:text-primary outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0"
        >
          <span className="font-medium">{c.name}</span>
          {c.stack && (
            <span className="text-muted-foreground">({c.stack})</span>
          )}
          <ExternalLink className="h-3 w-3" />
        </Link>
      ))}
    </div>
  )
}

// The addresses a port is open on; a node older than the field names none.
function addressText(r: PortMapRow): string {
  return r.addresses && r.addresses.length > 0 ? r.addresses.join(', ') : '—'
}

/** Every port on this host — listening or published by Docker — with what
 * governs it. `rowAction` draws a control at the end of a row (the desktop
 * table's last column, the bottom of a phone card); it may return null for a
 * row that has none. */
export function PortMapTable({ rowAction }: { rowAction?: (row: PortMapRow) => ReactNode }) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<PortMapRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [proto, setProto] = useState<ProtoFilter>('all')

  const load = useCallback(async () => {
    try {
      const data = await api.getPortMap()
      setRows(data ?? [])
      setError(null)
    } catch (e) {
      const err = e as Error
      setError(err.message || t('firewall.portmap.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  const refresh = useCallback(() => {
    setLoading(true)
    setError(null)
    void load()
  }, [load])

  useEffect(() => {
    void load()
  }, [load])

  const shown = filterPortRows(rows, search, proto)
  const clearFilters = () => {
    setSearch('')
    setProto('all')
  }
  const riskBorder = (r: PortMapRow) => {
    const risk = rowRisk(r)
    return risk ? RISK_BORDER[risk] : ''
  }
  const stateLabel = (state: string) =>
    state === 'listening' ? t('firewall.portmap.listening') : t('firewall.portmap.bound')
  // The legend names only the risks the table shows.
  const risks = (['anywhere', 'noRule'] as const).filter((risk) => shown.some((r) => rowRisk(r) === risk))
  const columns = rowAction ? 8 : 7

  // No ports at all, or none the search and filter leave: a filter that
  // matches nothing is not "no open ports".
  const empty = rows.length === 0 ? (
    <p>{t('firewall.portmap.noData')}</p>
  ) : (
    <div className="space-y-3">
      <p>
        {search.trim()
          ? t('firewall.ports.noMatch', { q: search.trim() })
          : t('firewall.ports.noMatchFilter')}
      </p>
      <Button variant="outline" size="sm" className="rounded-xl" onClick={clearFilters}>
        {t('firewall.ports.clearFilters')}
      </Button>
    </div>
  )

  return (
    <div className="space-y-2">
      {/* The search has a row of its own on a phone, where its placeholder
          would otherwise be cut short. */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-full sm:w-auto sm:min-w-48 sm:flex-1">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('firewall.ports.search')}
            className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-base md:text-[13px]"
          />
        </div>
        <Select value={proto} onValueChange={(v) => setProto(v as ProtoFilter)}>
          <SelectTrigger aria-label={t('firewall.ports.protocol')} className="w-28 h-9 rounded-xl text-[13px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('firewall.ports.all')}</SelectItem>
            <SelectItem value="tcp">TCP</SelectItem>
            <SelectItem value="udp">UDP</SelectItem>
          </SelectContent>
        </Select>
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('firewall.ports.count', { count: shown.length })}
        </span>
        <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
          <RefreshCw className={`h-3.5 w-3.5 mr-1 ${loading ? 'animate-spin' : ''}`} />
          {t('common.refresh')}
        </Button>
      </div>
      {/* A failed first load is said once, with no "no data" under it. */}
      {(loading || error) && rows.length === 0 ? (
        <ListLoadState
          loading={loading}
          error={error}
          errorTitle={t('firewall.portmap.loadFailed')}
          onRetry={refresh}
        />
      ) : (
        <>
          {error && (
            <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-[13px] text-destructive">
              {error}
            </div>
          )}
          {risks.length > 0 && (
            <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[12px] text-muted-foreground">
              {risks.map((risk) => (
                <span key={risk} className="flex items-start gap-1.5">
                  <RiskBadge risk={risk} />
                  {risk === 'anywhere' ? t('firewall.portmap.riskAnywhereHint') : t('firewall.portmap.riskNoRuleHint')}
                </span>
              ))}
            </div>
          )}
          {/* Desktop table */}
          <Table className="hidden md:table">
            <TableHeader>
              <TableRow>
                <TableHead className="w-20">{t('firewall.portmap.port')}</TableHead>
                <TableHead className="w-16">{t('firewall.portmap.protocol')}</TableHead>
                <TableHead className="w-24">{t('firewall.portmap.state')}</TableHead>
                <TableHead>{t('firewall.portmap.address')}</TableHead>
                <TableHead>{t('firewall.portmap.firewall')}</TableHead>
                <TableHead>{t('firewall.portmap.container')}</TableHead>
                <TableHead>{t('firewall.portmap.process')}</TableHead>
                {rowAction && <TableHead className="w-32 text-right">{t('common.actions')}</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.length === 0 && (
                <TableRow>
                  <TableCell colSpan={columns} className="text-center text-muted-foreground py-8">
                    {empty}
                  </TableCell>
                </TableRow>
              )}
              {shown.map((r) => (
                <TableRow key={`${r.proto}/${r.port}`} className={`group ${riskBorder(r)}`}>
                  <TableCell className="font-mono">{r.port}</TableCell>
                  <TableCell>
                    <Badge variant="outline" className="text-[12px]">
                      {r.proto.toUpperCase()}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-[12px]">
                    <Badge
                      variant={r.state === 'listening' ? 'default' : 'secondary'}
                      className="text-[12px]"
                    >
                      {stateLabel(r.state)}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-[12px] whitespace-normal">{addressText(r)}</TableCell>
                  <TableCell className="whitespace-normal">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <FirewallCell firewall={r.firewall} className="text-[12px]" />
                      <RiskBadge risk={rowRisk(r)} />
                    </div>
                  </TableCell>
                  <TableCell className="text-[12px]">
                    <ContainerLinks containers={r.containers} />
                  </TableCell>
                  <TableCell className="text-[12px]">
                    {r.process ? (
                      <span className="font-mono">
                        {r.process.name}
                        {r.process.pid > 0 && ` (${r.process.pid})`}
                      </span>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  {rowAction && <TableCell className="text-right">{rowAction(r)}</TableCell>}
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {/* Mobile cards */}
          <div className="md:hidden space-y-2">
            {shown.length === 0 && (
              <div className="text-center text-muted-foreground py-8 text-[13px]">{empty}</div>
            )}
            {shown.map((r) => (
              <div key={`${r.proto}/${r.port}`} className={`bg-card rounded-xl p-3 card-shadow space-y-1 ${riskBorder(r)}`}>
                <div className="flex justify-between text-[12px]">
                  <span className="text-muted-foreground">{t('firewall.portmap.port')}</span>
                  <span className="font-mono">{r.port}</span>
                </div>
                <div className="flex justify-between text-[12px]">
                  <span className="text-muted-foreground">{t('firewall.portmap.protocol')}</span>
                  <Badge variant="outline" className="text-[12px]">
                    {r.proto.toUpperCase()}
                  </Badge>
                </div>
                <div className="flex justify-between text-[12px]">
                  <span className="text-muted-foreground">{t('firewall.portmap.state')}</span>
                  <Badge
                    variant={r.state === 'listening' ? 'default' : 'secondary'}
                    className="text-[12px]"
                  >
                    {stateLabel(r.state)}
                  </Badge>
                </div>
                <div className="flex justify-between gap-2 text-[12px]">
                  <span className="text-muted-foreground shrink-0">{t('firewall.portmap.address')}</span>
                  <span className="font-mono text-right break-all">{addressText(r)}</span>
                </div>
                <div className="flex justify-between gap-2 text-[12px]">
                  <span className="text-muted-foreground shrink-0">{t('firewall.portmap.firewall')}</span>
                  <span className="flex flex-wrap items-center justify-end gap-1.5">
                    <FirewallCell firewall={r.firewall} className="text-right" />
                    <RiskBadge risk={rowRisk(r)} />
                  </span>
                </div>
                <div className="flex justify-between gap-2 text-[12px]">
                  <span className="text-muted-foreground shrink-0">{t('firewall.portmap.container')}</span>
                  <ContainerLinks containers={r.containers} align="end" />
                </div>
                <div className="flex justify-between gap-2 text-[12px]">
                  <span className="text-muted-foreground shrink-0">{t('firewall.portmap.process')}</span>
                  {r.process ? (
                    <span className="font-mono text-right break-all">
                      {r.process.name}
                      {r.process.pid > 0 && ` (${r.process.pid})`}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </div>
                {/* A row with no action renders nothing here, and the empty box goes. */}
                {rowAction && <div className="flex justify-end pt-1 empty:hidden">{rowAction(r)}</div>}
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
