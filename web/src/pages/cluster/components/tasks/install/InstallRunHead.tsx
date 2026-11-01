import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Square } from 'lucide-react'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { formatDate } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { StatusPill } from '@/components/StatusPill'
import { useConfirm } from '@/components/ConfirmDialog'
import type { RunRecord } from '@/types/api'
import { builtinText } from '../taskBuiltins'
import { runStatusText, runStatusTone } from '../taskRunStatus'
import { installRunReasonText } from './installRun'

interface InstallRunHeadProps {
  run: RunRecord
  /** Takes the head a stop answered, ahead of the stream. */
  onHead: (run: RunRecord) => void
}

// An install's head: what it is, how many servers it takes and how many at
// once, how it stands, who started it, and why it ended when it says. An
// install under way can be stopped (never forced: the leader cleans up each
// server it stops); one that ended never runs again.
export function InstallRunHead({ run, onHead }: InstallRunHeadProps) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const reason = installRunReasonText(run.reason, t)
  const canStop = run.status === 'running' || run.status === 'handing_over'

  const stop = async () => {
    const yes = await confirm({
      title: t('cluster.tasks.install.run.stopTitle'),
      description: t('cluster.tasks.install.run.stopDescription'),
      confirmLabel: t('cluster.tasks.run.stop'),
    })
    if (!yes) return
    setBusy(true)
    setError(null)
    try {
      onHead(await api.stopTaskRun(run.id, false))
    } catch (err) {
      setError(taskErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h2 className="text-[18px] font-bold break-words min-w-0">{builtinText(t, run.task_id, 'name', run.task_name)}</h2>
        <span data-testid="task-run-status">
          <StatusPill tone={runStatusTone(run.status)}>{runStatusText(run.status, t)}</StatusPill>
        </span>
        <span data-testid="install-run-size" className="text-[13px] text-muted-foreground">
          {t('cluster.tasks.install.run.size', { count: run.nodes.length, batch: run.batch })}
        </span>
        {canStop && (
          <Button variant="outline" size="sm" className="ml-auto" onClick={stop} disabled={busy}>
            <Square className="h-4 w-4" />
            {t('cluster.tasks.run.stop')}
          </Button>
        )}
      </div>
      <dl className="flex flex-wrap gap-x-5 gap-y-1 text-[12px] text-muted-foreground">
        <div className="flex gap-1.5">
          <dt>{t('cluster.tasks.run.startedBy')}</dt>
          <dd className="text-foreground break-all">{run.started_by}</dd>
        </div>
        <div className="flex gap-1.5">
          <dt>{t('cluster.tasks.run.startedAt')}</dt>
          <dd className="text-foreground">{formatDate(run.started_at)}</dd>
        </div>
        {run.ended_at && (
          <div className="flex gap-1.5">
            <dt>{t('cluster.tasks.run.endedAt')}</dt>
            <dd className="text-foreground">{formatDate(run.ended_at)}</dd>
          </div>
        )}
      </dl>
      {reason && <p data-testid="install-run-reason" className="text-[12px] text-muted-foreground break-words">{reason}</p>}
      {error && <p role="alert" data-testid="task-run-action-error" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive break-words">{error}</p>}
    </div>
  )
}
