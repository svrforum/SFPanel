import { useEffect, useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Server, Cpu, MemoryStick, HardDrive, Container, Crown, Bell, Loader2, Power, Download } from 'lucide-react'
import { api } from '@/lib/api'
import type { ClusterOverview as ClusterOverviewType, ClusterStatus, ClusterEvent } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { toast } from 'sonner'

export default function ClusterOverview() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [overview, setOverview] = useState<ClusterOverviewType | null>(null)
  const [events, setEvents] = useState<ClusterEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [updating, setUpdating] = useState(false)
  const [updateLog, setUpdateLog] = useState<Array<{ node_name?: string; step?: string; message?: string; overall?: string }>>([])

  const loadData = useCallback(() => {
    Promise.all([
      api.getClusterStatus(),
      api.getClusterOverview().catch(() => null),
      api.getClusterEvents(20).catch(() => ({ events: [] })),
    ]).then(([s, o, e]) => {
      setStatus(s)
      setOverview(o)
      setEvents(e.events)
    }).finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    loadData()
    const interval = setInterval(loadData, 15000)
    return () => clearInterval(interval)
  }, [loadData])

  if (loading) {
    return (
      <div className="flex items-center justify-center h-32">
        <div className="h-5 w-5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  const handleDisband = async () => {
    if (!confirm(t('cluster.overview.confirmDisband'))) return
    try {
      await api.disbandCluster()
      toast.success(t('cluster.overview.disbanded'))
      setTimeout(() => {
        const check = setInterval(() => {
          fetch('/api/v1/cluster/status', {
            headers: { 'Authorization': `Bearer ${localStorage.getItem('token')}` },
          })
            .then((r) => { if (r.ok) { clearInterval(check); window.location.reload() } })
            .catch(() => {})
        }, 2000)
      }, 1000)
    } catch (err) {
      toast.error(String(err))
    }
  }

  const handleClusterUpdate = async (mode: 'rolling' | 'simultaneous') => {
    if (!confirm(t('cluster.overview.confirmUpdate'))) return
    setUpdating(true)
    setUpdateLog([])
    try {
      await api.clusterUpdateStream(mode, (data) => {
        setUpdateLog(prev => [...prev, data as typeof prev[0]])
      })
    } catch (err) {
      toast.error(String(err))
    } finally {
      setUpdating(false)
      loadData()
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
    { label: t('cluster.overview.nodes'), value: `${onlineCount}/${nodes.length}`, icon: Server, color: '#3182f6' },
    { label: t('cluster.overview.avgCpu'), value: `${avgCpu.toFixed(1)}%`, icon: Cpu, color: avgCpu > 80 ? '#f04452' : avgCpu > 50 ? '#f59e0b' : '#3182f6' },
    { label: t('cluster.overview.avgMemory'), value: `${avgMem.toFixed(1)}%`, icon: MemoryStick, color: avgMem > 80 ? '#f04452' : avgMem > 50 ? '#f59e0b' : '#00c471' },
    { label: t('cluster.overview.avgDisk'), value: `${avgDisk.toFixed(1)}%`, icon: HardDrive, color: avgDisk > 80 ? '#f04452' : avgDisk > 50 ? '#f59e0b' : '#3182f6' },
    { label: t('cluster.overview.containers'), value: String(totalContainers), icon: Container, color: '#3182f6' },
  ]

  return (
    <div className="space-y-6">
      {/* Cluster info */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex items-center justify-between">
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
          <div className="flex gap-2">
            {status.is_leader && (
              <div className="flex gap-1">
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
              className="rounded-xl text-[#f04452] hover:text-[#f04452] hover:bg-[#f04452]/10 border-[#f04452]/20"
              onClick={handleDisband}
            >
              <Power className="h-3.5 w-3.5 mr-1.5" />
              {t('cluster.overview.disband')}
            </Button>
          </div>
        </div>
      </div>

      {/* Update progress */}
      {updateLog.length > 0 && (
        <div className="bg-card rounded-2xl p-5 card-shadow">
          <h3 className="text-[15px] font-semibold mb-3">{t('cluster.overview.updateProgress')}</h3>
          <div className="space-y-1 max-h-48 overflow-y-auto">
            {updateLog.map((entry, i) => (
              <div key={i} className="flex items-center gap-2 text-[12px]">
                {entry.step === 'complete' || entry.step === 'online' ? (
                  <span className="inline-flex h-1.5 w-1.5 rounded-full bg-[#00c471]" />
                ) : entry.step === 'error' ? (
                  <span className="inline-flex h-1.5 w-1.5 rounded-full bg-[#f04452]" />
                ) : entry.overall === 'complete' ? (
                  <span className="inline-flex h-1.5 w-1.5 rounded-full bg-[#00c471]" />
                ) : (
                  <span className="inline-flex h-1.5 w-1.5 rounded-full bg-[#3182f6] animate-pulse" />
                )}
                <span className="text-muted-foreground">{entry.node_name || 'Cluster'}</span>
                <span>{entry.message || entry.overall}</span>
              </div>
            ))}
          </div>
        </div>
      )}

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
            const statusColor = node.status === 'online' ? '#00c471' : node.status === 'suspect' ? '#f59e0b' : '#f04452'

            return (
              <div key={node.id} className="px-5 py-4 flex items-center gap-4">
                <div className="flex items-center gap-3 min-w-[200px]">
                  <span className={cn('h-2.5 w-2.5 rounded-full')} style={{ backgroundColor: statusColor }} />
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="text-[13px] font-medium">{node.name}</span>
                      {isLeader && nodes.length > 1 && (
                        <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-[#3182f6]/10 text-[#3182f6]">
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
    </div>
  )
}

function MetricBar({ label, value }: { label: string; value: number }) {
  const color = value > 80 ? '#f04452' : value > 50 ? '#f59e0b' : '#3182f6'
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

function ClusterInitForm() {
  const { t } = useTranslation()
  const [clusterName, setClusterName] = useState('sfpanel')
  const [interfaces, setInterfaces] = useState<{ name: string; address: string }[]>([])
  const [selectedAddr, setSelectedAddr] = useState('')
  const [initializing, setInitializing] = useState(false)
  const [restarting, setRestarting] = useState(false)

  useEffect(() => {
    api.getClusterInterfaces()
      .then((data) => {
        setInterfaces(data.interfaces || [])
        if (data.interfaces?.length > 0) {
          setSelectedAddr(data.interfaces[0].address)
        }
      })
      .catch(() => {})
  }, [])

  const handleInit = async () => {
    if (!clusterName.trim()) return
    setInitializing(true)
    try {
      await api.initCluster(clusterName.trim(), selectedAddr)
      toast.success(t('cluster.init.success'))
      setRestarting(true)
      // Wait for service restart, then reload
      setTimeout(() => {
        const check = setInterval(() => {
          fetch('/api/v1/cluster/status', {
            headers: { 'Authorization': `Bearer ${localStorage.getItem('token')}` },
          })
            .then((r) => {
              if (r.ok) {
                clearInterval(check)
                window.location.reload()
              }
            })
            .catch(() => {})
        }, 2000)
      }, 1000)
    } catch (err) {
      toast.error(String(err))
      setInitializing(false)
    }
  }

  if (restarting) {
    return (
      <div className="bg-card rounded-2xl p-8 card-shadow text-center space-y-4">
        <Loader2 className="h-10 w-10 text-primary mx-auto animate-spin" />
        <h2 className="text-[15px] font-semibold">{t('cluster.init.restarting')}</h2>
        <p className="text-[13px] text-muted-foreground">{t('cluster.init.restartingDesc')}</p>
      </div>
    )
  }

  return (
    <div className="bg-card rounded-2xl p-8 card-shadow space-y-6 max-w-lg mx-auto">
      <div className="text-center space-y-2">
        <Server className="h-12 w-12 text-muted-foreground mx-auto" />
        <h2 className="text-[15px] font-semibold">{t('cluster.notEnabled.title')}</h2>
        <p className="text-[13px] text-muted-foreground">
          {t('cluster.notEnabled.description')}
        </p>
      </div>

      <div className="space-y-4">
        {/* Cluster name */}
        <div className="space-y-1.5">
          <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.init.clusterName')}
          </label>
          <Input
            value={clusterName}
            onChange={(e) => setClusterName(e.target.value)}
            placeholder="sfpanel"
            className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
          />
        </div>

        {/* Advertise address */}
        <div className="space-y-1.5">
          <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.init.advertiseAddress')}
          </label>
          {interfaces.length > 0 ? (
            <div className="space-y-1.5">
              {interfaces.map((iface) => (
                <button
                  key={`${iface.name}-${iface.address}`}
                  onClick={() => setSelectedAddr(iface.address)}
                  className={cn(
                    'w-full flex items-center justify-between px-3 py-2 rounded-xl text-[13px] transition-colors',
                    selectedAddr === iface.address
                      ? 'bg-primary/10 ring-1 ring-primary/20'
                      : 'bg-secondary/50 hover:bg-secondary'
                  )}
                >
                  <span className="font-medium">{iface.address}</span>
                  <span className="text-[11px] text-muted-foreground">{iface.name}</span>
                </button>
              ))}
            </div>
          ) : (
            <Input
              value={selectedAddr}
              onChange={(e) => setSelectedAddr(e.target.value)}
              placeholder="192.168.1.100"
              className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
            />
          )}
          <p className="text-[11px] text-muted-foreground">
            {t('cluster.init.advertiseAddressDesc')}
          </p>
        </div>
      </div>

      <Button
        onClick={handleInit}
        disabled={initializing || !clusterName.trim()}
        className="w-full rounded-xl"
      >
        {initializing ? (
          <>
            <Loader2 className="h-4 w-4 mr-2 animate-spin" />
            {t('cluster.init.initializing')}
          </>
        ) : (
          t('cluster.init.button')
        )}
      </Button>
    </div>
  )
}

function EventDot({ type }: { type: string }) {
  const color = type.includes('offline') || type.includes('left')
    ? '#f04452'
    : type.includes('suspect')
      ? '#f59e0b'
      : type.includes('online') || type.includes('joined')
        ? '#00c471'
        : '#3182f6'

  return <span className="h-2 w-2 rounded-full shrink-0" style={{ backgroundColor: color }} />
}
