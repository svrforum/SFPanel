import { useCallback, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { useTranslation } from 'react-i18next'
import { Server, Trash2, RefreshCw, Tag, ArrowRightLeft, AlertTriangle } from 'lucide-react'
import { api } from '@/lib/api'
import type { ClusterNode, ClusterStatus, ClusterNodeMetrics } from '@/types/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { ListLoadState } from '@/components/ListLoadState'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import {
  GROUP_LABEL,
  filterFromQuery,
  filterNodes,
  lastSeenAgo,
  nodeGroups,
  nodeRoleKind,
  referenceVersion,
  sortNodes,
  type NodeFilter,
} from '@/lib/clusterNodes'
import { NodeLabelsDialog } from './components/NodeLabelsDialog'
import { KindBadge } from './components/KindBadge'
import { NodeFilterBar } from './components/NodeFilterBar'
import { NodeCard, NodeGroup, NodeStatus, NodeVersion } from './components/NodeCard'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

export default function ClusterNodes() {
  const { t, i18n } = useTranslation()
  const confirm = useConfirm()
  const [searchParams] = useSearchParams()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  // null until the first list arrives: a failed refresh keeps the last one.
  const [nodes, setNodes] = useState<ClusterNode[] | null>(null)
  const [nodesError, setNodesError] = useState<string | null>(null)
  const [metrics, setMetrics] = useState<ClusterNodeMetrics[]>([])
  const [latest, setLatest] = useState<string | undefined>()
  const [localId, setLocalId] = useState('')
  const [loading, setLoading] = useState(true)
  // Read once: the overview's links open the list already filtered, and from
  // then on the filter is the page's own and the address is left alone.
  const [filter, setFilter] = useState<NodeFilter>(() => filterFromQuery(searchParams))

  // Label editing
  const [labelDialogOpen, setLabelDialogOpen] = useState(false)
  const [labelNode, setLabelNode] = useState<ClusterNode | null>(null)

  // Background refreshes don't toggle the page-level loading spinner —
  // only the initial mount and the Refresh button do. Calling
  // setLoading(true) inside loadNodes would trip react-hooks/set-state-in-effect
  // because the interval below kicks it off synchronously.
  const loadNodes = useCallback(() => {
    // Each answer stands on its own: a refused node list must not cost the
    // status that says whether this is a cluster at all.
    Promise.allSettled([
      api.getClusterStatus(),
      api.getClusterNodes(),
      api.getClusterOverview(),
    ]).then(([s, n, o]) => {
      if (s.status === 'fulfilled') setStatus(s.value)
      if (n.status === 'fulfilled') {
        setNodes(n.value.nodes ?? [])
        setLocalId(n.value.local_id)
        setNodesError(null)
      } else {
        setNodesError(message(n.reason))
      }
      if (o.status === 'fulfilled' && o.value) {
        if (o.value.metrics) setMetrics(o.value.metrics)
        setLatest(o.value.latest_version)
      }
    }).finally(() => setLoading(false))
  }, [])

  const refresh = () => {
    setLoading(true)
    loadNodes()
  }

  // Load on mount + poll every 15s while visible (paused when the tab is hidden).
  useVisibleInterval(loadNodes, 15000)

  const leaderId = status?.leader_id ?? ''
  const versions = useMemo(() => new Map(metrics.map((m) => [m.node_id, m.version])), [metrics])
  const reference = useMemo(() => referenceVersion(latest, metrics.map((m) => m.version)), [latest, metrics])
  const groups = useMemo(() => nodeGroups(nodes ?? []), [nodes])
  const shown = useMemo(() => sortNodes(filterNodes(nodes ?? [], filter), leaderId), [nodes, filter, leaderId])

  const handleRemove = async (nodeId: string, nodeName: string) => {
    if (!(await confirm({ title: t('cluster.nodes.confirmRemove', { name: nodeName }), danger: true }))) return
    try {
      await api.removeClusterNode(nodeId)
      toast.success(t('cluster.nodes.removed', { name: nodeName }))
      loadNodes()
    } catch (err) {
      toast.error(message(err))
    }
  }

  const handleTransferLeadership = async (nodeId: string, nodeName: string) => {
    if (!(await confirm({ title: t('cluster.nodes.confirmTransfer', { name: nodeName }) }))) return
    try {
      await api.transferClusterLeadership(nodeId)
      toast.success(t('cluster.nodes.leaderTransferred', { name: nodeName }))
      setTimeout(loadNodes, 2000)
    } catch (err) {
      toast.error(message(err))
    }
  }

  const openLabelDialog = (node: ClusterNode) => {
    setLabelNode(node)
    setLabelDialogOpen(true)
  }

  // Only an answer that says so means "not a cluster". Before the first answer,
  // or when it failed, the page used to say this too.
  if (status?.enabled === false) {
    return (
      <div className="bg-card rounded-2xl p-8 card-shadow text-center">
        <Server className="h-12 w-12 text-muted-foreground mx-auto mb-3" />
        <p className="text-[13px] text-muted-foreground">{t('cluster.notEnabled.title')}</p>
      </div>
    )
  }

  if (nodes === null) {
    return (
      <ListLoadState
        loading={loading}
        error={loading ? null : nodesError ?? ''}
        errorTitle={t('cluster.nodes.loadError')}
        onRetry={refresh}
      />
    )
  }

  // Labels, leader transfer and removal are leader operations, and every node
  // can ask for them: the server forwards a follower's request to the leader.
  // A follower can also hand the leadership to itself. Never offered: removing
  // this node (that is leaving) or the leader (transfer it away first).
  const hasLeader = !!status?.is_leader || !!leaderId

  const labelChips = (node: ClusterNode) => (
    <div className="flex items-center gap-1 flex-wrap">
      {Object.entries(node.labels ?? {}).filter(([k]) => k !== GROUP_LABEL).map(([k, v]) => (
        <span key={k} className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
          {k}={v}
        </span>
      ))}
      {hasLeader && (
        <button
          onClick={() => openLabelDialog(node)}
          className="p-0.5 rounded hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
          title={t('cluster.nodes.editLabels')}
          aria-label={t('cluster.nodes.editLabels')}
        >
          <Tag className="h-3 w-3 text-muted-foreground" />
        </button>
      )}
    </div>
  )

  const actions = (node: ClusterNode) => {
    // Only a voting manager can lead: the server has no agent to hand it to.
    const canTransfer = hasLeader && node.status === 'online' && node.id !== leaderId && nodeRoleKind(node, leaderId) === 'manager'
    const canRemove = hasLeader && node.id !== localId && node.id !== leaderId
    if (!canTransfer && !canRemove) return null
    return (
      <div className="flex items-center gap-1">
        {canTransfer && (
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-7 p-0 text-primary hover:text-primary hover:bg-primary/10"
            onClick={() => handleTransferLeadership(node.id, node.name)}
            title={t('cluster.nodes.transferLeadership')}
            aria-label={t('cluster.nodes.transferLeadership')}
          >
            <ArrowRightLeft className="h-3.5 w-3.5" />
          </Button>
        )}
        {canRemove && (
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-7 p-0 text-destructive hover:text-destructive hover:bg-destructive/10"
            onClick={() => handleRemove(node.id, node.name)}
            aria-label={t('common.delete')}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>
    )
  }

  const seen = (node: ClusterNode) => lastSeenAgo(node.last_seen, i18n.language) || '—'

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('cluster.nodes.count', { count: nodes.length })}
        </span>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={refresh} disabled={loading}>
          <RefreshCw className={cn("h-4 w-4 mr-1", loading && "animate-spin")} />
          {t('common.refresh')}
        </Button>
      </div>

      {nodesError && (
        <div role="status" className="bg-warning/10 text-warning rounded-xl p-3 flex items-start gap-2">
          <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" />
          <div className="min-w-0">
            <p className="text-[13px] font-medium">{t('cluster.nodes.loadFailed')}</p>
            <p className="text-[12px] opacity-80 mt-0.5 break-words">{nodesError}</p>
          </div>
        </div>
      )}

      <NodeFilterBar filter={filter} onChange={setFilter} groups={groups} total={nodes.length} shown={shown.length} />

      {shown.length === 0 ? (
        <div className="bg-card rounded-2xl p-8 card-shadow text-center text-[13px] text-muted-foreground">
          {t('cluster.nodes.noMatch')}
        </div>
      ) : (
        <>
          <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('cluster.nodes.name')}</TableHead>
                  <TableHead>{t('cluster.nodes.kind')}</TableHead>
                  <TableHead>{t('common.status')}</TableHead>
                  <TableHead>{t('cluster.nodes.version')}</TableHead>
                  <TableHead>{t('cluster.nodes.group')}</TableHead>
                  <TableHead>{t('cluster.nodes.labels')}</TableHead>
                  <TableHead>{t('cluster.nodes.lastSeen')}</TableHead>
                  <TableHead>{t('cluster.nodes.apiAddress')}</TableHead>
                  <TableHead>{t('common.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((node) => (
                  <TableRow key={node.id}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <span data-node-name className="text-[13px] font-medium">{node.name}</span>
                        {node.id === localId && (
                          <span className="text-[10px] text-muted-foreground">({t('layout.cluster.localNode')})</span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell><KindBadge kind={nodeRoleKind(node, leaderId)} /></TableCell>
                    <TableCell><NodeStatus status={node.status} /></TableCell>
                    <TableCell className="text-[13px]"><NodeVersion version={versions.get(node.id)} reference={reference} /></TableCell>
                    <TableCell className="text-[13px]"><NodeGroup node={node} /></TableCell>
                    <TableCell>{labelChips(node)}</TableCell>
                    <TableCell className="text-[13px] text-muted-foreground">{seen(node)}</TableCell>
                    <TableCell className="text-[13px] text-muted-foreground">{node.api_address}</TableCell>
                    <TableCell>{actions(node)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <div className="md:hidden space-y-2">
            {shown.map((node) => (
              <NodeCard
                key={node.id}
                node={node}
                kind={nodeRoleKind(node, leaderId)}
                isLocal={node.id === localId}
                version={versions.get(node.id)}
                reference={reference}
                lastSeen={seen(node)}
                labels={labelChips(node)}
                actions={actions(node)}
              />
            ))}
          </div>
        </>
      )}

      {/* Label editing dialog */}
      <NodeLabelsDialog
        open={labelDialogOpen}
        onOpenChange={setLabelDialogOpen}
        node={labelNode}
        onSaved={loadNodes}
      />
    </div>
  )
}
