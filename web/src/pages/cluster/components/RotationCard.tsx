import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, X, type LucideIcon } from 'lucide-react'
import { StatusPill } from '@/components/StatusPill'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { RotationFailure } from '@/lib/clusterRotation'

/** What every rotation lists for a node: who it is, whether it is up, whether it can follow. */
export interface RotationRow {
  id: string
  name: string
  online: boolean
  can_rotate: boolean
}

export interface RotationColumn<R> {
  header: string
  cell: (row: R) => ReactNode
}

// One secret's card on the security page: where its rotation stands, whom it
// waits for, and each node's part in it. The leader's waiting lines are shown
// as sent — they carry the node's name and the reason.
export function RotationCard<R extends RotationRow>({ id, icon: Icon, title, summary, action, failure, waiting, columns, rows, children }: {
  /** data-testid suffix: rotation-<id>. */
  id: string
  icon: LucideIcon
  title: string
  summary?: ReactNode
  action?: ReactNode
  failure?: RotationFailure | null
  waiting?: string[]
  /** The columns between the name and online ones and "follows". */
  columns: RotationColumn<R>[]
  rows: R[]
  children?: ReactNode
}) {
  const { t } = useTranslation()

  const online = (row: R) => (
    <StatusPill tone={row.online ? 'success' : 'muted'}>
      {row.online ? t('cluster.status.online') : t('cluster.status.offline')}
    </StatusPill>
  )
  const follows = (row: R) =>
    row.can_rotate ? (
      <span className="inline-flex items-center gap-1 text-success">
        <Check className="h-4 w-4" aria-hidden />
        <span className="sr-only">{t('cluster.security.follows')}</span>
      </span>
    ) : (
      <span className="inline-flex items-center gap-1 text-destructive">
        <X className="h-4 w-4" aria-hidden />
        <span className="sr-only">{t('cluster.security.followsNot')}</span>
      </span>
    )

  return (
    <section data-testid={`rotation-${id}`} className="bg-card rounded-2xl card-shadow p-5 space-y-4" aria-label={title}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-2">
          <h2 className="text-[15px] font-semibold flex items-center gap-2">
            <Icon className="h-4 w-4 text-muted-foreground" aria-hidden />
            {title}
          </h2>
          {summary && <div className="flex flex-wrap items-center gap-2 text-[13px] text-muted-foreground">{summary}</div>}
        </div>
        {action}
      </div>

      {children}

      {waiting && waiting.length > 0 && (
        <div className="rounded-xl bg-warning/10 text-warning p-3 space-y-1">
          <p className="text-[11px] uppercase tracking-wider font-medium">{t('cluster.security.waiting')}</p>
          <ul aria-label={t('cluster.security.waiting')} className="text-[13px] space-y-0.5">
            {waiting.map((w) => <li key={w} className="break-words">{w}</li>)}
          </ul>
        </div>
      )}

      {failure && <RotationFailureNote failure={failure} />}

      {rows.length > 0 && (
        <>
          <div className="hidden md:block rounded-xl border border-border overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('cluster.nodes.name')}</TableHead>
                  <TableHead>{t('common.status')}</TableHead>
                  {columns.map((c) => <TableHead key={c.header}>{c.header}</TableHead>)}
                  <TableHead>{t('cluster.security.followsColumn')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row) => (
                  <TableRow key={row.id}>
                    <TableCell className="text-[13px] font-medium">{row.name}</TableCell>
                    <TableCell>{online(row)}</TableCell>
                    {columns.map((c) => <TableCell key={c.header} className="text-[13px]">{c.cell(row)}</TableCell>)}
                    <TableCell>{follows(row)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="md:hidden space-y-2">
            {rows.map((row) => (
              <div key={row.id} className="rounded-xl border border-border p-3 space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-[13px] font-medium truncate">{row.name}</span>
                  <div className="flex items-center gap-2 shrink-0">
                    {online(row)}
                    {follows(row)}
                  </div>
                </div>
                {columns.map((c) => (
                  <div key={c.header} className="flex items-center justify-between gap-2 text-[13px]">
                    <span className="text-muted-foreground">{c.header}</span>
                    <span className="min-w-0 text-right">{c.cell(row)}</span>
                  </div>
                ))}
              </div>
            ))}
          </div>
        </>
      )}
    </section>
  )
}

/** A rotation that did not start or stop: a refusal under its title, anything else as it came. */
export function RotationFailureNote({ failure }: { failure: RotationFailure }) {
  const { t } = useTranslation()
  return (
    <div role="alert" className="rounded-xl bg-destructive/10 p-3 text-destructive space-y-1">
      {failure.refused && <p className="text-[13px] font-medium">{t('cluster.security.refused')}</p>}
      <p className="text-[13px] break-words">{failure.message}</p>
    </div>
  )
}
