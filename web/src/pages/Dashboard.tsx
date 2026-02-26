import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  Cpu,
  MemoryStick,
  HardDrive,
  Network,
  Server,
  Container,
  FolderOpen,
  Package,
  Clock,
  FileText,
  Activity,
  ArrowUpRight,
  ArrowDownLeft,
} from 'lucide-react'
import { api } from '@/lib/api'
import { useWebSocket } from '@/hooks/useWebSocket'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import MetricsCard from '@/components/MetricsCard'
import MetricsChart from '@/components/MetricsChart'
import type { HostInfo, Metrics } from '@/types/api'

// 24h at 30s intervals = 2880 points; cap to keep chart readable
const MAX_CHART_POINTS = 2880

interface ProcessInfo {
  pid: number
  name: string
  cpu: number
  memory: number
  status: string
}

interface ContainerSummary {
  Id: string
  Names: string[]
  Image: string
  State: string
  Status: string
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const mins = Math.floor((seconds % 3600) / 60)
  return `${days}d ${hours}h ${mins}m`
}

function formatBytes(bytes: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(1)} ${units[i]}`
}

const quickActions = [
  { to: '/files', labelKey: 'dashboard.actionFiles', icon: FolderOpen, color: 'bg-primary/8 text-primary' },
  { to: '/docker', labelKey: 'dashboard.actionDocker', icon: Container, color: 'bg-[#00c471]/8 text-[#00c471]' },
  { to: '/packages', labelKey: 'dashboard.actionPackages', icon: Package, color: 'bg-[#f59e0b]/8 text-[#f59e0b]' },
  { to: '/cron', labelKey: 'dashboard.actionCron', icon: Clock, color: 'bg-[#8b5cf6]/8 text-[#8b5cf6]' },
  { to: '/logs', labelKey: 'dashboard.actionLogs', icon: FileText, color: 'bg-[#00c471]/8 text-[#00c471]' },
]

export default function Dashboard() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [hostInfo, setHostInfo] = useState<HostInfo | null>(null)
  const [primaryIP, setPrimaryIP] = useState<string>('')
  const [metrics, setMetrics] = useState<Metrics | null>(null)
  const [netRate, setNetRate] = useState<{ sent: number; recv: number }>({ sent: 0, recv: 0 })
  const [, setPrevNet] = useState<{ sent: number; recv: number; ts: number } | null>(null)
  const [chartData, setChartData] = useState<Array<{ time: string; cpu: number; memory: number }>>([])
  const [processes, setProcesses] = useState<ProcessInfo[]>([])
  const [containers, setContainers] = useState<ContainerSummary[]>([])
  const [recentLogs, setRecentLogs] = useState<string[]>([])

  // Fetch primary IP address
  useEffect(() => {
    api.getNetworkInterfaces().then((interfaces) => {
      const defaultIf = interfaces.find((i: any) => i.is_default && i.state === 'up')
      if (defaultIf && defaultIf.addresses.length > 0) {
        const ipv4 = defaultIf.addresses.find((a: any) => a.family === 'ipv4')
        if (ipv4) setPrimaryIP(ipv4.address)
      }
    }).catch(() => {})
  }, [])

  // Fetch host info and history on mount
  useEffect(() => {
    api.getSystemInfo().then((data) => {
      setHostInfo(data.host)
      if (data.metrics) {
        setMetrics(data.metrics)
      }
    }).catch(() => {})

    // Load 24h history for the chart
    api.getMetricsHistory().then((history) => {
      const points = history.map((pt) => {
        const d = new Date(pt.time)
        return {
          time: d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit' }),
          cpu: pt.cpu,
          memory: pt.mem_percent,
        }
      })
      setChartData(points)
    }).catch(() => {})
  }, [])

  // Fetch extra dashboard data
  useEffect(() => {
    api.getTopProcesses().then(setProcesses).catch(() => {})
    api.getContainers().then((data) => setContainers(data || [])).catch(() => setContainers([]))
    api.readLog('syslog', 8).then((data) => setRecentLogs(data.lines.slice(-8))).catch(() => {})
  }, [])

  // Refresh processes every 10 seconds
  useEffect(() => {
    const interval = setInterval(() => {
      api.getTopProcesses().then(setProcesses).catch(() => {})
    }, 10000)
    return () => clearInterval(interval)
  }, [])

  // WebSocket handler
  const onMessage = useCallback((data: Metrics) => {
    setMetrics(data)
    // Calculate network rate (bytes/sec) from cumulative deltas
    setPrevNet((prev) => {
      if (prev) {
        const dtSec = (data.timestamp - prev.ts) / 1000
        if (dtSec > 0) {
          const sentRate = Math.max(0, (data.net_bytes_sent - prev.sent) / dtSec)
          const recvRate = Math.max(0, (data.net_bytes_recv - prev.recv) / dtSec)
          setNetRate({ sent: sentRate, recv: recvRate })
        }
      }
      return { sent: data.net_bytes_sent, recv: data.net_bytes_recv, ts: data.timestamp }
    })
    const now = new Date()
    const timeLabel = now.toLocaleTimeString('en-US', {
      hour12: false,
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    })
    setChartData((prev) => {
      const next = [...prev, { time: timeLabel, cpu: data.cpu, memory: data.mem_percent }]
      if (next.length > MAX_CHART_POINTS) {
        return next.slice(next.length - MAX_CHART_POINTS)
      }
      return next
    })
  }, [])

  const { connected } = useWebSocket({
    url: '/ws/metrics',
    onMessage,
  })

  const runningContainers = containers.filter((c) => c.State === 'running').length
  const stoppedContainers = containers.length - runningContainers

  return (
    <div className="space-y-6 max-w-[1400px]">
      {/* Page header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold tracking-tight">{t('dashboard.title')}</h1>
          <p className="text-muted-foreground text-[13px] mt-0.5">{t('dashboard.subtitle')}</p>
        </div>
        <div className="flex items-center gap-2 bg-card rounded-full px-3 py-1.5 card-shadow">
          <div className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-[#00c471]' : 'bg-destructive'}`} />
          <span className="text-xs font-medium text-muted-foreground">
            {connected ? t('dashboard.live') : t('dashboard.disconnected')}
          </span>
        </div>
      </div>

      {/* Host info section */}
      {hostInfo && (
        <div className="bg-card rounded-2xl p-6 card-shadow">
          <div className="flex items-center gap-2 mb-4">
            <Server className="h-4 w-4 text-muted-foreground" />
            <span className="text-[13px] font-semibold text-foreground">{t('dashboard.hostInfo')}</span>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-7 gap-4">
            {[
              { label: t('dashboard.hostname'), value: hostInfo.hostname },
              { label: t('dashboard.os'), value: hostInfo.os },
              { label: t('dashboard.platform'), value: hostInfo.platform },
              { label: t('dashboard.kernel'), value: hostInfo.kernel },
              { label: t('dashboard.uptime'), value: formatUptime(hostInfo.uptime) },
              { label: t('dashboard.cpuCores'), value: hostInfo.num_cpu },
              { label: t('dashboard.ipAddress'), value: primaryIP || '-', mono: true },
            ].map((item) => (
              <div key={item.label}>
                <p className="text-[11px] text-muted-foreground mb-0.5">{item.label}</p>
                <p className={`text-[13px] font-semibold ${'mono' in item && item.mono ? 'font-mono' : ''}`}>{item.value}</p>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Metrics cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <MetricsCard
          title={t('dashboard.cpuUsage')}
          value={metrics ? `${metrics.cpu.toFixed(1)}%` : '--'}
          percent={metrics?.cpu ?? 0}
          icon={<Cpu className="h-5 w-5" />}
        />
        <MetricsCard
          title={t('dashboard.memory')}
          value={
            metrics
              ? `${formatBytes(metrics.mem_used)} / ${formatBytes(metrics.mem_total)}`
              : '--'
          }
          percent={metrics?.mem_percent ?? 0}
          icon={<MemoryStick className="h-5 w-5" />}
        />
        <MetricsCard
          title={t('dashboard.disk')}
          value={
            metrics
              ? `${formatBytes(metrics.disk_used)} / ${formatBytes(metrics.disk_total)}`
              : '--'
          }
          percent={metrics?.disk_percent ?? 0}
          icon={<HardDrive className="h-5 w-5" />}
        />
        <MetricsCard
          title={t('dashboard.network')}
          value={
            metrics
              ? `↑ ${formatBytes(netRate.sent)}/s  ↓ ${formatBytes(netRate.recv)}/s`
              : '--'
          }
          percent={0}
          icon={<Network className="h-5 w-5" />}
        />
      </div>

      {/* Charts + Docker summary row */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* CPU & Memory Chart — spans 2 columns */}
        <div className="lg:col-span-2">
          <MetricsChart data={chartData} title={t('dashboard.chartTitle')} />
        </div>

        {/* Docker Summary + Network */}
        <div className="space-y-6">
          {/* Docker summary */}
          <div className="bg-card rounded-2xl p-5 card-shadow">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-2">
                <Container className="h-4 w-4 text-muted-foreground" />
                <span className="text-[13px] font-semibold">{t('dashboard.dockerSummary')}</span>
              </div>
              <button onClick={() => navigate('/docker')} className="text-xs text-primary font-medium hover:underline">
                {t('dashboard.viewAll')}
              </button>
            </div>
            {containers.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">{t('dashboard.noContainers')}</p>
            ) : (
              <>
                <div className="grid grid-cols-3 gap-2 mb-4">
                  <div className="text-center py-2.5 rounded-xl bg-[#00c471]/8">
                    <p className="text-xl font-bold text-[#00c471]">{runningContainers}</p>
                    <p className="text-[11px] text-muted-foreground mt-0.5">{t('dashboard.containersRunning')}</p>
                  </div>
                  <div className="text-center py-2.5 rounded-xl bg-secondary">
                    <p className="text-xl font-bold text-muted-foreground">{stoppedContainers}</p>
                    <p className="text-[11px] text-muted-foreground mt-0.5">{t('dashboard.containersStopped')}</p>
                  </div>
                  <div className="text-center py-2.5 rounded-xl bg-primary/8">
                    <p className="text-xl font-bold text-primary">{containers.length}</p>
                    <p className="text-[11px] text-muted-foreground mt-0.5">{t('dashboard.containersTotal')}</p>
                  </div>
                </div>
                <div className="space-y-2">
                  {containers.slice(0, 5).map((c) => (
                    <div key={c.Id} className="flex items-center justify-between py-1">
                      <span className="truncate text-[13px] font-medium">{c.Names?.[0]?.replace(/^\//, '') || c.Id.slice(0, 12)}</span>
                      <span className={`text-[11px] font-medium px-2 py-0.5 rounded-full ${c.State === 'running' ? 'bg-[#00c471]/10 text-[#00c471]' : 'bg-secondary text-muted-foreground'}`}>
                        {c.State}
                      </span>
                    </div>
                  ))}
                </div>
              </>
            )}
          </div>

          {/* Network I/O */}
          {metrics && (
            <div className="bg-card rounded-2xl p-5 card-shadow">
              <div className="flex items-center gap-2 mb-4">
                <Network className="h-4 w-4 text-muted-foreground" />
                <span className="text-[13px] font-semibold">{t('dashboard.network')}</span>
              </div>
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 text-[13px] text-muted-foreground">
                    <ArrowUpRight className="h-3.5 w-3.5 text-primary" />
                    {t('dashboard.sent')}
                  </div>
                  <span className="text-[13px] font-semibold">{formatBytes(netRate.sent)}/s</span>
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 text-[13px] text-muted-foreground">
                    <ArrowDownLeft className="h-3.5 w-3.5 text-[#00c471]" />
                    {t('dashboard.received')}
                  </div>
                  <span className="text-[13px] font-semibold">{formatBytes(netRate.recv)}/s</span>
                </div>
                <div className="border-t border-border pt-3">
                  <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                    <span>{t('dashboard.totalSent')}</span>
                    <span className="font-medium">{formatBytes(metrics.net_bytes_sent)}</span>
                  </div>
                  <div className="flex items-center justify-between text-[11px] text-muted-foreground mt-1.5">
                    <span>{t('dashboard.totalReceived')}</span>
                    <span className="font-medium">{formatBytes(metrics.net_bytes_recv)}</span>
                  </div>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Processes + Recent Logs row */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Top Processes */}
        <div className="bg-card rounded-2xl p-5 card-shadow">
          <div className="flex items-center gap-2 mb-1">
            <Activity className="h-4 w-4 text-muted-foreground" />
            <span className="text-[13px] font-semibold">{t('dashboard.topProcesses')}</span>
          </div>
          <p className="text-[11px] text-muted-foreground mb-4">{t('dashboard.topProcessesDesc')}</p>
          {processes.length === 0 ? (
            <p className="text-[13px] text-muted-foreground">{t('dashboard.noProcesses')}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-16">{t('dashboard.pid')}</TableHead>
                  <TableHead>{t('dashboard.processName')}</TableHead>
                  <TableHead className="w-20 text-right">{t('dashboard.processCpu')}</TableHead>
                  <TableHead className="w-20 text-right">{t('dashboard.processMemory')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {processes.map((p) => (
                  <TableRow key={p.pid}>
                    <TableCell className="font-mono text-[11px]">{p.pid}</TableCell>
                    <TableCell className="truncate max-w-[200px] text-[13px]">{p.name}</TableCell>
                    <TableCell className="text-right font-mono text-[11px]">
                      <span className={p.cpu > 50 ? 'text-destructive' : p.cpu > 20 ? 'text-[#f59e0b]' : ''}>
                        {p.cpu.toFixed(1)}%
                      </span>
                    </TableCell>
                    <TableCell className="text-right font-mono text-[11px]">{p.memory.toFixed(1)}%</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        {/* Recent System Logs */}
        <div className="bg-card rounded-2xl p-5 card-shadow">
          <div className="flex items-center justify-between mb-1">
            <div className="flex items-center gap-2">
              <FileText className="h-4 w-4 text-muted-foreground" />
              <span className="text-[13px] font-semibold">{t('dashboard.recentLogs')}</span>
            </div>
            <button onClick={() => navigate('/logs')} className="text-xs text-primary font-medium hover:underline">
              {t('dashboard.viewAll')}
            </button>
          </div>
          <p className="text-[11px] text-muted-foreground mb-4">{t('dashboard.recentLogsDesc')}</p>
          {recentLogs.length === 0 ? (
            <p className="text-[13px] text-muted-foreground">{t('dashboard.noLogs')}</p>
          ) : (
            <div className="bg-[#191f28] rounded-xl p-3 font-mono text-[11px] text-[#8b95a1] space-y-0.5 overflow-x-auto max-h-[320px]">
              {recentLogs.map((line, i) => (
                <div key={i} className="whitespace-pre leading-5 hover:bg-white/5 px-1.5 rounded-lg">
                  {line}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Quick Actions */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="mb-1">
          <span className="text-[13px] font-semibold">{t('dashboard.quickActions')}</span>
        </div>
        <p className="text-[11px] text-muted-foreground mb-4">{t('dashboard.quickActionsDesc')}</p>
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
          {quickActions.map((action) => (
            <button
              key={action.to}
              onClick={() => navigate(action.to)}
              className="flex flex-col items-center gap-2.5 p-4 rounded-2xl bg-secondary/50 hover:bg-secondary transition-all duration-200 cursor-pointer"
            >
              <div className={`p-2.5 rounded-xl ${action.color}`}>
                <action.icon className="h-5 w-5" />
              </div>
              <span className="text-[13px] font-medium">{t(action.labelKey)}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
