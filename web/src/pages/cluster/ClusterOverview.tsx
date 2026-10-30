import { useEffect, useState, useCallback, useId } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { Server, Boxes, Cpu, MemoryStick, HardDrive, Container, Bell, Power, Download, Pencil, LogOut, AlertTriangle, CircleArrowUp } from 'lucide-react'
import { api, type ApiError } from '@/lib/api'
import type { ClusterOverview as ClusterOverviewType, ClusterStatus, ClusterEvent, ClusterNode, ClusterNodeMetrics, ClusterUpdateEvent } from '@/types/api'
import { useWebSocket } from '@/hooks/useWebSocket'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { TypeToConfirmDialog } from '@/components/TypeToConfirmDialog'
import { useConfirm } from '@/components/ConfirmDialog'
import { usePrompt } from '@/components/PromptDialog'
import { cn, nodeStatusColor } from '@/lib/utils'
import { waitForServerBack } from '@/lib/restart'
import { ALL_NODES, filterNodes, isAgent, isManager, isOutdated, nodeRoleKind, referenceVersion, sortNodes } from '@/lib/clusterNodes'
import { warningText, type ApiWarning } from '@/lib/warnings'
import { toast } from 'sonner'
import { ClusterInitForm } from './components/ClusterInitForm'
import { ClusterUpdateProgress } from './components/ClusterUpdateProgress'
import { EditNodeAddressDialog } from './components/EditNodeAddressDialog'
import { KindBadge } from './components/KindBadge'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

// How many offline agents the overview names before it says "and N more".
const NAMED_OFFLINE = 5

// The node restarts after a leave, a disband or its own update: wait for it,
// then reload (and reload anyway once the wait gives up).
const reloadWhenBack = () => waitForServerBack({ onTimeout: () => window.location.reload() })

// A wave is one agent or more, and no more than there are agents.
function clampWave(entered: string, agents: number): number {
  const n = Math.floor(Number(entered))
  return Number.isFinite(n) ? Math.min(Math.max(n, 1), agents) : 1
}

// The combined snapshot pushed over /ws/cluster/overview (status + overview +
// recent events in one message). Followers serve their replicated FSM view with
// stale=true.
interface ClusterSnapshot {
  enabled: boolean
  local_id?: string
  is_leader?: boolean
  stale?: boolean
  name?: string
  node_count?: number
  leader_id?: string
  nodes?: ClusterNode[]
  metrics?: ClusterNodeMetrics[]
  events?: ClusterEvent[]
  // Each push replaces the overview, so a field the push does not carry is
  // lost on the first one.
  latest_version?: string
}

export default function ClusterOverview() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const prompt = usePrompt()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [overview, setOverview] = useState<ClusterOverviewType | null>(null)
  const [events, setEvents] = useState<ClusterEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [updating, setUpdating] = useState(false)
  const [updateLog, setUpdateLog] = useState<ClusterUpdateEvent[]>([])
  const [waveSize, setWaveSize] = useState('1')
  const [disbandOpen, setDisbandOpen] = useState(false)
  const [disbanding, setDisbanding] = useState(false)
  // What a leave or a disband could not do. It stays until it is read, and
  // the wait for the restart starts only then.
  const [exitWarnings, setExitWarnings] = useState<{ title: string; warnings: ApiWarning[] } | null>(null)
  const waveHintId = useId()

  // Per-node address editing
  const [editDialogOpen, setEditDialogOpen] = useState(false)
  const [editingNode, setEditingNode] = useState<ClusterNode | null>(null)

  const loadData = useCallback(() => {
    Promise.all([
      // local:true pins the datacenter-scope status to this node so local_id /
      // is_leader stay correct even while a remote node is selected (?node=).
      api.getClusterStatus(),
      api.getClusterOverview().catch(() => null),
      api.getClusterEvents(20).catch(() => ({ events: [] })),
    ]).then(([s, o, e]) => {
      setStatus(s)
      setOverview(o)
      setEvents(e.events)
    }).finally(() => setLoading(false))
  }, [])

  // The /ws/cluster/overview push replaces the old 15s status+overview+events
  // triple-poll: one shared sampler per node fans the combined snapshot out to
  // every dashboard. We still fetch once on mount for an instant first paint
  // (and as a fallback while the socket is connecting / between reconnects).
  const applySnapshot = useCallback((snap: ClusterSnapshot) => {
    if (!snap || !snap.enabled) return
    setStatus({
      enabled: true,
      name: snap.name,
      node_count: snap.node_count,
      leader_id: snap.leader_id,
      local_id: snap.local_id,
      is_leader: snap.is_leader,
      stale: snap.stale,
    })
    setOverview({
      name: snap.name ?? '',
      node_count: snap.node_count ?? 0,
      leader_id: snap.leader_id ?? '',
      nodes: snap.nodes ?? [],
      metrics: snap.metrics ?? [],
      latest_version: snap.latest_version,
    })
    setEvents(snap.events ?? [])
    setLoading(false)
  }, [])

  // local: the snapshot already describes every node, so relaying this socket
  // through ?node= would just proxy an identical stream off a peer.
  useWebSocket<ClusterSnapshot>({ url: '/ws/cluster/overview', local: true, onMessage: applySnapshot })

  useEffect(() => {
    loadData()
    const handleVisibility = () => { if (!document.hidden) loadData() }
    document.addEventListener('visibilitychange', handleVisibility)
    return () => {
      document.removeEventListener('visibilitychange', handleVisibility)
    }
  }, [loadData])

  if (loading) {
    return (
      <div className="flex items-center justify-center h-32">
        <div className="h-5 w-5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  // The name is cluster-wide: any node can rename it (the server hands the
  // write to the leader), and the shell's sidebar is told to re-read it.
  const handleRename = async () => {
    const current = overview?.name || status?.name || ''
    const entered = await prompt({
      title: t('cluster.overview.renameTitle'),
      description: t('cluster.overview.renameHint'),
      defaultValue: current,
      confirmLabel: t('cluster.overview.rename'),
    })
    const name = entered?.trim()
    if (!name || name === current) return
    try {
      await api.renameCluster(name)
      toast.success(t('cluster.overview.renamed', { name }))
      loadData()
      window.dispatchEvent(new Event('sfpanel:cluster-changed'))
    } catch (err) {
      toast.error((err as { code?: string }).code === 'INVALID_NAME' ? t('cluster.overview.renameInvalid') : message(err))
    }
  }

  const handleDisband = async () => {
    setDisbanding(true)
    try {
      const res = await api.disbandCluster()
      setDisbandOpen(false)
      toast.success(t('cluster.overview.disbanded'))
      // The agents it could not tell need a command on their own servers. A
      // toast would go with the reload, so the names stay on screen and the
      // wait for the restart starts once they have been read.
      if (res.warnings?.length) {
        setExitWarnings({ title: t('cluster.overview.disbandWarningsTitle'), warnings: res.warnings })
        return
      }
      reloadWhenBack()
    } catch (err) {
      toast.error(message(err))
      setDisbanding(false)
    }
  }

  const closeExitWarnings = () => {
    setExitWarnings(null)
    reloadWhenBack()
  }

  const handleClusterUpdate = async (mode: 'rolling' | 'simultaneous') => {
    if (!status) return
    const agentCount = (overview?.nodes ?? []).filter(isAgent).length
    // Only a rolling update has waves to size; simultaneous sends every agent
    // in one.
    const wave = mode === 'rolling' && agentCount > 0 ? clampWave(waveSize, agentCount) : undefined
    const order = mode === 'rolling' ? t('cluster.overview.confirmRollingOrder') : t('cluster.overview.confirmSimultaneousOrder')
    if (!(await confirm({ title: t('cluster.overview.confirmUpdate'), description: order }))) return
    setUpdating(true)
    setUpdateLog([])
    // From a follower the stream is relayed through this node, and this node
    // is one of the ones the update restarts: the view is cut off part way.
    // The update itself carries on at the leader, so that is not a failure.
    const viaFollower = !status.is_leader
    let received = 0
    let concluded = false
    try {
      await api.clusterUpdateStream(mode, (data) => {
        received++
        if (data.overall === 'complete' || data.overall === 'error') concluded = true
        // The leader updates itself last and restarts: on the leader's own
        // page, wait for it to come back on the new release.
        if (data.overall === 'complete' && data.leader_restarting && !viaFollower) reloadWhenBack()
        setUpdateLog(prev => [...prev, data])
      }, viaFollower ? status.leader_id : undefined, wave)
      // A relay that closes cleanly mid-update ends the same way as one that
      // breaks: no verdict arrived, and the leader is still working.
      if (viaFollower && received > 0 && !concluded) toast.info(t('cluster.overview.updateContinuesOnLeader'))
    } catch (err) {
      const e = err as ApiError
      // Only this conflict means a run is under way; another says its own.
      if (e.status === 409 && e.code === 'UPDATE_IN_PROGRESS') toast.error(t('cluster.overview.updateAlreadyRunning'))
      else if (viaFollower && received > 0) toast.info(t('cluster.overview.updateContinuesOnLeader'))
      else toast.error(message(err))
    } finally {
      setUpdating(false)
      loadData()
    }
  }

  const openEditAddress = (node: ClusterNode) => {
    setEditingNode(node)
    setEditDialogOpen(true)
  }

  // The server ends the process two seconds after it answers a leave. A
  // warning names the node to remove on the leader, which a toast would take
  // away before it could be copied: it waits in the dialog like a disband's.
  const left = (warnings: ApiWarning[] | undefined) => {
    toast.success(t('cluster.leave.success'))
    if (warnings?.length) {
      setExitWarnings({ title: t('cluster.leave.warningsTitle'), warnings })
      return
    }
    reloadWhenBack()
  }

  // Rendered in every branch below: once the node restarts standalone, the
  // next status read (a tab shown again) turns the page into the init form,
  // and the warnings must not go with the cluster view.
  const exitWarningsDialog = (
    <Dialog open={!!exitWarnings} onOpenChange={(open) => { if (!open) closeExitWarnings() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="text-warning">{exitWarnings?.title}</DialogTitle>
          <DialogDescription asChild>
            <div className="space-y-2">
              {(exitWarnings?.warnings ?? []).map((w, i) => (
                <p key={i} className="break-words">{warningText(w, t)}</p>
              ))}
            </div>
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button onClick={closeExitWarnings}>{t('common.confirm')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )

  const handleLeave = async () => {
    if (!(await confirm({ title: t('cluster.leave.confirm'), danger: true }))) return
    try {
      left((await api.leaveCluster()).warnings)
    } catch (err) {
      const e = err as ApiError
      // Only a quorum refusal is one that forcing gets past. The reason (how
      // many voters would be left) is in the server's sentence.
      if (e.status === 409 && e.code === 'CLUSTER_QUORUM') {
        if (!(await confirm({ title: t('cluster.leave.forceConfirm'), description: e.serverMessage, danger: true }))) return
        try {
          left((await api.leaveCluster(true)).warnings)
        } catch (forceErr) {
          toast.error(message(forceErr))
        }
        return
      }
      toast.error(message(err))
    }
  }

  if (status?.configured) {
    // A member whose cluster did not start: offering Init or Join here would
    // replace the membership it still has, so say what happened instead.
    return (
      <div role="alert" className="bg-card rounded-2xl p-8 card-shadow max-w-lg mx-auto text-center space-y-3">
        <Server className="h-12 w-12 text-muted-foreground mx-auto" />
        <h2 className="text-[15px] font-semibold">{t('cluster.notRunning.title')}</h2>
        <p className="text-[13px] text-muted-foreground">{t('cluster.notRunning.description')}</p>
        <code className="inline-block rounded-lg bg-secondary/60 px-3 py-1.5 text-[12px]">sudo systemctl restart sfpanel</code>
        {exitWarningsDialog}
      </div>
    )
  }

  if (!status?.enabled) {
    return <>{exitWarningsDialog}<ClusterInitForm /></>
  }

  const leaderId = status.leader_id ?? ''
  const nodes = sortNodes(overview?.nodes || [], leaderId)
  const metrics = overview?.metrics || []
  const managers = nodes.filter(isManager)
  const agents = nodes.filter(isAgent)
  const online = (list: ClusterNode[]) => list.filter(n => n.status === 'online').length
  // Offline as the node list's filter reads it, so the link shows the same nodes.
  const offlineAgents = filterNodes(nodes, { ...ALL_NODES, kind: 'agent', status: 'offline' })
  const reference = referenceVersion(overview?.latest_version, metrics.map(m => m.version))
  const versionOf = new Map(metrics.map(m => [m.node_id, m.version]))
  const toUpdate = nodes.filter(n => n.status === 'online' && isOutdated(versionOf.get(n.id), reference)).length
  const offlineNames = offlineAgents.slice(0, NAMED_OFFLINE).map(n => n.name).join(', ')
    + (offlineAgents.length > NAMED_OFFLINE ? ' ' + t('cluster.overview.andMore', { count: offlineAgents.length - NAMED_OFFLINE }) : '')

  const avgCpu = metrics.length > 0 ? metrics.reduce((s, m) => s + m.cpu_percent, 0) / metrics.length : 0
  const avgMem = metrics.length > 0 ? metrics.reduce((s, m) => s + m.memory_percent, 0) / metrics.length : 0
  const avgDisk = metrics.length > 0 ? metrics.reduce((s, m) => s + m.disk_percent, 0) / metrics.length : 0
  const totalContainers = metrics.reduce((s, m) => s + m.container_count, 0)

  const statCards = [
    { key: 'managers', label: t('cluster.overview.managers'), value: `${online(managers)}/${managers.length}`, icon: Server, color: 'var(--primary)' },
    // A cluster without agents has no agent card.
    ...(agents.length > 0
      ? [{ key: 'agents', label: t('cluster.overview.agents'), value: `${online(agents)}/${agents.length}`, icon: Boxes, color: online(agents) < agents.length ? 'var(--warning)' : 'var(--primary)' }]
      : []),
    { key: 'cpu', label: t('cluster.overview.avgCpu'), value: `${avgCpu.toFixed(1)}%`, icon: Cpu, color: avgCpu > 80 ? 'var(--destructive)' : avgCpu > 50 ? 'var(--warning)' : 'var(--primary)' },
    { key: 'memory', label: t('cluster.overview.avgMemory'), value: `${avgMem.toFixed(1)}%`, icon: MemoryStick, color: avgMem > 80 ? 'var(--destructive)' : avgMem > 50 ? 'var(--warning)' : 'var(--success)' },
    { key: 'disk', label: t('cluster.overview.avgDisk'), value: `${avgDisk.toFixed(1)}%`, icon: HardDrive, color: avgDisk > 80 ? 'var(--destructive)' : avgDisk > 50 ? 'var(--warning)' : 'var(--primary)' },
    { key: 'containers', label: t('cluster.overview.containers'), value: String(totalContainers), icon: Container, color: 'var(--primary)' },
  ]

  return (
    <div className="space-y-6">
      {/* Stale-data banner: backend flagged this response as served from a
          local FSM without leader confirmation. Likely partition or
          mid-election; numbers below may not reflect the real cluster state. */}
      {status.stale && (
        <div className="bg-warning/10 border border-warning/30 rounded-xl px-4 py-2 text-[12px] text-warning">
          {t('cluster.overview.staleData')}
        </div>
      )}
      {/* Stale data may still list an agent offline that is back: no warning from it. */}
      {!status.stale && offlineAgents.length > 0 && (
        <Link
          to="/cluster/nodes?kind=agent&status=offline"
          className="flex items-start gap-2 bg-warning/10 border border-warning/30 rounded-xl px-4 py-2 text-[12px] text-warning hover:bg-warning/15"
        >
          <AlertTriangle className="h-3.5 w-3.5 mt-0.5 shrink-0" />
          <span>{t('cluster.overview.offlineAgents', { count: offlineAgents.length, names: offlineNames })}</span>
        </Link>
      )}
      {toUpdate > 0 && reference && (
        <div className="flex items-start gap-2 bg-primary/10 border border-primary/20 rounded-xl px-4 py-2 text-[12px] text-primary">
          <CircleArrowUp className="h-3.5 w-3.5 mt-0.5 shrink-0" />
          <span>{t('cluster.overview.toUpdate', { count: toUpdate, version: reference })}</span>
        </div>
      )}
      {/* Cluster info */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-3">
            <div className="h-8 w-8 rounded-lg bg-primary/10 flex items-center justify-center">
              <Server className="h-4 w-4 text-primary" />
            </div>
            <div>
              <div className="flex items-center gap-1">
                <h2 className="text-[15px] font-semibold">{overview?.name || status.name}</h2>
                {(status.is_leader || !!status.leader_id) && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground hover:bg-accent"
                    onClick={handleRename}
                    title={t('cluster.overview.rename')}
                    aria-label={t('cluster.overview.rename')}
                  >
                    <Pencil className="h-3.5 w-3.5" />
                  </Button>
                )}
              </div>
              <p className="text-[11px] text-muted-foreground">
                {t('cluster.overview.nodeCount', { count: nodes.length })}
              </p>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            {(status.is_leader || !!status.leader_id) && (
              <div className="flex flex-wrap items-center gap-1">
                {agents.length > 0 && (
                  <label className="flex items-center gap-1.5 text-[12px] text-muted-foreground mr-1" title={t('cluster.overview.waveSizeHint')}>
                    {t('cluster.overview.waveSize')}
                    <Input
                      type="number"
                      min={1}
                      max={agents.length}
                      value={waveSize}
                      onChange={(e) => setWaveSize(e.target.value)}
                      disabled={updating}
                      aria-describedby={waveHintId}
                      className="h-8 w-16 rounded-xl text-[13px]"
                    />
                    <span id={waveHintId} className="sr-only">{t('cluster.overview.waveSizeHint')}</span>
                  </label>
                )}
                <Button
                  variant="outline"
                  size="sm"
                  className="rounded-xl"
                  disabled={updating}
                  onClick={() => handleClusterUpdate('rolling')}
                >
                  <Download className="h-3.5 w-3.5 mr-1.5" />
                  {updating ? t('cluster.overview.updating') : t('cluster.overview.rollingUpdate')}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="rounded-xl"
                  disabled={updating}
                  onClick={() => handleClusterUpdate('simultaneous')}
                >
                  <Download className="h-3.5 w-3.5 mr-1.5" />
                  {t('cluster.overview.simultaneousUpdate')}
                </Button>
              </div>
            )}
            <Button
              variant="outline"
              size="sm"
              className="rounded-xl text-destructive hover:text-destructive hover:bg-destructive/10 border-destructive/20"
              onClick={() => setDisbandOpen(true)}
            >
              <Power className="h-3.5 w-3.5 mr-1.5" />
              {t('cluster.overview.disband')}
            </Button>
          </div>
        </div>
      </div>

      {/* Update progress */}
      {updateLog.length > 0 && <ClusterUpdateProgress events={updateLog} />}

      {/* Stats */}
      <div className={cn('grid grid-cols-2 gap-4', statCards.length > 5 ? 'md:grid-cols-3 xl:grid-cols-6' : 'md:grid-cols-5')}>
        {statCards.map((card) => (
          <div key={card.key} data-testid={`stat-${card.key}`} className="bg-card rounded-2xl p-5 card-shadow">
            <div className="flex items-center gap-2 mb-2">
              <card.icon className="h-4 w-4" style={{ color: card.color }} />
              <span className="text-[11px] text-muted-foreground">{card.label}</span>
            </div>
            <p className="text-[22px] font-bold tracking-tight" style={{ color: card.color }}>{card.value}</p>
          </div>
        ))}
      </div>

      {/* Node list with metrics */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <div className="px-5 py-4 border-b border-border">
          <h3 className="text-[15px] font-semibold">{t('cluster.overview.nodeStatus')}</h3>
        </div>
        <div className="divide-y divide-border">
          {nodes.map((node) => {
            const nodeMetrics = metrics.find(m => m.node_id === node.id)

            return (
              <div key={node.id} className="px-5 py-4 flex items-center gap-4">
                <div className="flex items-center gap-3 min-w-[200px]">
                  <span className={cn('h-2.5 w-2.5 rounded-full', nodeStatusColor(node.status))} />
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="text-[13px] font-medium">{node.name}</span>
                      <KindBadge kind={nodeRoleKind(node, leaderId)} />
                      {node.id === status.local_id && (
                        <span className="text-[10px] text-muted-foreground">({t('layout.cluster.localNode')})</span>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="text-[11px] text-muted-foreground">{node.api_address}</span>
                      {node.labels && Object.keys(node.labels).length > 0 && (
                        <div className="flex gap-1">
                          {Object.entries(node.labels).map(([k, v]) => (
                            <span key={k} className="inline-flex items-center px-1.5 py-0 rounded text-[9px] font-medium bg-secondary text-muted-foreground">
                              {k}={v}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                </div>

                {nodeMetrics ? (
                  <div className="flex items-center gap-6 flex-1">
                    <MetricBar label="CPU" value={nodeMetrics.cpu_percent} />
                    <MetricBar label={t('cluster.overview.memory')} value={nodeMetrics.memory_percent} />
                    <MetricBar label={t('cluster.overview.disk')} value={nodeMetrics.disk_percent} />
                    <div className="text-[13px] text-muted-foreground">
                      <Container className="h-3.5 w-3.5 inline mr-1" />
                      {nodeMetrics.container_count}
                    </div>
                  </div>
                ) : (
                  <div className="flex-1 text-[13px] text-muted-foreground italic">
                    {node.status === 'offline' ? t('cluster.overview.noMetrics') : t('cluster.overview.metricsLoading')}
                  </div>
                )}

                <div className="flex items-center gap-1 shrink-0">
                  {/* An agent's certificate names the address it registered
                      with: the server would take a new one and break the link. */}
                  {isManager(node) && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground hover:bg-accent"
                      onClick={() => openEditAddress(node)}
                      title={t('cluster.nodes.editAddress')}
                      aria-label={t('cluster.nodes.editAddress')}
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </Button>
                  )}
                  {node.id === status.local_id && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-destructive hover:text-destructive hover:bg-destructive/10"
                      onClick={handleLeave}
                      title={t('cluster.leave.action')}
                      aria-label={t('cluster.leave.action')}
                    >
                      <LogOut className="h-3.5 w-3.5" />
                    </Button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      </div>

      {/* Recent events */}
      {events.length > 0 && (
        <div className="bg-card rounded-2xl card-shadow overflow-hidden">
          <div className="px-5 py-4 border-b border-border flex items-center gap-2">
            <Bell className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-[15px] font-semibold">{t('cluster.overview.recentEvents')}</h3>
          </div>
          <div className="divide-y divide-border">
            {events.slice(0, 10).map((event) => (
              <div key={event.id} className="px-5 py-3 flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <EventDot type={event.type} />
                  <div>
                    <span className="text-[13px] font-medium">{event.node_name || event.node_id.slice(0, 8)}</span>
                    <span className="text-[13px] text-muted-foreground ml-2">{t(`cluster.events.${event.type}`, { defaultValue: event.type })}</span>
                  </div>
                </div>
                <span className="text-[11px] text-muted-foreground">
                  {new Date(event.timestamp).toLocaleString()}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Edit node address dialog */}
      <EditNodeAddressDialog
        open={editDialogOpen}
        onOpenChange={setEditDialogOpen}
        node={editingNode}
        onSaved={loadData}
      />

      {/* Disband Type-to-Confirm */}
      <TypeToConfirmDialog
        open={disbandOpen}
        onOpenChange={setDisbandOpen}
        title={t('cluster.overview.disbandConfirmTitle', { name: overview?.name || status.name || '' })}
        description={t('cluster.overview.disbandConfirmDesc', { name: overview?.name || status.name || '' })}
        confirmPhrase={overview?.name || status.name || ''}
        confirmLabel={t('cluster.overview.disband')}
        loading={disbanding}
        onConfirm={handleDisband}
      />

      {exitWarningsDialog}
    </div>
  )
}

function MetricBar({ label, value }: { label: string; value: number }) {
  const color = value > 80 ? 'var(--destructive)' : value > 50 ? 'var(--warning)' : 'var(--primary)'
  return (
    <div className="min-w-[100px]">
      <div className="flex justify-between text-[11px] mb-1">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-medium" style={{ color }}>{value.toFixed(1)}%</span>
      </div>
      <div className="h-1.5 rounded-full bg-secondary overflow-hidden">
        <div
          className="h-full rounded-full transition-all duration-500"
          style={{ width: `${Math.min(100, value)}%`, backgroundColor: color }}
        />
      </div>
    </div>
  )
}

function EventDot({ type }: { type: string }) {
  const color = type.includes('offline') || type.includes('left')
    ? 'var(--destructive)'
    : type.includes('suspect')
      ? 'var(--warning)'
      : type.includes('online') || type.includes('joined')
        ? 'var(--success)'
        : 'var(--primary)'

  return <span className="h-2 w-2 rounded-full shrink-0" style={{ backgroundColor: color }} />
}
