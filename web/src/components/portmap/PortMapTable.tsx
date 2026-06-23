import { useEffect, useState, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { ExternalLink, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { api } from '@/lib/api'
import type { PortMapRow } from '@/types/api'

export function PortMapTable() {
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
      setError(err.message || '포트 맵을 불러올 수 없습니다.')
    } finally {
      setLoading(false)
    }
  }, [])

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
          새로고침
        </Button>
      </div>
      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-[13px] text-destructive">
          {error}
        </div>
      )}
      {/* Desktop table */}
      <Table className="hidden md:table">
        <TableHeader>
          <TableRow>
            <TableHead className="w-20">포트</TableHead>
            <TableHead className="w-16">프로토콜</TableHead>
            <TableHead className="w-24">상태</TableHead>
            <TableHead className="w-56">방화벽</TableHead>
            <TableHead>컨테이너</TableHead>
            <TableHead className="w-56">프로세스</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                데이터 없음
              </TableCell>
            </TableRow>
          )}
          {rows.map((r, i) => {
            const hasContainer = r.containers && r.containers.length > 0
            const externalRisk = r.firewall && r.firewall.scope === 'Anywhere' && hasContainer
            const exposedNoRule = !r.firewall && !hasContainer && r.process
            const borderClass = externalRisk
              ? 'border-l-2 border-amber-500'
              : exposedNoRule
              ? 'border-l-2 border-destructive'
              : ''
            return (
              <TableRow key={i} className={borderClass}>
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
                  {r.firewall ? (
                    <span className="inline-flex items-center gap-1 text-[12px]">
                      <span
                        className={
                          r.firewall.action === 'DENY' || r.firewall.action === 'REJECT'
                            ? 'text-destructive'
                            : 'text-emerald-600'
                        }
                      >
                        {r.firewall.action}
                      </span>
                      <span className="text-muted-foreground">{r.firewall.scope}</span>
                    </span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell className="text-[12px]">
                  {r.containers && r.containers.length > 0 ? (
                    <div className="flex flex-col gap-0.5">
                      {r.containers.map((c, idx) => (
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
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
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
            )
          })}
        </TableBody>
      </Table>
      {/* Mobile cards */}
      <div className="md:hidden space-y-2">
        {rows.length === 0 && !loading && (
          <div className="text-center text-muted-foreground py-8 text-[13px]">데이터 없음</div>
        )}
        {rows.map((r, i) => {
          const hasContainer = r.containers && r.containers.length > 0
          const externalRisk = r.firewall && r.firewall.scope === 'Anywhere' && hasContainer
          const exposedNoRule = !r.firewall && !hasContainer && r.process
          const borderClass = externalRisk
            ? 'border-l-2 border-amber-500'
            : exposedNoRule
            ? 'border-l-2 border-destructive'
            : ''
          return (
            <div key={i} className={`bg-card rounded-xl p-3 card-shadow space-y-1 ${borderClass}`}>
              <div className="flex justify-between text-[12px]">
                <span className="text-muted-foreground">포트</span>
                <span className="font-mono">{r.port}</span>
              </div>
              <div className="flex justify-between text-[12px]">
                <span className="text-muted-foreground">프로토콜</span>
                <Badge variant="outline" className="text-[10px]">
                  {r.proto.toUpperCase()}
                </Badge>
              </div>
              <div className="flex justify-between text-[12px]">
                <span className="text-muted-foreground">상태</span>
                <Badge
                  variant={r.state === 'listening' ? 'default' : 'secondary'}
                  className="text-[10px]"
                >
                  {r.state === 'listening' ? 'LISTENING' : 'BOUND'}
                </Badge>
              </div>
              <div className="flex justify-between gap-2 text-[12px]">
                <span className="text-muted-foreground shrink-0">방화벽</span>
                {r.firewall ? (
                  <span className="inline-flex items-center gap-1 text-right">
                    <span
                      className={
                        r.firewall.action === 'DENY' || r.firewall.action === 'REJECT'
                          ? 'text-destructive'
                          : 'text-emerald-600'
                      }
                    >
                      {r.firewall.action}
                    </span>
                    <span className="text-muted-foreground">{r.firewall.scope}</span>
                  </span>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </div>
              <div className="flex justify-between gap-2 text-[12px]">
                <span className="text-muted-foreground shrink-0">컨테이너</span>
                {r.containers && r.containers.length > 0 ? (
                  <div className="flex flex-col items-end gap-0.5">
                    {r.containers.map((c, idx) => (
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
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </div>
              <div className="flex justify-between gap-2 text-[12px]">
                <span className="text-muted-foreground shrink-0">프로세스</span>
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
          )
        })}
      </div>
    </div>
  )
}
