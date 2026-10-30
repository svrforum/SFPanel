import { useEffect, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { AlertTriangle, CheckCircle2, Loader2, X } from 'lucide-react'
import type { ClusterNode } from '@/types/api'
import { roleChangePhase, type RoleChange, type RoleChangeFailure } from '@/lib/clusterNodes'
import { useNow } from '@/hooks/useNow'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// One line per conversion or promotion the node list is following. The server
// said 202 and nothing more, so where the change stands is read from the list
// the page keeps fetching; the line stays after the end until it is closed, so
// the operator sees what to do next.
export function RoleChangeProgress({ nodeId, name, target, nodes, startedAt, onDone, onDismiss }: RoleChange & {
  nodes: ClusterNode[]
  /** Once, when the node is what it was asked to become. */
  onDone: (nodeId: string) => void
  onDismiss: (nodeId: string) => void
}) {
  const { t } = useTranslation()
  const now = useNow(1000)
  const phase = roleChangePhase({ nodeId, target, startedAt }, nodes, now)

  const told = useRef(false)
  useEffect(() => {
    if (phase !== 'done' || told.current) return
    told.current = true
    onDone(nodeId)
  }, [phase, nodeId, onDone])

  const toAgent = target === 'agent'
  let tone: 'running' | 'warning' | 'success' = 'running'
  let text: string
  let action: ReactNode = null
  if (phase === 'done') {
    tone = 'success'
    text = toAgent ? t('cluster.convert.doneCard', { name }) : t('cluster.promote.done', { name })
    // The node held the JWT key and the proxy secret until a moment ago.
    if (toAgent) {
      action = (
        <Button asChild size="sm" variant="outline" className="rounded-xl">
          <Link to="/cluster/security">{t('cluster.convert.toSecurity')}</Link>
        </Button>
      )
    }
  } else if (phase === 'raftPort') {
    tone = 'warning'
    text = t('cluster.promote.raftPort', { name })
  } else if (phase === 'timeout') {
    tone = 'warning'
    text = toAgent ? t('cluster.convert.timeout') : t('cluster.promote.timeout')
  } else {
    text = toAgent ? t('cluster.convert.started', { name }) : t('cluster.promote.started', { name })
  }

  const icon = {
    running: <Loader2 className="h-4 w-4 mt-0.5 shrink-0 animate-spin" aria-hidden />,
    warning: <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />,
    success: <CheckCircle2 className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />,
  }[tone]

  return (
    <div
      role="status"
      data-testid="role-change"
      className={cn(
        'rounded-xl p-3 flex items-start gap-2',
        tone === 'running' && 'bg-secondary/50',
        tone === 'warning' && 'bg-warning/10 text-warning',
        tone === 'success' && 'bg-success/10 text-success',
      )}
    >
      {icon}
      <div className="min-w-0 flex-1 space-y-2">
        <p className="text-[13px] font-medium break-words">{text}</p>
        {action}
      </div>
      {/* A change still under way has nothing to close: it ends by itself. */}
      {phase !== 'running' && (
        <Button variant="ghost" size="icon-xs" aria-label={t('common.close')} onClick={() => onDismiss(nodeId)}>
          <X className="h-4 w-4" />
        </Button>
      )}
    </div>
  )
}

/** A refused or unanswered conversion or promotion, inside its dialog. */
export function RoleChangeFailureNote({ failure, refusedTitle, noAnswer }: {
  failure: RoleChangeFailure
  refusedTitle: string
  noAnswer: string
}) {
  return (
    <div role="alert" className="rounded-xl bg-destructive/10 p-3 text-destructive space-y-1">
      {failure.kind === 'refused' && <p className="text-[13px] font-medium">{refusedTitle}</p>}
      {/* Without an answer the change may be under way: what to check, not the transport error. */}
      <p className="text-[13px] break-words">{failure.kind === 'noAnswer' ? noAnswer : failure.message}</p>
    </div>
  )
}
