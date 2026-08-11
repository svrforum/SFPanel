import { useEffect, useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Server, Cpu, MemoryStick, HardDrive, Container, Crown, Bell, Power, Download, Pencil, LogOut } from 'lucide-react'
import { api } from '@/lib/api'
import type { ClusterOverview as ClusterOverviewType, ClusterStatus, ClusterEvent, ClusterNode, ClusterNodeMetrics } from '@/types/api'
import { useWebSocket } from '@/hooks/useWebSocket'
import { Button } from '@/components/ui/button'
import { TypeToConfirmDialog } from '@/components/TypeToConfirmDialog'
import { useConfirm } from '@/components/ConfirmDialog'
import { cn, nodeStatusColor } from '@/lib/utils'
import { waitForServerBack } from '@/lib/restart'
import { toast } from 'sonner'
import { ClusterInitForm } from './components/ClusterInitForm'
import { ClusterUpdateProgress, type UpdateEvent } from './components/ClusterUpdateProgress'
import { EditNodeAddressDialog } from './components/EditNodeAddressDialog'

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
}

export default function ClusterOverview() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [overview, setOverview] = useState<ClusterOverviewType | null>(null)
  const [events, setEvents] = useState<ClusterEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [updating, setUpdating] = useState(false)
  const [updateLog, setUpdateLog] = useState<UpdateEvent[]>([])
  const [disbandOpen, setDisbandOpen] = useState(false)
  const [disbanding, setDisbanding] = useState(false)

  // Per-node address editing
  const [editDialogOpen, setEditDialogOpen] = useState(false)
  const [editingNode, setEditingNode] = useState<ClusterNode | null>(null)

  const loadData = useCallback(() => {
    Promise.all([
      // local:true pins the datacenter-scope status to this node so local_id /
      // is_leader stay correct even while a remote node is selected (?node=).
      api.getClusterStatus(true),
      api.getClusterOverview(true).catch(() => null),
      api.getClusterEvents(20, true).catch(() => ({ events: [] })),
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
    })
    setEvents(snap.events ?? [])
    setLoading(false)
  }, [])

  useWebSocket<ClusterSnapshot>({ url: '/ws/cluster/overview', onMessage: applySnapshot })

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

  const handleDisband = async () => {
    setDisbanding(true)
    try {
      await api.disbandCluster()
      setDisbandOpen(false)
      toast.success(t('cluster.overview.disbanded'))
      // Wait for the self-restart (bounded, lib default 5 min), then reload;
      // on timeout reload anyway, matching the old inline copy's give-up path.
      waitForServerBack({ onTimeout: () => window.location.reload() })
    } catch (err) {
      toast.error(String(err))
      setDisbanding(false)
    }
  }

  const handleClusterUpdate = async (mode: 'rolling' | 'simultaneous') => {
    if (!(await confirm({ title: t('cluster.overview.confirmUpdate') }))) return
    setUpdating(true)
    setUpdateLog([])
    try {
      await api.clusterUpdateStream(mode, (data) => {
        setUpdateLog(prev => [...prev, data as UpdateEvent])
      })
    } catch (err) {
      toast.error(String(err))
    } finally {
      setUpdating(false)
      loadData()
    }
  }

  const openEditAddress = (node: ClusterNode) => {
    setEditingNode(node)
    setEditDialogOpen(true)
  }

  const handleLeave = async () => {
    if (!(await confirm({ title: t('cluster.leave.confirm'), danger: true }))) return
    try {
      await api.leaveCluster()
      toast.success(t('cluster.leave.success'))
    } catch (err) {
      if ((err as { status?: number }).status === 409) {
        if (!(await confirm({ title: t('cluster.leave.forceConfirm'), danger: true }))) return
        try {
          await api.leaveCluster(true)
          toast.success(t('cluster.leave.success'))
        } catch (forceErr) {
          toast.error(String(forceErr))
        }
        return
      }
      toast.error(String(err))
    }
  }

  if (!status?.enabled) {
    return <ClusterInitForm />
  }

  const nodes = overview?.nodes || []
  const metrics = overview?.metrics || []
  const onlineCount = nodes.filter(n => n.status === 'online').length

  const avgCpu = metrics.length > 0 ? metrics.reduce((s, m) => s + m.cpu_percent, 0) / metrics.length : 0
  const avgMem = metrics.length > 0 ? metrics.reduce((s, m) => s + m.memory_percent, 0) / metrics.length : 0
  const avgDisk = metrics.length > 0 ? metrics.reduce((s, m) => s + m.disk_percent, 0) / metrics.length : 0
  const totalContainers = metrics.reduce((s, m) => s + m.container_count, 0)

  const statCards = [
    { label: t('cluster.overview.nodes'), value: `${onlineCount}/${nodes.length}`, icon: Server, color: 'var(--primary)' },
    { label: t('cluster.overview.avgCpu'), value: `${avgCpu.toFixed(1)}%`, icon: Cpu, color: avgCpu > 80 ? 'var(--destructive)' : avgCpu > 50 ? 'var(--warning)' : 'var(--primary)' },
    { label: t('cluster.overview.avgMemory'), value: `${avgMem.toFixed(1)}%`, icon: MemoryStick, color: avgMem > 80 ? 'var(--destructive)' : avgMem > 50 ? 'var(--warning)' : 'var(--success)' },
    { label: t('cluster.overview.avgDisk'), value: `${avgDisk.toFixed(1)}%`, icon: HardDrive, color: avgDisk > 80 ? 'var(--destructive)' : avgDisk > 50 ? 'var(--warning)' : 'var(--primary)' },
    { label: t('cluster.overview.containers'), value: String(totalContainers), icon: Container, color: 'var(--primary)' },
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
      {/* Cluster info */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-3">
            <div className="h-8 w-8 rounded-lg bg-primary/10 flex items-center justify-center">
              <Server className="h-4 w-4 text-primary" />
            </div>
            <div>
              <h2 className="text-[15px] font-semibold">{overview?.name || status.name}</h2>
              <p className="text-[11px] text-muted-foreground">
                {t('cluster.overview.nodeCount', { count: nodes.length })}
              </p>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            {status.is_leader && (
              <div className="flex flex-wrap gap-1">
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
      {updateLog.length > 0 && <ClusterUpdateProgress updateLog={updateLog} />}

      {/* Stats */}
      <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
        {statCards.map((card) => (
          <div key={card.label} className="bg-card rounded-2xl p-5 card-shadow">
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
            const isLeader = node.id === status.leader_id

            return (
              <div key={node.id} className="px-5 py-4 flex items-center gap-4">
                <div className="flex items-center gap-3 min-w-[200px]">
                  <span className={cn('h-2.5 w-2.5 rounded-full', nodeStatusColor(node.status))} />
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="text-[13px] font-medium">{node.name}</span>
                      {isLeader && nodes.length > 1 && (
                        <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-primary/10 text-primary">
                          <Crown className="h-3 w-3" />
                          {t('layout.cluster.leader')}
                        </span>
                      )}
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
        title={t('cluster.overview.disbandConfirmTitle')}
        description={t('cluster.overview.disbandConfirmDesc', { name: overview?.name || status.name || '' })}
        confirmPhrase={overview?.name || status.name || ''}
        confirmLabel={t('cluster.overview.disband')}
        loading={disbanding}
        onConfirm={handleDisband}
      />
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
