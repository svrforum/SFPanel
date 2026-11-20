import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUpRight } from 'lucide-react'
import { StatusPill } from '@/components/StatusPill'
import { cn } from '@/lib/utils'
import { containerProblem, type PortChip, type RunningContainer, type RunningGuest, type RunningStack } from '@/lib/running'
import { NodeLink } from './NodeLink'
import { containerTarget, nodeSectionId, stackTarget, type Scope } from './targets'
import type { GuestServer } from './useRunningBoard'
import { containerWord, exitText, guestMeta, guestWord, serviceNote, stackWord } from './words'

const chipBase = 'inline-flex min-h-8 items-center gap-1 rounded-lg px-2 text-xs'

/**
 * The published ports. A link opens the service in a new tab; a port bound to
 * loopback, a UDP port and one that does not speak HTTP are words only.
 */
export function PortChips({ ports, className }: { ports: PortChip[]; className?: string }) {
  const { t } = useTranslation()
  if (ports.length === 0) return null
  return (
    <ul aria-label={t('running.port.list')} className={cn('flex flex-wrap gap-1.5', className)}>
      {ports.map((p) => {
        const label = p.proto === 'tcp' ? String(p.port) : `${p.port}/${p.proto}`
        return (
          <li key={`${p.port}/${p.proto}`}>
            {p.href ? (
              <a
                href={p.href}
                target="_blank"
                rel="noopener noreferrer"
                className={cn(chipBase, 'bg-primary/10 font-semibold text-primary hover:bg-primary/15 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring')}
              >
                {label}
                <ArrowUpRight className="size-3.5" aria-hidden="true" />
                <span className="sr-only"> ({t('running.port.newTab')})</span>
              </a>
            ) : (
              <span className={cn(chipBase, 'bg-secondary text-muted-foreground')}>
                {p.internal ? t('running.port.internal', { port: label }) : label}
              </span>
            )}
          </li>
        )
      })}
    </ul>
  )
}

// The whole tile answers a click through its name's link, stretched over the
// tile; the port chips sit above that layer as links of their own.
const tileClass = 'relative flex min-w-0 flex-col gap-1.5 rounded-xl border border-border bg-card p-3'
const tileAttention = 'border-destructive/40 bg-destructive/5'
const nameLink = 'min-w-0 break-words text-[14px] font-semibold outline-none after:absolute after:inset-0 after:rounded-xl hover:underline focus-visible:after:ring-2 focus-visible:after:ring-ring'

export function StackTile({ stack, scope, nodeId, localId }: { stack: RunningStack; scope: Scope; nodeId: string; localId: string }) {
  const { t, i18n } = useTranslation()
  const problems = stack.containers.filter((c) => containerProblem(c) !== null)
  const attention = stack.state === 'partial' || problems.length > 0
  const word = stackWord(stack, attention, t)
  return (
    <li className={cn(tileClass, attention && tileAttention)}>
      <div className="flex items-start justify-between gap-2">
        <NodeLink target={stackTarget(scope, nodeId, stack)} nodeId={nodeId} localId={localId} className={nameLink}>{stack.name}</NodeLink>
        <StatusPill tone={word.tone} className="shrink-0">{word.text}</StatusPill>
      </div>
      <p className="break-words text-xs text-muted-foreground">
        {stack.services.length === 0 ? t('running.stack.noContainers') : stack.services.map((s, i) => {
          // A stack that is down says so in its pill: its services do not each
          // repeat "stopped", only what ended otherwise.
          const note = stack.state === 'stopped' && s.container.health === 'stopped' ? null : serviceNote(s.container, t, i18n.language)
          const problem = containerProblem(s.container) !== null
          return (
            <Fragment key={s.name}>
              {i > 0 && ' · '}
              <span className={cn(problem && 'font-semibold text-destructive')}>{note ? `${s.name} ${note}` : s.name}</span>
            </Fragment>
          )
        })}
      </p>
      <PortChips ports={stack.ports} className="relative z-10" />
    </li>
  )
}

export function ContainerTile({ container, scope, nodeId, localId }: { container: RunningContainer; scope: Scope; nodeId: string; localId: string }) {
  const { t, i18n } = useTranslation()
  const attention = containerProblem(container) !== null
  const word = containerWord(container, t)
  const exited = container.state.toLowerCase() === 'exited'
  return (
    <li className={cn(tileClass, attention && tileAttention)}>
      <div className="flex items-start justify-between gap-2">
        <NodeLink target={containerTarget(scope, container.id)} nodeId={nodeId} localId={localId} className={nameLink}>{container.name}</NodeLink>
        <StatusPill tone={word.tone} className="shrink-0">{word.text}</StatusPill>
      </div>
      <p className="break-all text-xs text-muted-foreground">
        {exited ? exitText(container, t, i18n.language) : t('running.image', { image: container.image })}
      </p>
      <PortChips ports={container.ports} className="relative z-10" />
    </li>
  )
}

const serverLink = 'inline-flex min-h-8 items-center break-words rounded-sm font-semibold text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

/**
 * A virtual machine or LXC container of a Proxmox node. The panel has no
 * screen of its own for a guest, so the tile is no link; a guest that is one
 * of the cluster's nodes links to that node — its section on the datacenter
 * board, its own board otherwise.
 */
export function GuestTile({ guest, server, scope, localId, onReveal }: {
  guest: RunningGuest
  server?: GuestServer
  scope: Scope
  localId: string
  /** Brings a node's section on this page into view and focus. */
  onReveal?: (nodeId: string) => void
}) {
  const { t } = useTranslation()
  const word = guestWord(guest, t)
  return (
    <li className={tileClass}>
      <div className="flex items-start justify-between gap-2">
        <p className="min-w-0 break-words text-[14px] font-semibold">{guest.title}</p>
        <StatusPill tone={word.tone} className="shrink-0">{word.text}</StatusPill>
      </div>
      <p className="break-words text-xs text-muted-foreground">{guestMeta(guest, t)}</p>
      {guest.tags.length > 0 && (
        <ul aria-label={t('running.guest.tags')} className="flex flex-wrap gap-1.5">
          {guest.tags.map((tag) => <li key={tag} className="break-all rounded-lg bg-secondary px-2 py-0.5 text-xs text-muted-foreground">{tag}</li>)}
        </ul>
      )}
      {server && (
        <p className="flex flex-wrap items-center gap-x-1.5 text-xs">
          <span className="text-muted-foreground">{t('running.guest.isNode')}</span>{' '}
          {scope === 'cluster' ? (
            <a
              href={`#${nodeSectionId(server.id)}`}
              className={serverLink}
              onClick={(e) => {
                if (!onReveal) return
                e.preventDefault()
                onReveal(server.id)
              }}
            >
              {server.name}
            </a>
          ) : (
            <NodeLink target={{ to: '/running', onNode: true }} nodeId={server.id} localId={localId} className={serverLink}>{server.name}</NodeLink>
          )}
        </p>
      )}
    </li>
  )
}
