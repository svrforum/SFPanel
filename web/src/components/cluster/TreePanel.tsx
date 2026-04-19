import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Crown, ChevronDown, ChevronRight, Server, LogOut, PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { ClusterNode, ClusterStatus } from '@/types/api'

export type TreeSelection =
  | { type: 'datacenter' }
  | { type: 'node'; nodeId: string }

interface TreePanelProps {
  clusterStatus: ClusterStatus
  nodes: ClusterNode[]
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

  const statusColor = (status: string) => {
    switch (status) {
      case 'online': return 'bg-[#00c471]'
      case 'suspect': return 'bg-[#f59e0b]'
      case 'offline': return 'bg-[#f04452]'
      default: return 'bg-muted-foreground'
    }
  }

  if (collapsed) {
    return (
      <div className="w-[52px] bg-card border-r border-border flex flex-col h-full shrink-0">
        <button
          onClick={onToggleCollapse}
          className="flex items-center justify-center py-3 hover:bg-accent transition-colors border-b border-border"
          title="Expand tree"
        >
          <PanelLeftOpen className="h-4 w-4 text-foreground/60" />
        </button>

        <div className="flex-1 flex flex-col items-center gap-1 px-1 py-2">
          {/* Datacenter icon */}
          <button
            onClick={() => onSelect({ type: 'datacenter' })}
            className={cn(
              'w-9 h-9 rounded-lg flex items-center justify-center transition-colors',
              isDatacenterSelected ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-accent'
            )}
            title={clusterStatus.name}
          >
            <Server className="h-4 w-4" />
          </button>

          {/* Node dots */}
          {nodes.map((node) => (
            <button
              key={node.id}
              onClick={() => onSelect({ type: 'node', nodeId: node.id })}
              className={cn(
                'w-9 h-9 rounded-lg flex items-center justify-center transition-colors',
                selection.type === 'node' && selection.nodeId === node.id
                  ? 'bg-primary/10'
                  : 'hover:bg-accent'
              )}
              title={node.name}
            >
              <span className={cn('h-2.5 w-2.5 rounded-full', statusColor(node.status))} />
            </button>
          ))}
        </div>

        <div className="shrink-0 border-t border-border py-2 flex flex-col items-center gap-1">
          <button onClick={onLogout} className="w-9 h-9 rounded-lg flex items-center justify-center text-muted-foreground hover:bg-accent transition-colors" title={t('layout.logout')}>
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
          <img src="/banner.png" alt="SFPanel" className="w-full h-auto" />
        </Link>
        <button onClick={onToggleCollapse} className="p-1.5 rounded-lg hover:bg-accent border border-border transition-colors shrink-0" title="Collapse tree">
          <PanelLeftClose className="h-4 w-4 text-foreground/60" />
        </button>
      </div>

      {/* Tree */}
      <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar px-2">
        <p className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider px-2 mb-1">
          {t('cluster.title')}
        </p>

        {/* Datacenter / cluster root */}
        <div className="flex items-center">
          <button
            onClick={() => setNodesExpanded(!nodesExpanded)}
            className="p-1 rounded hover:bg-accent transition-colors shrink-0"
          >
            {nodesExpanded ? <ChevronDown className="h-3 w-3 text-muted-foreground" /> : <ChevronRight className="h-3 w-3 text-muted-foreground" />}
          </button>
          <button
            onClick={() => onSelect({ type: 'datacenter' })}
            className={cn(
              'flex-1 flex items-center gap-2 px-1.5 py-1.5 rounded-lg text-[12px] font-semibold transition-colors',
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
        {nodesExpanded && <div className="ml-3 border-l border-border pl-1 mt-0.5">
          {nodes.map((node) => {
            const isSelected = selection.type === 'node' && selection.nodeId === node.id
            const isLeader = node.id === clusterStatus.leader_id
            const isLocal = node.id === localId

            return (
              <button
                key={node.id}
                onClick={() => onSelect({ type: 'node', nodeId: node.id })}
                className={cn(
                  'w-full flex items-center gap-2 px-2 py-1.5 rounded-lg text-[11px] transition-colors',
                  isSelected
                    ? 'bg-primary/10 text-primary font-semibold'
                    : 'text-foreground/80 hover:bg-accent'
                )}
              >
                <span className={cn('h-2 w-2 rounded-full shrink-0', statusColor(node.status))} />
                <span className="truncate">{node.name}</span>
                {isLeader && nodes.length > 1 && (
                  <Crown className="h-2.5 w-2.5 text-primary shrink-0 ml-auto" />
                )}
                {isLocal && !isLeader && (
                  <span className="text-[8px] text-muted-foreground ml-auto shrink-0">local</span>
                )}
              </button>
            )
          })}
        </div>}
      </div>

      {/* Bottom */}
      <div className="shrink-0 border-t border-border px-3 py-2">
        <div className="flex items-center justify-between">
          <span className="text-[10px] text-muted-foreground">
            {panelVersion ? `v${panelVersion}` : '...'}
          </span>
          <button
            onClick={onLogout}
            className="text-[10px] text-muted-foreground hover:text-foreground transition-colors"
            title={t('layout.logout')}
          >
            <LogOut className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>
    </div>
  )
}
