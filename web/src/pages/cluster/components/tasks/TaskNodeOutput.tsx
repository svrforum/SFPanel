import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, X } from 'lucide-react'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { Button } from '@/components/ui/button'
import { StatusPill } from '@/components/StatusPill'
import type { RunStepRecord } from '@/types/api'
import { cellState } from './taskRunProgress'
import { appendOutput, cellText, cellTone, emptyOutput, type OutputState } from './taskRunView'

const OUTPUT_POLL_MS = 2000

interface TaskNodeOutputProps {
  runId: string
  nodeId: string
  nodeName: string
  step: number
  stepName: string
  /** The step as the node recorded it; its job_id is what the output is read by. */
  record: RunStepRecord | undefined
  onClose: () => void
}

// One step's output on one node, read from the node every two seconds from
// where the last answer left off (next_skip) until the node says it is done.
// The holder keys it by node, step and job, so a new choice starts afresh.
export function TaskNodeOutput({ runId, nodeId, nodeName, step, stepName, record, onClose }: TaskNodeOutputProps) {
  const { t } = useTranslation()
  const [out, setOut] = useState<OutputState>(emptyOutput)
  const [error, setError] = useState<string | null>(null)
  const current = useRef<OutputState>(emptyOutput)
  const asking = useRef(false)
  const stopped = useRef(false)
  const live = useRef(true)
  const box = useRef<HTMLPreElement>(null)
  const atEnd = useRef(true)
  const job = record?.job_id

  useEffect(() => {
    live.current = true
    return () => { live.current = false }
  }, [])

  // One request at a time: a slow node is not asked again before it answers.
  const poll = useCallback(() => {
    if (!job || asking.current || stopped.current || current.current.done) return
    asking.current = true
    api.getTaskStepOutput(runId, nodeId, step, current.current.skip).then(
      (view) => {
        if (!live.current) return
        current.current = appendOutput(current.current, view)
        setOut(current.current)
        setError(null)
      },
      (err) => {
        if (!live.current) return
        // A job the node no longer has will not come back; anything else
        // (a node rebooting, a manager busy) may.
        if ((err as { status?: number }).status === 404) stopped.current = true
        setError(taskErrorMessage(err, t))
      },
    ).finally(() => { asking.current = false })
  }, [job, runId, nodeId, step, t])
  useVisibleInterval(poll, OUTPUT_POLL_MS)

  // The box follows the newest line unless the operator has scrolled up.
  useLayoutEffect(() => {
    const el = box.current
    if (el && atEnd.current) el.scrollTop = el.scrollHeight
  }, [out.text])
  const onScroll = () => {
    const el = box.current
    if (el) atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
  }

  const cell = cellState(record)
  let note: string | null = null
  if (!job) {
    note = cell === 'pending' || cell === 'running' ? t('cluster.tasks.output.notStarted') : t('cluster.tasks.output.noJob')
  }

  return (
    <section data-testid="task-node-output" aria-labelledby={`task-node-output-${nodeId}-${step}`} className="rounded-xl border border-border p-3 space-y-2 min-w-0">
      <div className="flex items-start gap-2">
        <h3 id={`task-node-output-${nodeId}-${step}`} className="flex-1 min-w-0 text-[14px] font-semibold break-words">
          {t('cluster.tasks.output.title', { node: nodeName, step: stepName })}
        </h3>
        <StatusPill tone={cellTone(cell)}>{cellText(cell, t)}</StatusPill>
        <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" onClick={onClose} aria-label={t('cluster.tasks.output.close')}>
          <X className="h-4 w-4" />
        </Button>
      </div>
      {record?.summary && <p className="text-[12px] text-muted-foreground break-words">{record.summary}</p>}
      {note ? (
        <p className="text-[13px] text-muted-foreground">{note}</p>
      ) : (
        <>
          {out.trimmed && <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.output.trimmed')}</p>}
          <pre
            ref={box}
            onScroll={onScroll}
            data-testid="task-node-output-text"
            className="bg-zinc-950 text-zinc-100 rounded-xl p-3 text-[12px] leading-relaxed font-mono whitespace-pre-wrap break-all max-h-96 min-h-24 overflow-y-auto"
          >
            {out.text}
          </pre>
          {!out.done && !error && (
            <p className="flex items-center gap-1.5 text-[12px] text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              {t('cluster.tasks.output.following')}
            </p>
          )}
          {out.done && out.text === '' && <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.output.empty')}</p>}
        </>
      )}
      {error && <p role="alert" className="text-[12px] text-destructive break-words">{error}</p>}
    </section>
  )
}
