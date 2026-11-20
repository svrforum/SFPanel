import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { RefreshCw, Search } from 'lucide-react'
import ResourceStatus from '@/components/ResourceStatus'
import { ListLoadState } from '@/components/ListLoadState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { filterContents, type NodeContents, type Totals } from '@/lib/running'
import { cn } from '@/lib/utils'
import { AttentionList } from './running/AttentionList'
import { Heading } from './running/Heading'
import { Meters, NodeBody, NodeSection } from './running/NodeSection'
import { useClusterBoard, useFolded, useNodeBoard, type BoardModel } from './running/useRunningBoard'

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

function TotalsRow({ totals, showNodes }: { totals: Totals; showNodes: boolean }) {
  const { t } = useTranslation()
  return (
    <div className="space-y-1.5">
      <dl className="flex flex-wrap gap-2">
        {showNodes && <Stat testId="total-nodes" label={t('running.totals.nodes')} value={`${totals.online} / ${totals.nodes}`} />}
        <Stat testId="total-stacks" label={t('running.totals.stacks')} value={String(totals.stacks)} />
        <Stat testId="total-running" label={t('running.totals.running')} value={String(totals.running)} />
        <Stat testId="total-attention" label={t('running.totals.attention')} value={String(totals.attention)} tone={totals.attention > 0 ? 'text-destructive' : undefined} />
      </dl>
      {totals.failed > 0 && <p className="text-xs text-muted-foreground">{t('running.totals.failed', { count: totals.failed })}</p>}
    </div>
  )
}

function SearchField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { t } = useTranslation()
  return (
    <div className="relative sm:max-w-xs">
      <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
      <Input
        type="search"
        aria-label={t('running.search.label')}
        placeholder={t('running.search.placeholder')}
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

/** The tiles a query leaves of what one node runs. */
function tileCount(contents: NodeContents, query: string): number {
  const shown = filterContents(contents, query)
  return shown.stacks.length + shown.standalone.length
}

/** The tiles a query leaves on every loaded node together. */
function matches(board: BoardModel, query: string): number {
  return board.sections.reduce((sum, s) => sum + (s.node.contents ? tileCount(s.node.contents, query) : 0), 0)
}

/** Title, what the page covers, when it was last heard from, and a refresh. */
function BoardHeader({ board, level, subtitle, onRefresh }: { board: BoardModel; level: 1 | 2; subtitle: string; onRefresh: () => void }) {
  const { t } = useTranslation()
  return (
    <header className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <Heading level={level} className={level === 1 ? 'text-[22px] font-bold tracking-tight' : 'text-[17px] font-semibold'}>{t('running.title')}</Heading>
        <div className="mt-1 flex flex-wrap items-center gap-x-2 text-[13px] text-muted-foreground">
          <p className="break-words">{subtitle}</p>
          <ResourceStatus resource={board.header} />
        </div>
      </div>
      <Button variant="outline" size="sm" className="rounded-xl" onClick={onRefresh} disabled={board.header.loading}>
        <RefreshCw className={cn(board.header.loading && 'animate-spin motion-reduce:animate-none')} aria-hidden="true" />
        {t('running.refresh')}
      </Button>
    </header>
  )
}

function Frame({ children }: { children: ReactNode }) {
  return <div className="mx-auto max-w-[1400px] space-y-4">{children}</div>
}

function ClusterRunning() {
  const { t } = useTranslation()
  const { state, refresh } = useClusterBoard()
  const [query, setQuery] = useState('')
  const [folded, toggleFold] = useFolded()
  useVisibleInterval(refresh, REFRESH_MS)

  if (state.phase === 'standalone') {
    return (
      <div className="rounded-2xl bg-card p-4 text-[13px] card-shadow">
        <p>{t('running.notCluster.body')}</p>
        <Link to="/running" className="mt-1 inline-flex min-h-11 items-center font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          {t('running.notCluster.link')}
        </Link>
      </div>
    )
  }
  if (state.phase !== 'ready') {
    return (
      <Frame>
        <Heading level={2} className="text-[17px] font-semibold">{t('running.title')}</Heading>
        <ListLoadState loading={state.phase === 'loading'} error={state.phase === 'error' ? state.error : null} errorTitle={t('running.overviewError')} onRetry={refresh} skeletonCount={3} />
      </Frame>
    )
  }

  const { board } = state
  const searching = query.trim() !== ''
  // "Nothing matches" only when there was something to search: nodes that
  // failed or were not asked say so in their own sections.
  const noMatch = searching && board.sections.some((s) => s.node.contents) && matches(board, query) === 0
  return (
    <Frame>
      <BoardHeader board={board} level={2} subtitle={t('running.subtitleCluster')} onRefresh={refresh} />
      <TotalsRow totals={board.totals} showNodes />
      <AttentionList items={board.items} scope="cluster" localId={board.localId} level={3} />
      <SearchField value={query} onChange={setQuery} />
      {noMatch && <NoMatch query={query} onClear={() => setQuery('')} />}
      {board.sections
        // A search keeps only the loaded nodes it found something on; one that
        // could not be searched still shows why.
        .filter((s) => !searching || !s.node.contents || tileCount(s.node.contents, query) > 0)
        .map((s) => (
          <NodeSection
            key={s.node.id}
            section={s}
            folded={!searching && folded.has(s.node.id)}
            onToggle={() => toggleFold(s.node.id)}
            query={query}
            localId={board.localId}
          />
        ))}
    </Frame>
  )
}

function NodeRunning() {
  const { t } = useTranslation()
  const { board, host, metrics, refresh } = useNodeBoard()
  const [query, setQuery] = useState('')
  useVisibleInterval(refresh, REFRESH_MS)
  const section = board.sections[0]
  const noMatch = query.trim() !== '' && !!section.node.contents && matches(board, query) === 0
  return (
    <Frame>
      <BoardHeader board={board} level={1} subtitle={host ? t('running.subtitleNode', { host }) : t('running.subtitleThisNode')} onRefresh={refresh} />
      {metrics && <Meters metrics={{ cpu: metrics.cpu, memory: metrics.mem_percent, disk: metrics.disk_percent }} />}
      <TotalsRow totals={board.totals} showNodes={false} />
      <AttentionList items={board.items} scope="node" localId="" level={2} />
      <SearchField value={query} onChange={setQuery} />
      {noMatch ? <NoMatch query={query} onClear={() => setQuery('')} /> : (
        <div className="rounded-2xl bg-card pb-1 card-shadow">
          <NodeBody section={section} scope="node" query={query} localId="" groupLevel={2} />
        </div>
      )}
    </Frame>
  )
}
