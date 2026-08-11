import { useEffect, useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { ExternalLink, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import type { PortMapRow } from '@/types/api'

// Risk highlighting shared by the desktop table and the mobile cards:
// amber = firewall opens the port to Anywhere in front of a container,
// red = a host process listens with no firewall rule and no container.
function rowBorderClass(r: PortMapRow): string {
  const hasContainer = !!(r.containers && r.containers.length > 0)
  const externalRisk = !!(r.firewall && r.firewall.scope === 'Anywhere' && hasContainer)
  const exposedNoRule = !r.firewall && !hasContainer && !!r.process
  return externalRisk
    ? 'border-l-2 border-amber-500'
    : exposedNoRule
    ? 'border-l-2 border-destructive'
    : ''
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
            : 'text-emerald-600'
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
  return (
    <div className={`flex flex-col gap-0.5 ${align === 'end' ? 'items-end' : ''}`}>
      {containers.map((c, idx) => (
        <Link
          key={`${c.id}-${idx}`}
          to={`/docker/containers?selected=${encodeURIComponent(c.id)}`}
          className="inline-flex items-center gap-1 hover:text-primary outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
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

export function PortMapTable() {
  const { t } = useTranslation()
  const [rows, setRows] = useState<PortMapRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

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
    void load()
  }, [load])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <div className="space-y-2">
      <div className="flex justify-end">
        <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
          <RefreshCw className={`h-3.5 w-3.5 mr-1 ${loading ? 'animate-spin' : ''}`} />
          {t('common.refresh')}
        </Button>
      </div>
      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-[13px] text-destructive">
          {error}
        </div>
      )}
      {loading && rows.length === 0 ? (
        <div className="bg-card rounded-2xl p-3 card-shadow space-y-2">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full rounded-xl" />
          ))}
        </div>
      ) : (
        <>
          {/* Desktop table */}
          <Table className="hidden md:table">
            <TableHeader>
              <TableRow>
                <TableHead className="w-20">{t('firewall.portmap.port')}</TableHead>
                <TableHead className="w-16">{t('firewall.portmap.protocol')}</TableHead>
                <TableHead className="w-24">{t('firewall.portmap.state')}</TableHead>
                <TableHead className="w-56">{t('firewall.portmap.firewall')}</TableHead>
                <TableHead>{t('firewall.portmap.container')}</TableHead>
                <TableHead className="w-56">{t('firewall.portmap.process')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.length === 0 && !loading && (
                <TableRow>
                  <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                    {t('firewall.portmap.noData')}
                  </TableCell>
                </TableRow>
              )}
              {rows.map((r, i) => (
                <TableRow key={i} className={rowBorderClass(r)}>
                  <TableCell className="font-mono">{r.port}</TableCell>
                  <TableCell>
                    <Badge variant="outline" className="text-[10px]">
                      {r.proto.toUpperCase()}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-[12px]">
                    <Badge
                      variant={r.state === 'listening' ? 'default' : 'secondary'}
                      className="text-[10px]"
                    >
                      {r.state === 'listening' ? 'LISTENING' : 'BOUND'}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <FirewallCell firewall={r.firewall} className="text-[12px]" />
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
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {/* Mobile cards */}
          <div className="md:hidden space-y-2">
            {rows.length === 0 && !loading && (
              <div className="text-center text-muted-foreground py-8 text-[13px]">{t('firewall.portmap.noData')}</div>
            )}
            {rows.map((r, i) => (
              <div key={i} className={`bg-card rounded-xl p-3 card-shadow space-y-1 ${rowBorderClass(r)}`}>
                <div className="flex justify-between text-[12px]">
                  <span className="text-muted-foreground">{t('firewall.portmap.port')}</span>
                  <span className="font-mono">{r.port}</span>
                </div>
                <div className="flex justify-between text-[12px]">
                  <span className="text-muted-foreground">{t('firewall.portmap.protocol')}</span>
                  <Badge variant="outline" className="text-[10px]">
                    {r.proto.toUpperCase()}
                  </Badge>
                </div>
                <div className="flex justify-between text-[12px]">
                  <span className="text-muted-foreground">{t('firewall.portmap.state')}</span>
                  <Badge
                    variant={r.state === 'listening' ? 'default' : 'secondary'}
                    className="text-[10px]"
                  >
                    {r.state === 'listening' ? 'LISTENING' : 'BOUND'}
                  </Badge>
                </div>
                <div className="flex justify-between gap-2 text-[12px]">
                  <span className="text-muted-foreground shrink-0">{t('firewall.portmap.firewall')}</span>
                  <FirewallCell firewall={r.firewall} className="text-right" />
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
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
