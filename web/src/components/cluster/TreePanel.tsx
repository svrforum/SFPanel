import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Crown, ChevronDown, ChevronRight, Server, LogOut, PanelLeftClose, PanelLeftOpen, Coffee, AlertTriangle, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import GithubIcon from '@/components/GithubIcon'
import Logo from '@/components/Logo'
import { Input } from '@/components/ui/input'
import { cn, nodeStatusColor } from '@/lib/utils'
import { isAgent, treeSections } from '@/lib/clusterNodes'
import type { ClusterNode, ClusterStatus } from '@/types/api'
import { sectionLabel } from './treeSection'

export type TreeSelection =
  | { type: 'datacenter' }
  | { type: 'node'; nodeId: string }

/** The keys of the sections an operator folded, kept across reloads. */
const FOLDED_KEY = 'sfpanel-tree-collapsed'
/** Past this many nodes the tree offers a search. */
const SEARCH_FROM = 8

function readFolded(): string[] {
  try {
    const saved = JSON.parse(localStorage.getItem(FOLDED_KEY) ?? '[]')
    return Array.isArray(saved) ? saved.filter((k): k is string => typeof k === 'string') : []
  } catch {
    return []
  }
}

interface TreePanelProps {
  clusterStatus: ClusterStatus
  nodes: ClusterNode[]
  /** The node list call failed — what's shown may be stale or empty. */
  nodesError?: boolean
  localId: string
  selection: TreeSelection
  onSelect: (sel: TreeSelection) => void
  collapsed: boolean
  onToggleCollapse: () => void
  panelVersion: string
  onLogout: () => void
}

export default function TreePanel({
  clusterStatus,
  nodes,
  nodesError,
  localId,
  selection,
  onSelect,
  collapsed,
  onToggleCollapse,
  panelVersion,
  onLogout,
}: TreePanelProps) {
  const { t } = useTranslation()
  const isDatacenterSelected = selection.type === 'datacenter'
  const [nodesExpanded, setNodesExpanded] = useState(true)
  const [query, setQuery] = useState('')
  const [folded, setFolded] = useState(readFolded)
  const leaderId = clusterStatus.leader_id ?? ''

  // Managers, then agents by group (treeSections). The API derives the node
  // list from a Go map whose iteration order is random, so without a sort the
  // sidebar reshuffles between renders/pages.
  const sections = useMemo(() => treeSections(nodes, leaderId, query), [nodes, leaderId, query])
  // The rail has no room for headings or a search: every node, in tree order.
  const railSections = useMemo(() => treeSections(nodes, leaderId, ''), [nodes, leaderId])
  // A cluster of managers alone is drawn as it always was, without headings.
  const headed = nodes.some(isAgent)
  const searching = query.trim() !== ''

  const toggleFolded = (key: string) => {
    const next = folded.includes(key) ? folded.filter((k) => k !== key) : [...folded, key]
    setFolded(next)
    localStorage.setItem(FOLDED_KEY, JSON.stringify(next))
  }

  const renderNode = (node: ClusterNode) => {
    const isSelected = selection.type === 'node' && selection.nodeId === node.id
    const isLeader = node.id === leaderId
    const isLocal = node.id === localId

    return (
      <button
        key={node.id}
        onClick={() => onSelect({ type: 'node', nodeId: node.id })}
        aria-current={isSelected ? 'true' : undefined}
        className={cn(
          'w-full flex items-center gap-2 px-2 py-1.5 rounded-lg text-[11px] transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
          isSelected
            ? 'bg-primary/10 text-primary font-semibold'
            : 'text-foreground/80 hover:bg-accent'
        )}
      >
        <span className={cn('h-2 w-2 rounded-full shrink-0', nodeStatusColor(node.status))} />
        <span className="truncate" data-tree-node={node.id}>{node.name}</span>
        {isLeader && nodes.length > 1 && (
          <Crown className="h-2.5 w-2.5 text-primary shrink-0 ml-auto" />
        )}
        {isLocal && !isLeader && (
          <span className="text-[8px] text-muted-foreground ml-auto shrink-0">{t('cluster.tree.local')}</span>
        )}
      </button>
    )
  }

  if (collapsed) {
    return (
      <div className="w-[52px] bg-card border-r border-border flex flex-col h-full shrink-0">
        <button
          onClick={onToggleCollapse}
          className="flex items-center justify-center py-3 hover:bg-accent transition-colors border-b border-border outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
          title={t('layout.expand')}
          aria-label={t('layout.expand')}
        >
          <PanelLeftOpen className="h-4 w-4 text-foreground/60" />
        </button>

        {/* Scrolls on its own so that tens of nodes leave the sign-out below in view. */}
        <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar flex flex-col items-center gap-1 px-1 py-2">
          {nodesError && (
            <span className="py-1 text-warning shrink-0" title={t('cluster.tree.nodesUnavailable')}>
              <AlertTriangle className="h-3.5 w-3.5" />
            </span>
          )}

          {/* Datacenter icon */}
          <button
            onClick={() => onSelect({ type: 'datacenter' })}
            className={cn(
              'w-9 h-9 shrink-0 rounded-lg flex items-center justify-center transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
              isDatacenterSelected ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-accent'
            )}
            title={clusterStatus.name}
            aria-label={clusterStatus.name}
          >
            <Server className="h-4 w-4" />
          </button>

          {/* Node dots, a rule between sections */}
          {railSections.map((section, i) => (
            <div key={section.key} className="contents">
              {headed && i > 0 && <span aria-hidden="true" className="w-6 shrink-0 border-t border-border my-1" />}
              {section.nodes.map((node) => (
                <button
                  key={node.id}
                  onClick={() => onSelect({ type: 'node', nodeId: node.id })}
                  className={cn(
                    'w-9 h-9 shrink-0 rounded-lg flex items-center justify-center transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
                    selection.type === 'node' && selection.nodeId === node.id
                      ? 'bg-primary/10'
                      : 'hover:bg-accent'
                  )}
                  title={node.name}
                  aria-label={node.name}
                >
                  <span className={cn('h-2.5 w-2.5 rounded-full', nodeStatusColor(node.status))} />
                </button>
              ))}
            </div>
          ))}
        </div>

        <div className="shrink-0 border-t border-border py-2 flex flex-col items-center gap-1">
          <button onClick={onLogout} className="w-9 h-9 rounded-lg flex items-center justify-center text-muted-foreground hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0" title={t('layout.logout')} aria-label={t('layout.logout')}>
            <LogOut className="h-4 w-4" />
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="w-[180px] bg-card border-r border-border flex flex-col h-full shrink-0">
      {/* Banner link + collapse button */}
      <div className="px-3 py-3 flex items-center gap-2">
        <Link to="/dashboard" aria-label="SFPanel" className="flex-1 min-w-0 rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/30">
          <Logo />
        </Link>
        <button onClick={onToggleCollapse} className="p-1.5 rounded-lg hover:bg-accent border border-border transition-colors shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0" title={t('layout.collapse')} aria-label={t('layout.collapse')}>
          <PanelLeftClose className="h-4 w-4 text-foreground/60" />
        </button>
      </div>

      {nodes.length > SEARCH_FROM && (
        <div className="relative px-2 pb-2">
          <Search className="absolute left-4 top-[7px] h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
          <Input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t('cluster.tree.search')}
            aria-label={t('cluster.tree.search')}
            className="pl-7 h-7 rounded-lg bg-secondary/50 border-0 text-[11px] md:text-[11px] shadow-none"
          />
        </div>
      )}

      {/* Tree */}
      <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar px-2">
        <p className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider px-2 mb-1">
          {t('cluster.title')}
        </p>

        {/* Datacenter / cluster root */}
        <div className="flex items-center">
          <button
            onClick={() => setNodesExpanded(!nodesExpanded)}
            className="p-1 rounded hover:bg-accent transition-colors shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
            title={t('cluster.tree.toggleNodes')}
            aria-label={t('cluster.tree.toggleNodes')}
            aria-expanded={nodesExpanded}
          >
            {nodesExpanded ? <ChevronDown className="h-3 w-3 text-muted-foreground" /> : <ChevronRight className="h-3 w-3 text-muted-foreground" />}
          </button>
          <button
            onClick={() => onSelect({ type: 'datacenter' })}
            className={cn(
              'flex-1 flex items-center gap-2 px-1.5 py-1.5 rounded-lg text-[12px] font-semibold transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
              isDatacenterSelected
                ? 'bg-primary/10 text-primary'
                : 'text-foreground hover:bg-accent'
            )}
          >
            <Server className="h-3.5 w-3.5 shrink-0" />
            <span className="truncate">{clusterStatus.name}</span>
          </button>
        </div>

        {/* Node list — collapsible */}
        {(nodesExpanded || searching) && <div className="ml-3 border-l border-border pl-1 mt-0.5">
          {nodesError && (
            <p className="flex items-center gap-1.5 px-2 py-1.5 text-[10px] text-warning">
              <AlertTriangle className="h-3 w-3 shrink-0" />
              <span className="truncate">{t('cluster.tree.nodesUnavailable')}</span>
            </p>
          )}
          {!headed && sections.flatMap((section) => section.nodes).map(renderNode)}
          {headed && sections.map((section) => {
            const label = sectionLabel(section, t)
            // A search shows every match, folded or not.
            const open = searching || !folded.includes(section.key)
            return (
              <section key={section.key} aria-label={label} className="mt-1 first:mt-0">
                <button
                  onClick={() => toggleFolded(section.key)}
                  aria-expanded={open}
                  disabled={searching}
                  className="w-full flex items-center gap-1 px-1 py-1 rounded text-[10px] font-semibold text-muted-foreground enabled:hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                >
                  {open ? <ChevronDown className="h-3 w-3 shrink-0" /> : <ChevronRight className="h-3 w-3 shrink-0" />}
                  <span className="truncate">{label}</span>
                  <span aria-hidden="true" className="ml-auto shrink-0 font-normal tabular-nums">{section.nodes.length}</span>
                </button>
                {open && section.nodes.map(renderNode)}
              </section>
            )
          })}
          {searching && sections.length === 0 && (
            <p className="px-2 py-1.5 text-[10px] text-muted-foreground">{t('cluster.tree.noMatch')}</p>
          )}
        </div>}
      </div>

      {/* Bottom */}
      <div className="shrink-0 border-t border-border px-3 py-2 space-y-1.5">
        <div className="flex items-center justify-between">
          <span className="text-[10px] text-muted-foreground">
            {panelVersion ? `v${panelVersion}` : '...'}
          </span>
          <button
            onClick={onLogout}
            className="text-[10px] text-muted-foreground hover:text-foreground transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
            title={t('layout.logout')}
            aria-label={t('layout.logout')}
          >
            <LogOut className="h-3.5 w-3.5" />
          </button>
        </div>
        <div className="flex items-center gap-2.5">
          <a
            href="https://github.com/svrforum/SFPanel"
            target="_blank"
            rel="noopener noreferrer"
            className="text-muted-foreground hover:text-foreground transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
            title="GitHub"
            aria-label="GitHub"
          >
            <GithubIcon className="h-3 w-3" />
          </a>
          <a
            href="https://buymeacoffee.com/svrforum"
            target="_blank"
            rel="noopener noreferrer"
            className="text-muted-foreground hover:text-[#FFDD00] transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
            title="Buy me a coffee"
            aria-label="Buy me a coffee"
          >
            <Coffee className="h-3 w-3" />
          </a>
        </div>
      </div>
    </div>
  )
}
