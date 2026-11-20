import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import { lastSeenAgo } from '@/lib/clusterNodes'
import type { AttentionItem, Problem } from '@/lib/running'
import { Heading, type HeadingLevel } from './Heading'
import { NodeLink } from './NodeLink'
import { ageText } from './words'
import { DOWN_NODES, containerTarget, stackTarget, type Scope, type Target } from './targets'

const linkClass = 'font-semibold text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm'

function subjectName(item: AttentionItem): string {
  if (item.subject.kind === 'node') return item.nodeName
  return item.subject.kind === 'stack' ? item.subject.stack.name : item.subject.container.name
}

/** What needs looking at, worst first; each line says what, where, why, and leads there. */
export function AttentionList({ items, scope, localId, level }: { items: AttentionItem[]; scope: Scope; localId: string; level: HeadingLevel }) {
  const { t, i18n } = useTranslation()
  const headingId = useId()
  if (items.length === 0) return null

  // Inside a stack's line the container is named; on its own line it is the subject.
  const problemText = (p: Problem, named: boolean) => {
    const name = p.container.service
    if (p.kind === 'exited') {
      const values = { name, code: p.code, ago: ageText(p.age, i18n.language) }
      return named ? t('running.attention.exited', values) : t('running.attention.exitedSelf', values)
    }
    if (p.kind === 'restarting') return named ? t('running.attention.restarting', { name }) : t('running.attention.restartingSelf')
    if (p.kind === 'unhealthy') return named ? t('running.attention.unhealthy', { name }) : t('running.attention.unhealthySelf')
    return named ? t('running.attention.dead', { name }) : t('running.attention.deadSelf')
  }

  const why = (item: AttentionItem): string[] => {
    const s = item.subject
    if (s.kind === 'node') {
      const status = {
        online: t('cluster.status.online'),
        suspect: t('cluster.status.suspect'),
        offline: t('cluster.status.offline'),
        joining: t('cluster.status.joining'),
      }[s.status]
      const ago = s.lastSeen ? lastSeenAgo(s.lastSeen, i18n.language) : ''
      return [ago ? t('running.attention.nodeDown', { status, ago }) : t('running.attention.nodeDownNever', { status })]
    }
    return [
      ...(item.partial ? [t('running.attention.partial', item.partial)] : []),
      ...item.problems.map((p) => problemText(p, s.kind === 'stack')),
    ]
  }

  const link = (item: AttentionItem): { target: Target; text: string } => {
    if (item.opens.kind === 'nodes') return { target: DOWN_NODES, text: t('running.attention.openNodes') }
    if (item.opens.kind === 'stack') return { target: stackTarget(scope, item.nodeId, item.opens.stack), text: t('running.attention.openStack') }
    return { target: containerTarget(scope, item.opens.container.id, true), text: t('running.attention.openLogs') }
  }

  return (
    <section aria-labelledby={headingId} className="rounded-2xl border border-destructive/30 bg-card p-4 card-shadow">
      <Heading level={level} id={headingId} className="flex items-center gap-2 text-sm font-semibold text-destructive">
        <AlertTriangle className="size-4 shrink-0" aria-hidden="true" />
        {t('running.attention.title', { count: items.length })}
      </Heading>
      <ul className="mt-2 space-y-2 text-[13px]">
        {items.map((item) => {
          const name = subjectName(item)
          const to = link(item)
          return (
            <li key={`${item.nodeId}/${item.subject.kind}/${name}`} className="break-words">
              <strong className="font-semibold">{name}</strong>
              {scope === 'cluster' && item.subject.kind !== 'node' && <span className="text-muted-foreground"> · {item.nodeName}</span>}
              {' — '}
              {why(item).join(' ')}{' '}
              <NodeLink target={to.target} nodeId={item.nodeId} localId={localId} className={linkClass}>
                {to.text}<span className="sr-only"> ({name})</span>
              </NodeLink>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
