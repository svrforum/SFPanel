import { useCallback, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { useTranslation } from 'react-i18next'
import { Server, RefreshCw, AlertTriangle, Plus, Loader2 } from 'lucide-react'
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
  ROLE_CHANGE_TIMEOUT_MS,
  filterFromQuery,
  filterNodes,
  isAgent,
  lastSeenAgo,
  nodeGroups,
  nodeRoleKind,
  referenceVersion,
  sortNodes,
  type NodeFilter,
  type RoleChange,
} from '@/lib/clusterNodes'
import { NodeLabelsDialog } from './components/NodeLabelsDialog'
import { EditNodeAddressDialog } from './components/EditNodeAddressDialog'
import { NodeRowMenu } from './components/NodeRowMenu'
import { KindBadge } from './components/KindBadge'
import { NodeFilterBar } from './components/NodeFilterBar'
import { NodeCard, NodeGroup, NodeStatus, NodeVersion } from './components/NodeCard'
import { AddAgentDialog } from './components/AddAgentDialog'
import { ConvertNodeDialog } from './components/ConvertNodeDialog'
import { PromoteNodeDialog } from './components/PromoteNodeDialog'
import { RoleChangeProgress } from './components/RoleChangeProgress'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

export default function ClusterNodes() {
  const { t, i18n } = useTranslation()
  const confirm = useConfirm()
  const [searchParams, setSearchParams] = useSearchParams()
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

  // The old token page's address lands here with ?add=agent (App.tsx).
  const [addOpen, setAddOpen] = useState(() => searchParams.get('add') === 'agent')

  // Label editing
  const [labelDialogOpen, setLabelDialogOpen] = useState(false)
  const [labelNode, setLabelNode] = useState<ClusterNode | null>(null)
  // Address editing
  const [addressDialogOpen, setAddressDialogOpen] = useState(false)
  const [addressNode, setAddressNode] = useState<ClusterNode | null>(null)
  // Conversion and promotion
  const [convertOpen, setConvertOpen] = useState(false)
  const [convertNode, setConvertNode] = useState<ClusterNode | null>(null)
  const [promoteOpen, setPromoteOpen] = useState(false)
  const [promoteNode, setPromoteNode] = useState<ClusterNode | null>(null)
  // The changes the page follows. Kept in this page only: a reload forgets
  // them, and the list still shows where each node ended up.
  const [changes, setChanges] = useState<(RoleChange & { done?: boolean })[]>([])
  const following = useRef(false)

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

  // Every 3s while a conversion or promotion is under way, only the list. A
  // conversion takes a voter out of Raft and a promotion adds one, so the
  // managers may be electing a leader: a 503 or any other miss is not shown,
  // the next poll asks again. Past the timeout the 15s poll is enough.
  const followChanges = () => {
    const now = Date.now()
    if (following.current || !changes.some((c) => !c.done && now - c.startedAt <= ROLE_CHANGE_TIMEOUT_MS)) return
    following.current = true
    api.getClusterNodes()
      .then((res) => {
        setNodes(res.nodes ?? [])
        setLocalId(res.local_id)
      })
      .catch(() => {})
      .finally(() => { following.current = false })
  }
  useVisibleInterval(followChanges, 3000)

  const leaderId = status?.leader_id ?? ''
  const versions = useMemo(() => new Map(metrics.map((m) => [m.node_id, m.version])), [metrics])
  const reference = useMemo(() => referenceVersion(latest, metrics.map((m) => m.version)), [latest, metrics])
  const groups = useMemo(() => nodeGroups(nodes ?? []), [nodes])
  const shown = useMemo(() => sortNodes(filterNodes(nodes ?? [], filter), leaderId), [nodes, filter, leaderId])

  // After a change the list is fetched again and the shell is told, so the
  // sidebar's tree shows the same nodes, groups and leader as this table.
  const changed = useCallback(() => {
    loadNodes()
    window.dispatchEvent(new Event('sfpanel:cluster-changed'))
  }, [loadNodes])

  const handleRemove = async (node: ClusterNode) => {
    // A removed agent stays an agent until it is reset on its own server: the
    // operator needs the command that makes it a standalone panel again.
    const title = isAgent(node)
      ? t('cluster.nodes.confirmRemoveAgent', { name: node.name })
      : t('cluster.nodes.confirmRemove', { name: node.name })
    if (!(await confirm({ title, danger: true }))) return
    try {
      await api.removeClusterNode(node.id)
      toast.success(t('cluster.nodes.removed', { name: node.name }))
      changed()
    } catch (err) {
      toast.error(message(err))
    }
  }

  const handleTransferLeadership = async (node: ClusterNode) => {
    if (!(await confirm({ title: t('cluster.nodes.confirmTransfer', { name: node.name }) }))) return
    try {
      await api.transferClusterLeadership(node.id)
      toast.success(t('cluster.nodes.leaderTransferred', { name: node.name }))
      // The election takes a moment: asked at once, the old leader still leads.
      setTimeout(changed, 2000)
    } catch (err) {
      toast.error(message(err))
    }
  }

  const startChange = (node: ClusterNode, target: RoleChange['target']) => {
    setChanges((prev) => [
      ...prev.filter((c) => c.nodeId !== node.id),
      { nodeId: node.id, name: node.name, target, startedAt: Date.now() },
    ])
  }

  const changeDone = useCallback((nodeId: string) => {
    setChanges((prev) => prev.map((c) => (c.nodeId === nodeId ? { ...c, done: true } : c)))
    changed()
  }, [changed])

  const dismissChange = useCallback((nodeId: string) => {
    setChanges((prev) => prev.filter((c) => c.nodeId !== nodeId))
  }, [])

  // Closed, the dialog is not asked for again by a reload of the same address.
  const setAddAgent = (open: boolean) => {
    setAddOpen(open)
    if (!open && searchParams.has('add')) {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev)
        next.delete('add')
        return next
      }, { replace: true })
    }
  }

  const openLabelDialog = (node: ClusterNode) => {
    setLabelNode(node)
    setLabelDialogOpen(true)
  }

  const openAddressDialog = (node: ClusterNode) => {
    setAddressNode(node)
    setAddressDialogOpen(true)
  }

  const openConvertDialog = (node: ClusterNode) => {
    setConvertNode(node)
    setConvertOpen(true)
  }

  const openPromoteDialog = (node: ClusterNode) => {
    setPromoteNode(node)
    setPromoteOpen(true)
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

  // Every leader operation needs a leader to land on; NodeRowMenu decides
  // which of them a node is offered.
  const hasLeader = !!status?.is_leader || !!leaderId

  const labelChips = (node: ClusterNode) => (
    <div className="flex items-center gap-1 flex-wrap">
      {Object.entries(node.labels ?? {}).filter(([k]) => k !== GROUP_LABEL).map(([k, v]) => (
        <span key={k} className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
          {k}={v}
        </span>
      ))}
    </div>
  )

  const menu = (node: ClusterNode) => (
    <NodeRowMenu
      node={node}
      localId={localId}
      leaderId={leaderId}
      hasLeader={hasLeader}
      onLabels={openLabelDialog}
      onAddress={openAddressDialog}
      onTransfer={handleTransferLeadership}
      onRemove={handleRemove}
      onConvert={openConvertDialog}
      onPromote={openPromoteDialog}
    />
  )

  // A converting node leaves Raft before it registers as an agent, and for
  // that moment the list does not have it: its row stays, saying so.
  const absent = changes.filter((c) => !c.done && !nodes.some((n) => n.id === c.nodeId))
  const startedText = (c: RoleChange) =>
    c.target === 'agent' ? t('cluster.convert.started', { name: c.name }) : t('cluster.promote.started', { name: c.name })

  const seen = (node: ClusterNode) => lastSeenAgo(node.last_seen, i18n.language) || '—'

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('cluster.nodes.count', { count: nodes.length })}
        </span>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="rounded-xl" onClick={refresh} disabled={loading}>
            <RefreshCw className={cn("h-4 w-4 mr-1", loading && "animate-spin")} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" className="rounded-xl" onClick={() => setAddAgent(true)}>
            <Plus className="h-4 w-4 mr-1" />
            {t('cluster.addAgent.button')}
          </Button>
        </div>
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

      {changes.length > 0 && (
        <div className="space-y-2">
          {changes.map((c) => (
            <RoleChangeProgress
              key={c.nodeId}
              nodeId={c.nodeId}
              name={c.name}
              target={c.target}
              startedAt={c.startedAt}
              nodes={nodes}
              onDone={changeDone}
              onDismiss={dismissChange}
            />
          ))}
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
                    <TableCell>{menu(node)}</TableCell>
                  </TableRow>
                ))}
                {absent.map((c) => (
                  <TableRow key={`absent-${c.nodeId}`}>
                    <TableCell>
                      <span data-node-name className="text-[13px] font-medium">{c.name}</span>
                    </TableCell>
                    <TableCell colSpan={8} className="text-[13px] text-muted-foreground">
                      <span className="inline-flex items-center gap-2">
                        <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
                        {startedText(c)}
                      </span>
                    </TableCell>
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
                menu={menu(node)}
              />
            ))}
            {absent.map((c) => (
              <div key={`absent-${c.nodeId}`} className="bg-card rounded-2xl card-shadow p-4 space-y-1">
                <p className="text-[13px] font-medium">{c.name}</p>
                <p className="text-[12px] text-muted-foreground inline-flex items-center gap-2">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
                  {startedText(c)}
                </p>
              </div>
            ))}
          </div>
        </>
      )}

      {/* Label editing dialog */}
      <NodeLabelsDialog
        open={labelDialogOpen}
        onOpenChange={setLabelDialogOpen}
        node={labelNode}
        onSaved={changed}
      />

      <AddAgentDialog open={addOpen} onOpenChange={setAddAgent} localId={localId} onJoined={changed} />

      <EditNodeAddressDialog
        open={addressDialogOpen}
        onOpenChange={setAddressDialogOpen}
        node={addressNode}
        onSaved={changed}
      />

      <ConvertNodeDialog
        open={convertOpen}
        onOpenChange={setConvertOpen}
        node={convertNode}
        localId={localId}
        nodes={nodes}
        onStarted={(node) => startChange(node, 'agent')}
      />

      <PromoteNodeDialog
        open={promoteOpen}
        onOpenChange={setPromoteOpen}
        node={promoteNode}
        onStarted={(node) => startChange(node, 'manager')}
      />
    </div>
  )
}
