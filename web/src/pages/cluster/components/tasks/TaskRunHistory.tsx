import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import type { RunRecord } from '@/types/api'
import { useNow } from '@/hooks/useNow'
import { relativeSince } from '@/lib/aiSessions'
import { formatDate } from '@/lib/utils'
import { ListLoadState } from '@/components/ListLoadState'
import PaginationRow from '@/components/PaginationRow'
import { StatusPill } from '@/components/StatusPill'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { builtinText } from './taskBuiltins'
import { runStatusText, runStatusTone } from './taskRunStatus'
import {
  HISTORY_PAGE_SIZE,
  durationText,
  filterRuns,
  historyPage,
  historyTasks,
  runDuration,
  type HistoryFilter,
  type HistoryStatus,
} from './taskHistory'

// Deliberately a native <select>, as the audit log's filter bar: two compact
// dropdowns, no popover chrome.
const selectCls = 'h-9 rounded-xl border border-border bg-card px-2 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring/40 min-w-0'

const runPath = (run: RunRecord) => `/cluster/tasks/runs/${encodeURIComponent(run.id)}`

// The status filter's options. Literal keys, so locales.test sees each one.
const STATUS_OPTIONS: { value: HistoryStatus; label: (t: TFunction) => string }[] = [
  { value: '', label: (t) => t('cluster.tasks.history.allStatuses') },
  { value: 'active', label: (t) => t('cluster.tasks.history.underWay') },
  { value: 'succeeded', label: (t) => runStatusText('succeeded', t) },
  { value: 'failed', label: (t) => runStatusText('failed', t) },
  { value: 'stopped', label: (t) => runStatusText('stopped', t) },
]

/**
 * Every run the cluster keeps (newest first, as the server lists them): the
 * 200 latest ended ones and every one under way. The list comes from the
 * Tasks tab, which polls it for its band, so the two never disagree and the
 * history refreshes with it. Filters and pages are the browser's: the
 * server has neither.
 */
export function TaskRunHistory({
  runs,
  error,
  onRetry,
}: {
  /** null until the first answer. */
  runs: RunRecord[] | null
  error: string | null
  onRetry: () => void
}) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const now = useNow(30_000)
  const [filter, setFilter] = useState<HistoryFilter>({ status: '', task: '' })
  const [page, setPage] = useState(1)

  const tasks = useMemo(() => {
    const list = historyTasks(runs ?? []).map((x) => ({ id: x.id, name: builtinText(t, x.id, 'name', x.name) }))
    // A task chosen whose last run has since left the history stays choosable.
    if (filter.task && !list.some((x) => x.id === filter.task)) list.push({ id: filter.task, name: filter.task })
    return list.sort((a, b) => a.name.localeCompare(b.name, i18n.language))
  }, [runs, filter.task, t, i18n.language])
  const shown = useMemo(() => filterRuns(runs ?? [], filter), [runs, filter])
  const cut = historyPage(shown, page)

  const choose = (next: Partial<HistoryFilter>) => {
    setFilter((f) => ({ ...f, ...next }))
    setPage(1)
  }

  const taskName = (run: RunRecord) => builtinText(t, run.task_id, 'name', run.task_name)
  const nodeCount = (run: RunRecord) => t('cluster.tasks.history.nodeCount', { count: run.nodes.length })
  const when = (run: RunRecord) => relativeSince(run.started_at, i18n.language, now) || formatDate(run.started_at)
  const took = (run: RunRecord) => {
    const ms = runDuration(run)
    if (ms !== null) return durationText(ms, t)
    return run.ended_at ? '—' : t('cluster.tasks.history.underWay')
  }
  const status = (run: RunRecord) => (
    <StatusPill tone={runStatusTone(run.status)}>{runStatusText(run.status, t)}</StatusPill>
  )

  let body
  if (runs === null) {
    body = (
      <ListLoadState
        loading={!error}
        error={error}
        errorTitle={t('cluster.tasks.history.loadError')}
        onRetry={onRetry}
        skeletonCount={6}
      />
    )
  } else if (runs.length === 0) {
    body = <p className="text-[13px] text-muted-foreground py-4">{t('cluster.tasks.history.empty')}</p>
  } else {
    body = (
      <>
        <div className="flex flex-wrap items-center gap-2">
          <select
            aria-label={t('cluster.tasks.history.statusFilter')}
            className={selectCls}
            value={filter.status}
            onChange={(e) => choose({ status: e.target.value as HistoryStatus })}
          >
            {STATUS_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>{o.label(t)}</option>
            ))}
          </select>
          <select
            aria-label={t('cluster.tasks.history.taskFilter')}
            className={`${selectCls} max-w-full`}
            value={filter.task}
            onChange={(e) => choose({ task: e.target.value })}
          >
            <option value="">{t('cluster.tasks.history.allTasks')}</option>
            {tasks.map((x) => (
              <option key={x.id} value={x.id}>{x.name}</option>
            ))}
          </select>
        </div>
        {shown.length === 0 ? (
          <p className="text-[13px] text-muted-foreground py-4">{t('cluster.tasks.history.noMatch')}</p>
        ) : null}
        {/* Desktop table: a row is a click anywhere, its name the link for keyboards. */}
        <div data-testid="task-history-table" className="hidden md:block">
          {shown.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="text-[11px]">{t('cluster.tasks.history.status')}</TableHead>
                  <TableHead className="text-[11px]">{t('cluster.tasks.history.task')}</TableHead>
                  <TableHead className="text-[11px]">{t('cluster.tasks.history.nodes')}</TableHead>
                  <TableHead className="text-[11px]">{t('cluster.tasks.history.startedBy')}</TableHead>
                  <TableHead className="text-[11px]">{t('cluster.tasks.history.startedAt')}</TableHead>
                  <TableHead className="text-[11px]">{t('cluster.tasks.history.took')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {cut.items.map((run) => (
                  <TableRow key={run.id} className="cursor-pointer" onClick={() => navigate(runPath(run))}>
                    <TableCell>{status(run)}</TableCell>
                    <TableCell className="text-[13px] max-w-[280px]">
                      <Link
                        to={runPath(run)}
                        onClick={(e) => e.stopPropagation()}
                        className="font-medium hover:underline underline-offset-2 break-words"
                      >
                        {taskName(run)}
                      </Link>
                    </TableCell>
                    <TableCell className="text-[12px] whitespace-nowrap">{nodeCount(run)}</TableCell>
                    <TableCell className="text-[12px] break-all">{run.started_by}</TableCell>
                    <TableCell className="text-[12px] text-muted-foreground whitespace-nowrap">
                      <span title={formatDate(run.started_at)}>{when(run)}</span>
                    </TableCell>
                    <TableCell className="text-[12px] text-muted-foreground whitespace-nowrap">{took(run)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
        {/* Phone cards: the whole card is the link. */}
        <ul className="md:hidden space-y-2">
          {cut.items.map((run) => (
            <li key={run.id}>
              <Link
                to={runPath(run)}
                data-testid="task-history-card"
                className="block rounded-xl border border-border p-3 space-y-1.5 hover:bg-secondary/40"
              >
                <div className="flex items-start justify-between gap-2">
                  <span className="text-[13px] font-medium break-words min-w-0">{taskName(run)}</span>
                  {status(run)}
                </div>
                <div className="flex flex-wrap gap-x-3 gap-y-0.5 text-[12px] text-muted-foreground">
                  <span>{nodeCount(run)}</span>
                  <span className="break-all">{run.started_by}</span>
                  <span title={formatDate(run.started_at)}>{when(run)}</span>
                  <span>{took(run)}</span>
                </div>
              </Link>
            </li>
          ))}
        </ul>
        <PaginationRow
          page={cut.page}
          total={shown.length}
          limit={HISTORY_PAGE_SIZE}
          label={t('cluster.tasks.history.page', { page: cut.page, total: cut.pages })}
          onPage={setPage}
        />
      </>
    )
  }

  return (
    <section data-testid="task-pane-history" className="rounded-2xl bg-card p-5 card-shadow space-y-3">
      <div>
        <h2 className="text-[18px] font-bold">{t('cluster.tasks.list.history')}</h2>
        <p className="text-[12px] text-muted-foreground mt-0.5">{t('cluster.tasks.history.kept')}</p>
      </div>
      {body}
    </section>
  )
}
