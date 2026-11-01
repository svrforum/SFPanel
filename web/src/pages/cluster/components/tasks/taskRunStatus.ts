import type { TFunction } from 'i18next'
import type { StatusPillTone } from '@/components/StatusPill'
import type { RunStatus } from '@/types/api'

// A run's status as the screen words and colours it. Literal keys, so
// locales.test sees every one.

const TEXT: Record<RunStatus, (t: TFunction) => string> = {
  running: (t) => t('cluster.tasks.runStatus.running'),
  stopping: (t) => t('cluster.tasks.runStatus.stopping'),
  handing_over: (t) => t('cluster.tasks.runStatus.handingOver'),
  succeeded: (t) => t('cluster.tasks.runStatus.succeeded'),
  failed: (t) => t('cluster.tasks.runStatus.failed'),
  stopped: (t) => t('cluster.tasks.runStatus.stopped'),
}

const TONE: Record<RunStatus, StatusPillTone> = {
  running: 'primary',
  stopping: 'warning',
  handing_over: 'warning',
  succeeded: 'success',
  failed: 'destructive',
  stopped: 'muted',
}

/** The status in the screen's words; one this screen does not know, as sent. */
export function runStatusText(status: RunStatus, t: TFunction): string {
  return Object.hasOwn(TEXT, status) ? TEXT[status](t) : status
}

export function runStatusTone(status: RunStatus): StatusPillTone {
  return Object.hasOwn(TONE, status) ? TONE[status] : 'muted'
}
