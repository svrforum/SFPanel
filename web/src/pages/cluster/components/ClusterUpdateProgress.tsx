import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { CheckCircle2, XCircle, Clock, AlertTriangle, MinusCircle, ArrowRightLeft, Loader2 } from 'lucide-react'
import { cn } from '@/lib/utils'

// One event from the cluster-update SSE stream. Either a per-node step event
// (node_id + step) or an overall lifecycle event (overall).
export type UpdateEvent = {
  node_id?: string
  node_name?: string
  step?: string
  message?: string
  overall?: string
  total_nodes?: number
  updated?: number
  failed?: number
  mode?: string
}

// Per-node update state, reduced from the event stream (latest step wins).
type NodeUpdateState = { node_id: string; node_name: string; step: string; message: string }

// Terminal "done" steps drive the completed count; error is its own bucket.
// Remaining steps (updating/waiting/transfer/warning/skipped) render as in-flight
// or soft-fail in the stepper.
const ERROR_STEPS = new Set(['error'])
const DONE_STEPS = new Set(['complete', 'online'])

function stepIcon(step: string) {
  if (DONE_STEPS.has(step)) return <CheckCircle2 className="h-4 w-4 text-success" />
  if (ERROR_STEPS.has(step)) return <XCircle className="h-4 w-4 text-destructive" />
  if (step === 'warning') return <AlertTriangle className="h-4 w-4 text-warning" />
  if (step === 'skipped') return <MinusCircle className="h-4 w-4 text-muted-foreground" />
  if (step === 'waiting') return <Clock className="h-4 w-4 text-warning" />
  if (step === 'transfer') return <ArrowRightLeft className="h-4 w-4 text-primary" />
  return <Loader2 className="h-4 w-4 text-primary animate-spin" />
}

// Reduces the raw cluster-update SSE events into per-node state + an overall
// summary and renders them as a per-node stepper (progress bar + step icons)
// instead of a flat scrolling log.
export function ClusterUpdateProgress({ updateLog }: { updateLog: UpdateEvent[] }) {
  const { t } = useTranslation()

  const updateProgress = useMemo(() => {
    const byNode = new Map<string, NodeUpdateState>()
    let total = 0
    let overall = ''
    let done = 0
    let failed = 0
    for (const e of updateLog) {
      if (e.overall) {
        overall = e.overall
        if (typeof e.total_nodes === 'number') total = e.total_nodes
        if (typeof e.updated === 'number') done = e.updated
        if (typeof e.failed === 'number') failed = e.failed
        continue
      }
      if (e.node_id) {
        byNode.set(e.node_id, {
          node_id: e.node_id,
          node_name: e.node_name || e.node_id,
          step: e.step || '',
          message: e.message || '',
        })
      }
    }
    const list = Array.from(byNode.values())
    const completed = overall === 'complete' || overall === 'error'
      ? done
      : list.filter(n => DONE_STEPS.has(n.step) || n.step === 'warning' || n.step === 'skipped').length
    return { list, total: total || list.length, completed, overall, failed }
  }, [updateLog])

  return (
    <div className="bg-card rounded-2xl p-5 card-shadow">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-[15px] font-semibold">{t('cluster.overview.updateProgress')}</h3>
        <span className="text-[12px] text-muted-foreground tabular-nums">
          {updateProgress.completed} / {updateProgress.total}
          {updateProgress.failed > 0 && (
            <span className="text-destructive ml-2">· {t('cluster.overview.updateFailed', { count: updateProgress.failed })}</span>
          )}
        </span>
      </div>
      {/* Overall progress bar */}
      <div className="h-1.5 bg-muted rounded-full overflow-hidden mb-4">
        <div
          className={cn('h-full rounded-full transition-all duration-500',
            updateProgress.overall === 'error' ? 'bg-destructive' : 'bg-primary')}
          style={{ width: `${updateProgress.total > 0 ? (updateProgress.completed / updateProgress.total) * 100 : 0}%` }}
        />
      </div>
      {/* Per-node stepper */}
      <div className="space-y-2">
        {updateProgress.list.map((n) => (
          <div key={n.node_id} className="flex items-center gap-3">
            <span className="shrink-0">{stepIcon(n.step)}</span>
            <span className="text-[13px] font-medium w-40 truncate shrink-0">{n.node_name}</span>
            <span className="text-[12px] text-muted-foreground truncate">
              {t(`cluster.overview.step.${n.step}`, { defaultValue: n.step })}
              {n.message ? ` — ${n.message}` : ''}
            </span>
          </div>
        ))}
        {updateProgress.overall === 'complete' && (
          <div className="flex items-center gap-3 pt-1">
            <CheckCircle2 className="h-4 w-4 text-success shrink-0" />
            <span className="text-[13px] font-semibold">{t('cluster.overview.updateComplete')}</span>
          </div>
        )}
      </div>
    </div>
  )
}
