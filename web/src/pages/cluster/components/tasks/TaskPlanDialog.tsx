import { useId } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { StatusPill } from '@/components/StatusPill'
import { cn } from '@/lib/utils'
import type { CheckVerdict, PlanNode, TaskPlan } from '@/types/api'
import { PVE_REBOOT_CODE, confirmSteps, leftReasonText, notReadyText, planBlocked, verdictSays, verdictText, type RunErrorText } from './taskPlan'

/** An input as the run fills it: the value given, else the default. */
export interface FilledInput {
  name: string
  label: string
  value: string
  isDefault: boolean
  raw: boolean
}

interface TaskPlanDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The task's name on screen. */
  name: string
  plan: TaskPlan
  inputs: FilledInput[]
  /** A step's name on screen, by its index from 0. */
  stepName: (step: number) => string
  confirmed: number[]
  onConfirmedChange: (confirmed: number[]) => void
  /** The plan was taken again because the start found it changed. */
  changed: boolean
  error: RunErrorText | null
  busy: boolean
  onRun: () => void
}

function verdictTone(v: CheckVerdict): string {
  if (v.code === PVE_REBOOT_CODE) return 'text-muted-foreground'
  if (v.refused) return 'text-destructive'
  if (v.needs_confirm) return 'text-warning'
  return 'text-muted-foreground'
}

function NodeVerdicts({ node, stepName }: { node: PlanNode; stepName: (step: number) => string }) {
  const { t } = useTranslation()
  const said = (node.verdicts ?? []).filter(verdictSays)
  return (
    <li className="px-3 py-2 space-y-1">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-[13px] font-medium break-all">{node.name}</span>
        {node.leader && <StatusPill tone="primary">{t('cluster.kind.leader')}</StatusPill>}
      </div>
      {node.check_error ? (
        <p className="text-[12px] text-destructive break-words">{t('cluster.tasks.plan.checkError', { error: node.check_error })}</p>
      ) : said.length === 0 ? (
        <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.plan.allFine')}</p>
      ) : (
        <ul className="space-y-0.5">
          {said.map((v) => (
            <li key={v.step} className={cn('text-[12px] break-words', verdictTone(v))}>
              <span className="font-medium">{stepName(v.step)}</span>: {verdictText(v, t)}
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

// The preview: what a run would do, asked of every node without running
// anything. The values as the run fills them, the nodes batch by batch with
// each node's verdicts, the nodes left out and why, one confirmation per step
// that needs one (it covers every node), and the managers a start would wait
// for. [Run] starts this plan: it is held back while a confirmation is
// unticked, a manager is not ready, or no node is left.
export function TaskPlanDialog({
  open, onOpenChange, name, plan, inputs, stepName, confirmed, onConfirmedChange, changed, error, busy, onRun,
}: TaskPlanDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const toConfirm = confirmSteps(plan)
  const waves = plan.waves ?? []
  const left = plan.left ?? []
  const waiting = plan.managers_not_ready ?? []
  const unconfirmed = toConfirm.some((s) => !confirmed.includes(s))
  const canRun = !busy && !planBlocked(plan) && !unconfirmed

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="break-words leading-snug">{t('cluster.tasks.plan.title', { name })}</DialogTitle>
          <DialogDescription>{t('cluster.tasks.plan.description')}</DialogDescription>
        </DialogHeader>

        {changed && (
          <p data-testid="plan-notice" role="status" className="rounded-xl bg-warning/10 px-3 py-2 text-[13px] text-warning">
            {t('cluster.tasks.plan.changed')}
          </p>
        )}

        {waiting.length > 0 && (
          <div data-testid="plan-managers" role="alert" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive space-y-1">
            <p className="flex items-start gap-2">
              <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />
              {t('cluster.tasks.plan.managersNotReady')}
            </p>
            <ul className="list-disc pl-9">
              {waiting.map((m) => <li key={m} className="break-words">{notReadyText(m, t)}</li>)}
            </ul>
          </div>
        )}

        {inputs.length > 0 && (
          <section aria-labelledby={`${id}-inputs`} className="space-y-1.5">
            <h3 id={`${id}-inputs`} className="text-[13px] font-semibold">{t('cluster.tasks.setup.inputs')}</h3>
            <dl data-testid="plan-inputs" className="rounded-xl border border-border divide-y divide-border">
              {inputs.map((input) => (
                <div key={input.name} className="px-3 py-2 sm:flex sm:gap-3">
                  <dt className="text-[12px] text-muted-foreground sm:w-40 shrink-0 break-words">{input.label}</dt>
                  <dd className="min-w-0 flex-1 text-[13px] space-x-1.5">
                    <span className="font-mono whitespace-pre-wrap break-all">{input.value === '' ? t('cluster.tasks.plan.empty') : input.value}</span>
                    {input.isDefault && <span className="text-[12px] text-muted-foreground">{t('cluster.tasks.plan.default')}</span>}
                    {input.raw && <StatusPill tone="warning">{t('cluster.tasks.setup.raw')}</StatusPill>}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        )}

        <section aria-labelledby={`${id}-waves`} className="space-y-1.5">
          <h3 id={`${id}-waves`} className="text-[13px] font-semibold">{t('cluster.tasks.plan.waves')}</h3>
          {waves.length === 0 && <p className="text-[13px] text-destructive">{t('cluster.tasks.errors.noTargets')}</p>}
          {waves.map((wave, i) => (
            <div key={i} data-testid="plan-wave" className="rounded-xl border border-border">
              <p className="px-3 py-1.5 text-[12px] font-medium bg-secondary/40 rounded-t-xl">{t('cluster.tasks.plan.wave', { n: i + 1 })}</p>
              <ul className="divide-y divide-border">
                {wave.map((node) => <NodeVerdicts key={node.id} node={node} stepName={stepName} />)}
              </ul>
            </div>
          ))}
        </section>

        {left.length > 0 && (
          <section aria-labelledby={`${id}-left`} className="space-y-1.5">
            <h3 id={`${id}-left`} className="text-[13px] font-semibold">{t('cluster.tasks.plan.left')}</h3>
            <ul data-testid="plan-left" className="rounded-xl border border-border divide-y divide-border">
              {left.map((node) => (
                <li key={node.id} className="flex flex-wrap items-center gap-x-2 gap-y-0.5 px-3 py-2 text-[13px]">
                  <span className="font-medium break-all">{node.name}</span>
                  <span className="text-muted-foreground">{leftReasonText(node.reason, t)}</span>
                  {node.reason === 'busy' && node.run_id && (
                    <Link to={`/cluster/tasks/runs/${encodeURIComponent(node.run_id)}`} className="font-mono text-[12px] underline underline-offset-2 hover:text-primary break-all">
                      {node.run_id}
                    </Link>
                  )}
                </li>
              ))}
            </ul>
          </section>
        )}

        {toConfirm.length > 0 && (
          <section aria-labelledby={`${id}-confirm`} className="space-y-1.5">
            <h3 id={`${id}-confirm`} className="text-[13px] font-semibold">{t('cluster.tasks.plan.confirm')}</h3>
            <ul className="space-y-2">
              {toConfirm.map((step) => {
                const box = `${id}-confirm-${step}`
                return (
                  <li key={step} className="flex items-start gap-2.5 rounded-xl bg-warning/10 px-3 py-2">
                    <Checkbox
                      id={box}
                      className="mt-0.5"
                      checked={confirmed.includes(step)}
                      disabled={busy}
                      onCheckedChange={(on) => onConfirmedChange(on === true ? [...confirmed, step].sort((a, b) => a - b) : confirmed.filter((s) => s !== step))}
                    />
                    <label htmlFor={box} className="text-[13px] break-words cursor-pointer">
                      {t('cluster.tasks.plan.confirmStep', { step: stepName(step) })}
                    </label>
                  </li>
                )
              })}
            </ul>
          </section>
        )}

        {error && (
          <div role="alert" data-testid="plan-error" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive space-y-1">
            <p className="break-words">{error.text}</p>
            {error.server && <p className="text-[12px] font-mono break-words opacity-80">{error.server}</p>}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>{t('common.cancel')}</Button>
          <Button onClick={onRun} disabled={!canRun}>
            {busy && <Loader2 className="h-4 w-4 animate-spin" />}
            {t('cluster.tasks.setup.run')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
