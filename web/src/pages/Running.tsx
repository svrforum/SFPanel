import { lazy, Suspense, useMemo, useState, type ReactNode } from 'react'
import { flushSync } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { RefreshCw, Search } from 'lucide-react'
import ResourceStatus from '@/components/ResourceStatus'
import { ListLoadState } from '@/components/ListLoadState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { filterContents, filterGuests, type Totals } from '@/lib/running'
import { cn } from '@/lib/utils'
import { AttentionList } from './running/AttentionList'
import { NetworkView } from './running/network/NetworkView'
import { RackView } from './running/rack/RackView'
import { Meters, NodeBody, NodeSection } from './running/NodeSection'
import type { HeadingLevel } from './running/Heading'
import { nodeSectionId, type Scope } from './running/targets'
import { useClusterBoard, useFolded, useNodeBoard, useRunningView, type BoardModel, type BoardSection, type RunningView } from './running/useRunningBoard'

// The canvas and its library are fetched only once the canvas is chosen.
import { CANVAS_AREA } from './running/canvasArea'

const RunningCanvas = lazy(() => import('./running/canvas/RunningCanvas'))

const REFRESH_MS = 15_000

// The running board: what runs, on one node (/running) or on every node of the
// cluster (/cluster/running), with what needs looking at first. It reads and
// links; it starts and stops nothing.
export default function Running({ scope = 'node' }: { scope?: 'node' | 'cluster' }) {
  return scope === 'cluster' ? <ClusterRunning /> : <NodeRunning />
}

function Stat({ label, value, testId, tone }: { label: string; value: string; testId: string; tone?: string }) {
  return (
    <div data-testid={testId} className="flex min-w-[6.5rem] flex-col-reverse rounded-2xl bg-card px-4 py-2.5 card-shadow">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={cn('text-xl font-bold tabular-nums', tone)}>{value}</dd>
    </div>
  )
}

function TotalsRow({ totals, showNodes, loaded }: { totals: Totals; showNodes: boolean; loaded: boolean }) {
  const { t } = useTranslation()
  // Before any node has answered there is nothing to count yet, which is not zero.
  const count = (n: number) => (loaded ? String(n) : '—')
  return (
    <div className="space-y-1.5">
      <dl className="flex flex-wrap gap-2">
        {showNodes && <Stat testId="total-nodes" label={t('running.totals.nodes')} value={`${totals.online} / ${totals.nodes}`} />}
        <Stat testId="total-stacks" label={t('running.totals.stacks')} value={count(totals.stacks)} />
        <Stat testId="total-running" label={t('running.totals.running')} value={count(totals.running)} />
        <Stat testId="total-attention" label={t('running.totals.attention')} value={String(totals.attention)} tone={totals.attention > 0 ? 'text-destructive' : undefined} />
      </dl>
      {totals.failed > 0 && <p className="text-xs text-muted-foreground">{t('running.totals.failed', { count: totals.failed })}</p>}
    </div>
  )
}

function SearchField({ value, onChange, network }: { value: string; onChange: (value: string) => void; network?: boolean }) {
  const { t } = useTranslation()
  return (
    <div className="relative sm:max-w-xs">
      <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
      <Input
        type="search"
        aria-label={t(network ? 'running.network.search.label' : 'running.search.label')}
        placeholder={t(network ? 'running.network.search.placeholder' : 'running.search.placeholder')}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-9 rounded-xl border-0 bg-secondary/50 pl-9 text-base md:text-[13px]"
      />
    </div>
  )
}

function NoMatch({ query, onClear }: { query: string; onClear: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="rounded-2xl bg-card p-4 text-center card-shadow">
      <p className="break-words text-[13px]">{t('running.search.noMatch', { query: query.trim() })}</p>
      <Button variant="outline" size="sm" className="mt-2 rounded-xl" onClick={onClear}>{t('running.search.clear')}</Button>
    </div>
  )
}

/** Whether a node has anything a search can look through: stacks or guests it has listed. */
const searchable = (s: BoardSection) => !!s.node.contents || s.guests?.load.phase === 'ready'

/** The tiles a query leaves of what one node runs, its guests included. */
function tileCount(s: BoardSection, query: string): number {
  const shown = s.node.contents ? filterContents(s.node.contents, query) : { stacks: [], standalone: [] }
  const guests = s.guests?.load.phase === 'ready' ? filterGuests(s.guests.guests, query).length : 0
  return shown.stacks.length + shown.standalone.length + guests
}

/** The tiles a query leaves on every loaded node together. */
function matches(board: BoardModel, query: string): number {
  return board.sections.reduce((sum, s) => sum + tileCount(s, query), 0)
}

/** Board, canvas, network or rack: a button each, the one in use pressed. */
function ViewSwitch({ view, onView }: { view: RunningView; onView: (view: RunningView) => void }) {
  const { t } = useTranslation()
  const option = (value: RunningView, label: string) => (
    <button
      type="button"
      aria-pressed={view === value}
      onClick={() => onView(value)}
      className={cn(
        'inline-flex min-h-8 items-center rounded-lg px-3 text-[13px] font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        view === value ? 'bg-card text-foreground card-shadow' : 'text-muted-foreground hover:text-foreground',
      )}
    >
      {label}
    </button>
  )
  return (
    <div role="group" aria-label={t('running.view.label')} className="inline-flex rounded-xl bg-secondary p-0.5">
      {option('board', t('running.view.board'))}
      {option('canvas', t('running.view.canvas'))}
      {option('network', t('running.view.network'))}
      {option('rack', t('running.view.rack'))}
    </div>
  )
}

/** The canvas, once its chunk has arrived; until then, a line that says it is coming. */
function Canvas({ sections, scope, query, storageKey, level }: {
  sections: BoardSection[]
  scope: Scope
  query: string
  storageKey: string
  level: HeadingLevel
}) {
  const { t } = useTranslation()
  return (
    <Suspense fallback={(
      <div className={cn(CANVAS_AREA, 'flex items-center justify-center rounded-2xl bg-card card-shadow')}>
        <p role="status" className="text-[13px] text-muted-foreground">{t('running.canvas.loading')}</p>
      </div>
    )}
    >
      {/* A new key is a new place to read the moved boxes from. */}
      <RunningCanvas key={storageKey} sections={sections} scope={scope} query={query} storageKey={storageKey} level={level} />
    </Suspense>
  )
}

/** The page's heading, what the page covers, when it was last heard from, the view and a refresh. */
function BoardHeader({ board, title, subtitle, onRefresh, view, onView }: {
  board: BoardModel
  title: string
  subtitle: string
  onRefresh: () => void
  view: RunningView
  onView: (view: RunningView) => void
}) {
  const { t } = useTranslation()
  return (
    <header className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-[22px] font-bold tracking-tight">{title}</h1>
        <div className="mt-1 flex flex-wrap items-center gap-x-2 text-[13px] text-muted-foreground">
          <p className="break-words">{subtitle}</p>
          <ResourceStatus resource={board.header} />
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <ViewSwitch view={view} onView={onView} />
        <Button variant="outline" size="sm" className="rounded-xl" onClick={onRefresh} disabled={board.header.loading}>
          <RefreshCw className={cn(board.header.loading && 'animate-spin motion-reduce:animate-none')} aria-hidden="true" />
          {t('running.refresh')}
        </Button>
      </div>
    </header>
  )
}

/**
 * The page's column. The board reads best at a column's width; the canvas is
 * fitted to its area, so it takes all the width there is — every pixel it is
 * given is a larger letter.
 */
function Frame({ wide, children }: { wide?: boolean; children: ReactNode }) {
  return <div className={cn('mx-auto space-y-4', !wide && 'max-w-[1400px]')}>{children}</div>
}

function ClusterRunning() {
  const { t } = useTranslation()
  const { state, refresh } = useClusterBoard()
  const [query, setQuery] = useState('')
  const [folded, toggleFold] = useFolded()
  const [view, setView] = useRunningView()
  // The network view's own reads are asked again only when the operator asks.
  const [netRound, setNetRound] = useState(0)
  useVisibleInterval(refresh, REFRESH_MS)

  if (state.phase === 'standalone') {
    return (
      <Frame>
        <h1 className="text-[22px] font-bold tracking-tight">{t('running.title')}</h1>
        <div className="rounded-2xl bg-card p-4 text-[13px] card-shadow">
          <p>{t('running.notCluster.body')}</p>
          <Link to="/running" className="mt-1 inline-flex min-h-11 items-center font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            {t('running.notCluster.link')}
          </Link>
        </div>
      </Frame>
    )
  }
  if (state.phase !== 'ready') {
    return (
      <Frame>
        <h1 className="text-[22px] font-bold tracking-tight">{t('running.title')}</h1>
        <ListLoadState loading={state.phase === 'loading'} error={state.phase === 'error' ? state.error : null} errorTitle={t('running.overviewError')} onRetry={refresh} skeletonCount={3} />
      </Frame>
    )
  }

  const { board } = state
  const searching = query.trim() !== ''
  // "Nothing matches" only when there was something to search: nodes that
  // failed or were not asked say so in their own sections.
  const noMatch = searching && board.sections.some(searchable) && matches(board, query) === 0
  // A guest that is one of the nodes leads to that node's section; a search
  // that hides the section is cleared first, so the link always arrives.
  const reveal = (nodeId: string) => {
    if (!document.getElementById(nodeSectionId(nodeId))) flushSync(() => setQuery(''))
    const section = document.getElementById(nodeSectionId(nodeId))
    if (!section) return
    section.scrollIntoView({ block: 'start' })
    section.querySelector<HTMLElement>('[data-node-toggle]')?.focus({ preventScroll: true })
  }
  if (view === 'rack') {
    return (
      <Frame wide>
        <BoardHeader board={board} title={t('running.title')} subtitle={t('running.subtitleCluster')} onRefresh={() => { refresh(); setNetRound((r) => r + 1) }} view={view} onView={setView} />
        <RackView sections={board.sections} round={netRound} level={2} />
      </Frame>
    )
  }
  if (view === 'network') {
    return (
      <Frame>
        <BoardHeader board={board} title={t('running.title')} subtitle={t('running.subtitleCluster')} onRefresh={() => { refresh(); setNetRound((r) => r + 1) }} view={view} onView={setView} />
        <SearchField value={query} onChange={setQuery} network />
        <NetworkView sections={board.sections} scope="cluster" query={query} round={netRound} level={2} onClearQuery={() => setQuery('')} />
      </Frame>
    )
  }
  if (view === 'canvas') {
    return (
      <Frame wide>
        <BoardHeader board={board} title={t('running.title')} subtitle={t('running.subtitleCluster')} onRefresh={refresh} view={view} onView={setView} />
        <SearchField value={query} onChange={setQuery} />
        {noMatch && <NoMatch query={query} onClear={() => setQuery('')} />}
        <Canvas sections={board.sections} scope="cluster" query={query} storageKey="sfpanel-running-canvas:cluster" level={2} />
      </Frame>
    )
  }
  return (
    <Frame>
      <BoardHeader board={board} title={t('running.title')} subtitle={t('running.subtitleCluster')} onRefresh={refresh} view={view} onView={setView} />
      <TotalsRow totals={board.totals} showNodes loaded={board.sections.some((s) => s.node.contents)} />
      <AttentionList items={board.items} scope="cluster" level={2} />
      <SearchField value={query} onChange={setQuery} />
      {noMatch && <NoMatch query={query} onClear={() => setQuery('')} />}
      {board.sections
        // A search keeps only the loaded nodes it found something on; one that
        // could not be searched, or whose guests could not be, still shows why.
        .filter((s) => !searching || !s.node.contents || s.guests?.load.phase === 'error' || tileCount(s, query) > 0)
        .map((s) => (
          <NodeSection
            key={s.node.id}
            section={s}
            folded={folded.has(s.node.id)}
            onToggle={() => toggleFold(s.node.id)}
            query={query}
            onReveal={reveal}
          />
        ))}
    </Frame>
  )
}

function NodeRunning() {
  const { t } = useTranslation()
  const { board, host, metrics, refresh } = useNodeBoard()
  const [query, setQuery] = useState('')
  const [view, setView] = useRunningView()
  const [netRound, setNetRound] = useState(0)
  useVisibleInterval(refresh, REFRESH_MS)
  const section = board.sections[0]
  const noMatch = query.trim() !== '' && searchable(section) && matches(board, query) === 0
  const subtitle = host ? t('running.subtitleNode', { host }) : t('running.subtitleThisNode')
  // The one box carries the node's numbers, which this scope has from the node itself.
  const boxed = useMemo(
    () => [metrics ? { ...section, metrics: { cpu: metrics.cpu, memory: metrics.mem_percent, disk: metrics.disk_percent } } : section],
    [section, metrics],
  )
  if (view === 'rack') {
    return (
      <Frame wide>
        <BoardHeader board={board} title={t('layout.tabs.runningHere')} subtitle={subtitle} onRefresh={() => { refresh(); setNetRound((r) => r + 1) }} view={view} onView={setView} />
        <RackView sections={board.sections} round={netRound} level={2} />
      </Frame>
    )
  }
  if (view === 'network') {
    return (
      <Frame>
        <BoardHeader board={board} title={t('layout.tabs.runningHere')} subtitle={subtitle} onRefresh={() => { refresh(); setNetRound((r) => r + 1) }} view={view} onView={setView} />
        <SearchField value={query} onChange={setQuery} network />
        <NetworkView sections={board.sections} scope="node" query={query} round={netRound} level={2} onClearQuery={() => setQuery('')} />
      </Frame>
    )
  }
  if (view === 'canvas') {
    return (
      <Frame wide>
        <BoardHeader board={board} title={t('layout.tabs.runningHere')} subtitle={subtitle} onRefresh={refresh} view={view} onView={setView} />
        <SearchField value={query} onChange={setQuery} />
        {noMatch && <NoMatch query={query} onClear={() => setQuery('')} />}
        <Canvas sections={boxed} scope="node" query={query} storageKey={`sfpanel-running-canvas:node:${section.node.id}`} level={2} />
      </Frame>
    )
  }
  return (
    <Frame>
      <BoardHeader board={board} title={t('layout.tabs.runningHere')} subtitle={subtitle} onRefresh={refresh} view={view} onView={setView} />
      {metrics && <Meters metrics={{ cpu: metrics.cpu, memory: metrics.mem_percent, disk: metrics.disk_percent }} />}
      <TotalsRow totals={board.totals} showNodes={false} loaded={!!section.node.contents} />
      <AttentionList items={board.items} scope="node" level={2} />
      <SearchField value={query} onChange={setQuery} />
      {noMatch ? <NoMatch query={query} onClear={() => setQuery('')} /> : (
        <div className="rounded-2xl bg-card pb-1 card-shadow">
          <NodeBody section={section} scope="node" query={query} groupLevel={2} />
        </div>
      )}
    </Frame>
  )
}
