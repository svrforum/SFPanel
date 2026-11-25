import { useId, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Check, Search } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { NodeStatusMark } from '@/components/cluster/NodeStatusMark'
import { sectionLabel } from '@/components/cluster/treeSection'
import { useServerScope } from '@/hooks/useServerScope'
import { isAgent, treeSections } from '@/lib/clusterNodes'
import { cn } from '@/lib/utils'
import type { ClusterNode } from '@/types/api'

/**
 * The cluster's servers to pick one from: the managers, then the agents by
 * group (treeSections), searchable from the start. Each row says the server's
 * state in words, not by its mark alone, and which one leads and which one
 * this panel runs on. An empty list says why: still coming, or not to be had —
 * never "no match" for a list that has not come.
 */
export function ServerList({ onPick, autoFocus }: { onPick: (id: string) => void; autoFocus?: boolean }) {
  const { t } = useTranslation()
  const { nodes, nodesError, status, localId, currentId } = useServerScope()
  const [query, setQuery] = useState('')
  const leaderId = status?.leader_id ?? ''
  const sections = useMemo(() => treeSections(nodes, leaderId, query), [nodes, leaderId, query])
  // A cluster of managers alone is one list without headings.
  const headed = nodes.some(isAgent)
  const idBase = useId()

  const row = (n: ClusterNode) => {
    const chosen = n.id === currentId
    const state = t(`cluster.status.${n.status}`, { defaultValue: n.status })
    const leader = n.id === leaderId && nodes.length > 1
    const attached = n.id === localId
    return (
      <button
        key={n.id}
        type="button"
        onClick={() => onPick(n.id)}
        aria-pressed={chosen}
        className={cn(
          'flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring',
          chosen ? 'bg-primary/10 font-medium text-primary' : 'text-foreground/80 hover:bg-accent',
        )}
      >
        <NodeStatusMark status={n.status} />
        <span className="truncate">{n.name}</span>
        {/* Online is the usual case: said to a screen reader only. */}
        <span className={n.status === 'online' ? 'sr-only' : 'shrink-0 text-[12px] text-muted-foreground'}>{state}</span>
        {(leader || attached) && (
          <span className="ml-auto flex shrink-0 items-center gap-1.5 text-[12px] text-muted-foreground">
            {leader && <span>{t('cluster.kind.leader')}</span>}
            {attached && <span>{t('layout.server.attached')}</span>}
          </span>
        )}
        {chosen && <Check aria-hidden="true" className={cn('h-4 w-4 shrink-0', !(leader || attached) && 'ml-auto')} />}
      </button>
    )
  }

  let body: ReactNode
  if (nodes.length === 0) {
    // Nothing has come yet. A failed ask says so above, once.
    body = nodesError ? null : <p role="status" className="px-2.5 py-2 text-[13px] text-muted-foreground">{t('common.loading')}</p>
  } else if (sections.length === 0) {
    body = <p role="status" className="px-2.5 py-2 text-[13px] text-muted-foreground">{t('layout.server.noMatch')}</p>
  } else {
    body = sections.map((section, i) => {
      if (!headed) return <div key={section.key}>{section.nodes.map(row)}</div>
      const headingId = `${idBase}-s${i}`
      return (
        <section key={section.key} aria-labelledby={headingId} className="pt-1 first:pt-0">
          <p id={headingId} className="px-2.5 pt-2 pb-1 text-[12px] font-semibold text-muted-foreground">{sectionLabel(section, t)}</p>
          {section.nodes.map(row)}
        </section>
      )
    })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="relative shrink-0 p-2">
        <Search aria-hidden="true" className="pointer-events-none absolute left-4.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('layout.server.search')}
          aria-label={t('layout.server.search')}
          autoFocus={autoFocus}
          className="h-9 rounded-lg border-0 bg-secondary/50 pl-8 text-base shadow-none md:text-[13px]"
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1 pb-1">
        {/* The list may be the last one that came, or none at all. */}
        {nodesError && (
          <p role="status" className="flex items-center gap-1.5 px-2.5 py-2 text-[13px] text-warning">
            <AlertTriangle aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
            <span>{t('layout.server.listUnavailable')}</span>
          </p>
        )}
        {body}
      </div>
    </div>
  )
}
