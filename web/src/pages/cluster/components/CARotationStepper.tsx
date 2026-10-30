import { useTranslation } from 'react-i18next'
import { AlertTriangle, Check, ChevronRight, Clock, Loader2 } from 'lucide-react'
import type { CARotationView } from '@/types/api'
import { caCanAbort, caStepper, countdown } from '@/lib/clusterRotation'
import { useNow } from '@/hooks/useNow'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// Where a CA rotation stands: trust → reissue → promote → done, or the branch
// back to the current CA (rollback → drop → aborted) with why it went back.
// Phases are shown by the names the leader, the CLI and cluster-ops use.
// Before promote a phase runs out at its deadline, and the operator may stop it.
export function CARotationStepper({ rotation, onAbort, aborting }: {
  rotation: CARotationView
  onAbort: () => void
  aborting?: boolean
}) {
  const { t, i18n } = useTranslation()
  const now = useNow(1000)
  const { branch, steps, current } = caStepper(rotation.phase)
  const back = branch === 'back'
  const finished = !back && rotation.phase === 'done'
  const stoppable = caCanAbort(rotation.phase)
  const time = rotation.deadline
    ? new Date(rotation.deadline).toLocaleTimeString(i18n.language, { hour: '2-digit', minute: '2-digit' })
    : ''

  return (
    <div className={cn('rounded-xl p-3 space-y-3', back ? 'bg-warning/10' : 'bg-secondary/50')}>
      <ol aria-label={t('cluster.security.phase')} className="flex flex-wrap items-center gap-1.5">
        {steps.map((step, i) => {
          const passed = current >= 0 && i < current
          const here = i === current
          return (
            <li key={step} aria-current={here ? 'step' : undefined} className="inline-flex items-center gap-1.5">
              {i > 0 && <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" aria-hidden />}
              <span
                className={cn(
                  'inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[12px] font-mono',
                  here && back && 'bg-warning/20 text-warning font-semibold',
                  here && !back && finished && 'bg-success/10 text-success font-semibold',
                  here && !back && !finished && 'bg-primary/10 text-primary font-semibold',
                  !here && passed && !back && 'text-success',
                  !here && passed && back && 'text-warning/70',
                  !here && !passed && 'text-muted-foreground',
                )}
              >
                {here && back && <AlertTriangle className="h-3 w-3" aria-hidden />}
                {here && !back && !finished && <Loader2 className="h-3 w-3 animate-spin" aria-hidden />}
                {((passed && !back) || (here && finished)) && <Check className="h-3 w-3" aria-hidden />}
                {step}
              </span>
            </li>
          )
        })}
        {/* A phase a newer leader knows and this page does not: shown as sent. */}
        {current < 0 && (
          <li aria-current="step" className="inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-mono bg-primary/10 text-primary font-semibold ml-1">
            {rotation.phase}
          </li>
        )}
      </ol>

      {rotation.reason && (
        <p className="text-[13px] text-warning break-words">
          <span className="font-medium">{t('cluster.security.reason')}:</span> {rotation.reason}
        </p>
      )}

      {stoppable && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          {rotation.deadline ? (
            <p className="text-[13px] flex items-center gap-2">
              <Clock className="h-4 w-4 text-muted-foreground shrink-0" aria-hidden />
              <span>{t('cluster.security.deadline', { time })}</span>
              <span data-testid="countdown" className="font-mono tabular-nums text-muted-foreground">
                {countdown(rotation.deadline, now)}
              </span>
            </p>
          ) : <span />}
          <Button size="sm" variant="outline" className="rounded-xl" onClick={onAbort} loading={aborting}>
            {t('cluster.security.abort')}
          </Button>
        </div>
      )}
    </div>
  )
}
