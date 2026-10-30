import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { CheckCircle2, XCircle, Clock, AlertTriangle, MinusCircle, ArrowRightLeft, Loader2, RotateCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { ClusterUpdateEvent } from '@/types/api'
import { reduceUpdateEvents } from './updateProgress'

function stepIcon(step: string) {
  if (step === 'online' || step === 'complete') return <CheckCircle2 className="h-4 w-4 text-success" />
  if (step === 'error') return <XCircle className="h-4 w-4 text-destructive" />
  if (step === 'warning') return <AlertTriangle className="h-4 w-4 text-warning" />
  if (step === 'skipped') return <MinusCircle className="h-4 w-4 text-muted-foreground" />
  if (step === 'waiting') return <Clock className="h-4 w-4 text-warning" />
  if (step === 'transfer') return <ArrowRightLeft className="h-4 w-4 text-primary" />
  return <Loader2 className="h-4 w-4 text-primary animate-spin" />
}

// Renders the cluster-update stream as a per-node stepper under the run's
// target, its current wave and, when it stopped, why.
export function ClusterUpdateProgress({ events }: { events: ClusterUpdateEvent[] }) {
  const { t } = useTranslation()
  const run = useMemo(() => reduceUpdateEvents(events), [events])

  // An older leader still sends "complete" and "warning" steps.
  const stepLabel = (step: string) => ({
    updating: t('cluster.overview.step.updating'),
    waiting: t('cluster.overview.step.waiting'),
    transfer: t('cluster.overview.step.transfer'),
    complete: t('cluster.overview.step.complete'),
    online: t('cluster.overview.step.online'),
    error: t('cluster.overview.step.error'),
    warning: t('cluster.overview.step.warning'),
    skipped: t('cluster.overview.step.skipped'),
  } as Record<string, string>)[step] ?? step

  return (
    <div className="bg-card rounded-2xl p-5 card-shadow" data-testid="update-progress">
      <div className="flex items-start justify-between gap-3 mb-3">
        <div className="min-w-0">
          <h3 className="text-[15px] font-semibold">{t('cluster.overview.updateProgress')}</h3>
          {run.target && (
            <p className="text-[12px] text-muted-foreground">{t('cluster.overview.updateTarget', { version: run.target })}</p>
          )}
          {run.wave !== undefined && run.overall !== 'complete' && (
            <p className="text-[12px] text-muted-foreground break-words">
              {t('cluster.overview.wave', { wave: run.wave, waves: run.waves ?? run.wave, nodes: run.waveNodes.join(', ') })}
            </p>
          )}
        </div>
        <span className="text-[12px] text-muted-foreground tabular-nums shrink-0" data-testid="update-count">
          {run.done} / {run.total}
          {run.failed > 0 && (
            <span className="text-destructive ml-2">· {t('cluster.overview.updateFailed', { count: run.failed })}</span>
          )}
        </span>
      </div>
      {/* Overall progress bar */}
      <div className="h-1.5 bg-muted rounded-full overflow-hidden mb-4">
        <div
          className={cn('h-full rounded-full transition-all duration-500',
            run.overall === 'error' ? 'bg-destructive' : 'bg-primary')}
          style={{ width: `${run.total > 0 ? Math.min(100, (run.done / run.total) * 100) : 0}%` }}
        />
      </div>
      {run.stopReason && (
        <div role="alert" className="mb-3 rounded-xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-[13px] text-destructive">
          <p className="font-semibold">{t('cluster.overview.stopped')}</p>
          <p className="break-words">{run.stopReason}</p>
        </div>
      )}
      {/* Per-node stepper */}
      <div className="space-y-2">
        {run.nodes.map((n) => (
          <div key={n.id} className="flex items-center gap-3">
            <span className="shrink-0">{stepIcon(n.step)}</span>
            <span className="text-[13px] font-medium w-40 truncate shrink-0">{n.name}</span>
            <span className="text-[12px] text-muted-foreground truncate">
              {stepLabel(n.step)}
              {n.message ? ` — ${n.message}` : ''}
            </span>
          </div>
        ))}
        {run.overall === 'complete' && (
          <div className="flex items-center gap-3 pt-1">
            <CheckCircle2 className="h-4 w-4 text-success shrink-0" />
            <span className="text-[13px] font-semibold">{t('cluster.overview.updateComplete')}</span>
          </div>
        )}
        {run.overall === 'complete' && run.leaderRestarting && (
          <div className="flex items-center gap-3">
            <RotateCw className="h-4 w-4 text-primary shrink-0" />
            <span className="text-[13px] text-muted-foreground">{t('cluster.overview.leaderRestarting')}</span>
          </div>
        )}
      </div>
    </div>
  )
}
