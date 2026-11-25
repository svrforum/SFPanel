import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, RefreshCw } from 'lucide-react'
import { ListLoadState } from '@/components/ListLoadState'
import { Skeleton } from '@/components/ui/skeleton'
import { lastSeenAgo } from '@/lib/clusterNodes'
import { filterContents, filterGuests } from '@/lib/running'
import { cn } from '@/lib/utils'
import { KindBadge } from '@/pages/cluster/components/KindBadge'
import { NodeStatus } from '@/pages/cluster/components/NodeCard'
import { Heading, type HeadingLevel } from './Heading'
import { HostListeners } from './HostListeners'
import { NodeLink } from './NodeLink'
import { ContainerTile, GuestTile, StackTile } from './Tiles'
import { nodeSectionId, type Scope } from './targets'
import type { BoardSection, GuestGroup, SectionMetrics } from './useRunningBoard'

// Columns as wide as a tile needs, one on a phone; min() keeps a single
// column from pushing past a screen narrower than the tile.
const grid = 'grid gap-2.5 grid-cols-[repeat(auto-fill,minmax(min(100%,15rem),1fr))]'

interface BodyProps {
  section: BoardSection
  scope: Scope
  query: string
  /** The level of the group headings ("스택 6"). */
  groupLevel: HeadingLevel
  /** Brings a node's section on this page into view: where a guest that is that node links. */
  onReveal?: (nodeId: string) => void
}

/**
 * A Proxmox node's virtual machines and LXC containers, above its stacks; or,
 * when they could not be listed, a line that says so and why — never as if
 * there were none.
 */
function GuestsGroup({ group, scope, query, level, onReveal }: {
  group: GuestGroup
  scope: Scope
  query: string
  level: HeadingLevel
  onReveal?: (nodeId: string) => void
}) {
  const { t, i18n } = useTranslation()
  const { load } = group
  if (load.phase === 'error') {
    return (
      <div className="px-4 pt-3 pb-1">
        <Heading level={level} className="text-xs font-semibold text-muted-foreground">{t('running.groups.guestsTitle')}</Heading>
        <p role="alert" className="mt-1 break-words text-xs text-destructive">{t('running.guest.loadError')} {load.error}</p>
      </div>
    )
  }
  const shown = filterGuests(group.guests, query)
  if (shown.length === 0) return null
  return (
    <div className="px-4 pt-3 pb-1">
      <Heading level={level} className="text-xs font-semibold text-muted-foreground">{t('running.groups.guests', { count: shown.length })}</Heading>
      {load.refreshError && (
        <p className="mt-1 text-xs text-destructive">{t('running.node.refreshError', { time: new Date(load.at).toLocaleTimeString(i18n.language) })}</p>
      )}
      <ul className={cn(grid, 'mt-2')}>
        {shown.map((g) => (
          <GuestTile key={g.vmid} guest={g} server={group.servers.get(g.vmid)} scope={scope} onReveal={onReveal} />
        ))}
      </ul>
    </div>
  )
}

/** What runs on one node, or why it cannot be shown. */
export function NodeBody({ section, scope, query, groupLevel, onReveal }: BodyProps) {
  const { t, i18n } = useTranslation()
  const { node, load } = section

  let content: ReactNode
  if (!load) {
    content = <p className="px-4 py-3 text-[13px] text-muted-foreground">{t('running.node.notAsked')}</p>
  } else if (load.phase === 'loading') {
    content = (
      <div role="status" aria-label={t('common.loading')} className="space-y-2 px-4 py-3">
        <Skeleton className="h-16 w-full rounded-xl" />
        <Skeleton className="h-16 w-full rounded-xl" />
      </div>
    )
  } else if (load.phase === 'error') {
    content = (
      <div className="px-4 py-3">
        <ListLoadState loading={false} error={load.error} errorTitle={t('running.node.loadError')} onRetry={section.retry} />
      </div>
    )
  } else if (load.phase === 'noDocker') {
    content = <p className="px-4 py-3 text-[13px] text-muted-foreground">{t('running.node.noDocker')}</p>
  } else if (node.contents) {
    const shown = filterContents(node.contents, query)
    const empty = node.contents.stacks.length === 0 && node.contents.standalone.length === 0
    content = (
      <>
        {load.refreshError && (
          <p className="flex flex-wrap items-center gap-x-2 px-4 pt-3 text-xs">
            <span className="text-destructive">{t('running.node.refreshError', { time: new Date(load.at).toLocaleTimeString(i18n.language) })}</span>
            <button type="button" onClick={section.retry} className="inline-flex min-h-11 items-center gap-1 rounded-md px-2 font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <RefreshCw className="size-3.5" aria-hidden="true" />{t('common.retry')}
            </button>
          </p>
        )}
        {shown.stacks.length > 0 && (
          <div className="px-4 pt-3 pb-1">
            <Heading level={groupLevel} className="text-xs font-semibold text-muted-foreground">{t('running.groups.stacks', { count: shown.stacks.length })}</Heading>
            <ul className={cn(grid, 'mt-2')}>
              {shown.stacks.map((s) => <StackTile key={`${s.managed ? 'managed' : 'label'}:${s.name}`} stack={s} scope={scope} nodeId={node.id} />)}
            </ul>
          </div>
        )}
        {shown.standalone.length > 0 && (
          <div className="px-4 pt-3 pb-1">
            <Heading level={groupLevel} className="text-xs font-semibold text-muted-foreground">{t('running.groups.standalone', { count: shown.standalone.length })}</Heading>
            <ul className={cn(grid, 'mt-2')}>
              {shown.standalone.map((c) => <ContainerTile key={c.id} container={c} scope={scope} nodeId={node.id} />)}
            </ul>
          </div>
        )}
        {empty && !section.guests && (
          <div className="px-4 py-6 text-center">
            <p className="text-[13px] font-medium">{t('running.empty.title')}</p>
            <p className="mt-1 text-xs text-muted-foreground">
              {t('running.empty.hint')}{' '}
              <NodeLink target={{ to: '/appstore', onNode: scope === 'cluster' }} nodeId={node.id} className="font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{t('running.empty.appStore')}</NodeLink>
              {' · '}
              <NodeLink target={{ to: '/docker/stacks', onNode: scope === 'cluster' }} nodeId={node.id} className="font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{t('running.empty.stacks')}</NodeLink>
            </p>
          </div>
        )}
      </>
    )
  }

  return (
    <>
      {section.guests && <GuestsGroup group={section.guests} scope={scope} query={query} level={groupLevel} onReveal={onReveal} />}
      {content}
      {/* Ports the host serves itself are a question for any node that answers, with Docker or without. */}
      {load && <div className="pt-2"><HostListeners node={section.nodeParam} /></div>}
    </>
  )
}

/** A node's CPU, memory and disk use, in words and numbers. */
export function Meters({ metrics, className }: { metrics: SectionMetrics; className?: string }) {
  const { t } = useTranslation()
  const rows = [
    { key: 'cpu', label: t('running.node.cpu'), value: metrics.cpu },
    { key: 'memory', label: t('running.node.memory'), value: metrics.memory },
    { key: 'disk', label: t('running.node.disk'), value: metrics.disk },
  ]
  return (
    <dl className={cn('flex flex-wrap gap-x-3 text-xs text-muted-foreground', className)}>
      {rows.map((r) => (
        <div key={r.key} className="flex gap-1">
          <dt>{r.label}</dt>
          <dd className="font-semibold text-foreground">{Math.round(r.value)}%</dd>
        </div>
      ))}
    </dl>
  )
}

/** One node of the cluster: its header, and what runs on it unless folded. */
export function NodeSection({ section, folded, onToggle, query, onReveal }: {
  section: BoardSection
  folded: boolean
  onToggle: () => void
  query: string
  onReveal?: (nodeId: string) => void
}) {
  const { t, i18n } = useTranslation()
  const headingId = useId()
  const bodyId = useId()
  const { node, kind, metrics } = section
  const ago = node.status !== 'online' && node.lastSeen ? lastSeenAgo(node.lastSeen, i18n.language) : ''
  return (
    <section id={nodeSectionId(node.id)} aria-labelledby={headingId} className="scroll-mt-4 rounded-2xl bg-card card-shadow">
      <header className={cn('flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2', !folded && 'border-b border-border')}>
        <Heading level={2} id={headingId} className="min-w-0 text-[15px] font-semibold">
          <button
            type="button"
            data-node-toggle
            aria-expanded={!folded}
            aria-controls={folded ? undefined : bodyId}
            onClick={onToggle}
            className="inline-flex min-h-11 max-w-full items-center gap-1.5 rounded-md text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <ChevronDown className={cn('size-4 shrink-0 transition-transform motion-reduce:transition-none', folded && '-rotate-90')} aria-hidden="true" />
            <span className="min-w-0 break-words">{node.name}</span>
          </button>
        </Heading>
        {kind && <KindBadge kind={kind} />}
        <NodeStatus status={node.status} />
        {ago && <span className="text-xs text-muted-foreground">{t('running.node.lastSeen', { ago })}</span>}
        {metrics && <Meters metrics={metrics} className="basis-full sm:ml-auto sm:basis-auto" />}
      </header>
      {!folded && (
        <div id={bodyId} className="pb-1">
          <NodeBody section={section} scope="cluster" query={query} groupLevel={3} onReveal={onReveal} />
        </div>
      )}
    </section>
  )
}
