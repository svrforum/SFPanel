import { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
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
  Shield,
} from 'lucide-react'
import { api } from '@/lib/api'
import { useWebSocket } from '@/hooks/useWebSocket'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
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
import FirewallLogMiniTable from '@/components/FirewallLogMiniTable'
import type { LayoutOutletContext } from '@/components/Layout'
import { cn, formatBytes, formatUptime } from '@/lib/utils'
import { parseFirewallLine } from '@/lib/logParsers'
import type { FirewallLogEntry } from '@/lib/logParsers'
import type { DashboardOverview, HostInfo, Metrics } from '@/types/api'

// 24h at 30s intervals = 2880 points; cap to keep chart readable
const MAX_CHART_POINTS = 2880

// How old Layout's shared overview payload may be before we refetch instead of
// reusing it. Covers the mount-together race on first entry (delta well under
// a second) while a later navigation back to the dashboard still gets fresh
// data.
const OVERVIEW_REUSE_MS = 10_000

type ChartRange = '1h' | '4h' | '12h' | '24h'
const CHART_RANGE_MS: Record<ChartRange, number> = {
  '1h': 60 * 60 * 1000,
  '4h': 4 * 60 * 60 * 1000,
  '12h': 12 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
}

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

const quickActions = [
  { to: '/files', labelKey: 'dashboard.actionFiles', icon: FolderOpen, color: 'bg-primary/8 text-primary' },
  { to: '/docker', labelKey: 'dashboard.actionDocker', icon: Container, color: 'bg-success/8 text-success' },
  { to: '/packages', labelKey: 'dashboard.actionPackages', icon: Package, color: 'bg-warning/8 text-warning' },
  { to: '/cron', labelKey: 'dashboard.actionCron', icon: Clock, color: 'bg-chart-4/8 text-chart-4' },
  { to: '/logs', labelKey: 'dashboard.actionLogs', icon: FileText, color: 'bg-success/8 text-success' },
]

// Shared pill-tab control for the chart-range picker and the log-tab switcher,
// which used to copy-paste the same wrapper + button class strings.
function SegmentedControl<T extends string>({
  options,
  value,
  onChange,
  className,
  buttonClassName,
}: {
  options: Array<{ value: T; label: string }>
  value: T
  onChange: (value: T) => void
  className?: string
  buttonClassName?: string
}) {
  return (
    <div className={cn('flex items-center gap-1 bg-secondary/60 rounded-lg p-0.5', className)}>
      {options.map((opt) => (
        <button
          key={opt.value}
          onClick={() => onChange(opt.value)}
          className={cn(
            'py-1 rounded-md text-[11px] font-medium transition-all outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
            buttonClassName ?? 'px-2.5',
            value === opt.value
              ? 'bg-card text-foreground shadow-sm'
              : 'text-muted-foreground hover:text-foreground'
          )}
        >
          {opt.label}
        </button>
      ))}
    </div>
  )
}

export default function Dashboard() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const outletCtx = useOutletContext<LayoutOutletContext | undefined>()
  const [hostInfo, setHostInfo] = useState<HostInfo | null>(null)
  const [primaryIP, setPrimaryIP] = useState<string>('')
  const [metrics, setMetrics] = useState<Metrics | null>(null)
  const [netRate, setNetRate] = useState<{ sent: number; recv: number }>({ sent: 0, recv: 0 })
  const prevNetRef = useRef<{ sent: number; recv: number; ts: number } | null>(null)
  const [chartData, setChartData] = useState<Array<{ ts: number; cpu: number; memory: number; disk: number }>>([])
  const [chartRange, setChartRange] = useState<ChartRange>('1h')
  const [processes, setProcesses] = useState<ProcessInfo[]>([])
  const [containers, setContainers] = useState<ContainerSummary[]>([])
  const [recentLogs, setRecentLogs] = useState<string[]>([])
  const [logTab, setLogTab] = useState<'firewall' | 'syslog'>('firewall')
  const [firewallLogs, setFirewallLogs] = useState<FirewallLogEntry[]>([])
  const [updateAvailable, setUpdateAvailable] = useState<string | null>(null)

  // Fetch primary IP address
  useEffect(() => {
    api.getNetworkInterfaces().then((interfaces) => {
      const defaultIf = interfaces.find((i) => i.is_default && i.state === 'up')
      if (defaultIf && defaultIf.addresses.length > 0) {
        const ipv4 = defaultIf.addresses.find((a) => a.family === 'ipv4')
        if (ipv4) setPrimaryIP(ipv4.address)
      }
    }).catch(() => {})
  }, [])

  const applyOverview = useCallback((data: DashboardOverview) => {
    setHostInfo(data.host)
    if (data.metrics) {
      setMetrics(data.metrics)
    }
    if (data.metrics_history) {
      const points = data.metrics_history.map((pt) => ({
        ts: pt.time,
        cpu: pt.cpu,
        memory: pt.mem_percent,
        disk: pt.disk_percent ?? 0,
      }))
      setChartData(points)
    }
    if (data.update_info?.update_available) {
      setUpdateAvailable(data.update_info.latest_version || null)
    }
  }, [])

  // Host info, metrics, history and update info come from a single aggregate
  // call. Layout fetches the same endpoint for its sidebar version display and
  // shares the payload via Outlet context — reuse it when it matches this node
  // scope and is fresh, so first entry costs one /system/overview round-trip
  // instead of two.
  const sharedOverview = outletCtx?.overview
  useEffect(() => {
    // Under Layout with its fetch still in flight — its arrival re-runs this
    // effect (the context value is the effect's dependency).
    if (outletCtx && !sharedOverview) return
    const reusable =
      sharedOverview?.data &&
      sharedOverview.node === api.currentNode &&
      Date.now() - sharedOverview.at < OVERVIEW_REUSE_MS
        ? sharedOverview.data
        : null
    // Fetch our own when there's no usable shared payload (rendered outside
    // Layout, node mismatch after a node switch, stale, or Layout's fetch
    // failed). Resolving the reused payload through a promise keeps the state
    // updates async in both branches (react-hooks/set-state-in-effect).
    const source = reusable ? Promise.resolve(reusable) : api.getDashboardOverview()
    source.then(applyOverview).catch(() => {})
  }, [outletCtx, sharedOverview, applyOverview])

  // Fetch extra dashboard data
  useEffect(() => {
    api.getContainers().then((data) => setContainers(data || [])).catch(() => setContainers([]))
    // Go's JSON serializer turns an empty []string into null; defaulting to
    // [] before slicing prevents a TypeError that .catch(() => {}) can't see
    // because it's thrown inside the .then.
    api.readLog('syslog', 8).then((data) => setRecentLogs((data.lines ?? []).slice(-8))).catch(() => {})
    api.readLog('firewall', 50).then((data) => {
      const parsed = (data.lines ?? []).slice(-50)
        .map(parseFirewallLine)
        .filter((e): e is FirewallLogEntry => e.parsed)
        .slice(-15)
      setFirewallLogs(parsed)
    }).catch(() => {})
  }, [])

  // Refresh processes every 10 seconds (fires immediately on mount, pauses
  // while the tab is hidden)
  const fetchProcesses = useCallback(() => {
    api.getTopProcesses().then(setProcesses).catch(() => {})
  }, [])
  useVisibleInterval(fetchProcesses, 10000)

  // WebSocket handler
  const onMessage = useCallback((data: Metrics) => {
    setMetrics(data)
    // Calculate network rate (bytes/sec) from cumulative deltas
    const prev = prevNetRef.current
    if (prev) {
      const dtSec = (data.timestamp - prev.ts) / 1000
      if (dtSec > 0) {
        const sentRate = Math.max(0, (data.net_bytes_sent - prev.sent) / dtSec)
        const recvRate = Math.max(0, (data.net_bytes_recv - prev.recv) / dtSec)
        setNetRate({ sent: sentRate, recv: recvRate })
      }
    }
    prevNetRef.current = { sent: data.net_bytes_sent, recv: data.net_bytes_recv, ts: data.timestamp }
    setChartData((prevData) => {
      const next = [...prevData, { ts: Date.now(), cpu: data.cpu, memory: data.mem_percent, disk: data.disk_percent }]
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

  // Derive 'now' from the latest data point's timestamp — the metrics WS
  // sends a fresh sample every ~2s, so anchoring the rolling window to
  // the newest point is visually identical to Date.now(). Avoids the
  // react-hooks/purity violation of calling Date.now() inside useMemo.
  // Empty chart fallback uses 0; the chart is empty before the first sample
  // anyway, so the X domain doesn't matter visually.
  const now = chartData.length > 0 ? chartData[chartData.length - 1].ts : 0

  const filteredChartData = useMemo(() => {
    const cutoff = now - CHART_RANGE_MS[chartRange]
    return chartData.filter((pt) => pt.ts >= cutoff)
  }, [chartData, chartRange, now])

  const chartXDomain = useMemo<[number, number]>(() => {
    return [now - CHART_RANGE_MS[chartRange], now]
  }, [chartRange, now])

  const runningContainers = containers.filter((c) => c.State === 'running').length
  const stoppedContainers = containers.length - runningContainers

  return (
    <div className="space-y-4 md:space-y-6 max-w-[1400px]">
      {/* Page header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold tracking-tight">{t('dashboard.title')}</h1>
          <p className="text-muted-foreground text-[13px] mt-0.5">{t('dashboard.subtitle')}</p>
        </div>
        <div className="flex items-center gap-2 bg-card rounded-full px-3 py-1.5 card-shadow">
          <div className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-success' : 'bg-destructive'}`} />
          <span className="text-xs font-medium text-muted-foreground">
            {connected ? t('dashboard.live') : t('dashboard.disconnected')}
          </span>
        </div>
      </div>

      {/* Update banner */}
      {updateAvailable && (
        <div className="bg-primary/10 border border-primary/20 rounded-2xl px-5 py-3 flex items-center justify-between">
          <span className="text-[13px] font-medium text-primary">
            {t('dashboard.updateBanner', { version: updateAvailable })}
          </span>
          <button
            onClick={() => navigate('/settings?scope=node&tab=system')}
            className="text-[13px] font-medium text-primary hover:underline flex items-center gap-1 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
          >
            {t('dashboard.updateBannerAction')}
            <ArrowUpRight className="h-3.5 w-3.5" />
          </button>
        </div>
      )}

      {/* Host info section */}
      {hostInfo && (
        <div className="bg-card rounded-2xl p-4 md:p-6 card-shadow">
          <div className="flex items-center gap-2 mb-4">
            <Server className="h-4 w-4 text-muted-foreground" />
            <span className="text-[13px] font-semibold text-foreground">{t('dashboard.hostInfo')}</span>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-7 gap-3 md:gap-4">
            {[
              { label: t('dashboard.hostname'), value: hostInfo.hostname },
              { label: t('dashboard.os'), value: hostInfo.os },
              { label: t('dashboard.platform'), value: hostInfo.platform_version ? `${hostInfo.platform} ${hostInfo.platform_version}` : hostInfo.platform },
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
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3 md:gap-4">
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
          subLabel={t('dashboard.swap')}
          subValue={
            metrics
              ? metrics.swap_total > 0
                ? `${formatBytes(metrics.swap_used)} / ${formatBytes(metrics.swap_total)}`
                : t('dashboard.swapDisabled')
              : '--'
          }
          subPercent={metrics?.swap_percent}
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
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 md:gap-6">
        {/* CPU & Memory Chart — spans 2 columns */}
        <div className="lg:col-span-2">
          <MetricsChart
            data={filteredChartData}
            title={t('dashboard.chartTitle')}
            xDomain={chartXDomain}
            headerAction={
              <SegmentedControl
                className="shrink-0"
                buttonClassName="px-2 md:px-2.5"
                options={(['1h', '4h', '12h', '24h'] as ChartRange[]).map((range) => ({
                  value: range,
                  label: t(`dashboard.chartRange${range.toUpperCase() as '1H' | '4H' | '12H' | '24H'}`),
                }))}
                value={chartRange}
                onChange={setChartRange}
              />
            }
          />
        </div>

        {/* Docker Summary + Network */}
        <div className="space-y-4 md:space-y-6">
          {/* Docker summary */}
          <div className="bg-card rounded-2xl p-4 md:p-5 card-shadow">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-2">
                <Container className="h-4 w-4 text-muted-foreground" />
                <span className="text-[13px] font-semibold">{t('dashboard.dockerSummary')}</span>
              </div>
              <button onClick={() => navigate('/docker')} className="text-xs text-primary font-medium hover:underline rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0">
                {t('dashboard.viewAll')}
              </button>
            </div>
            {containers.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">{t('dashboard.noContainers')}</p>
            ) : (
              <>
                <div className="grid grid-cols-3 gap-2 mb-4">
                  <div className="text-center py-2.5 rounded-xl bg-success/8">
                    <p className="text-xl font-bold text-success">{runningContainers}</p>
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
                      <span className={`text-[11px] font-medium px-2 py-0.5 rounded-full ${c.State === 'running' ? 'bg-success/10 text-success' : 'bg-secondary text-muted-foreground'}`}>
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
            <div className="bg-card rounded-2xl p-4 md:p-5 card-shadow">
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
                    <ArrowDownLeft className="h-3.5 w-3.5 text-success" />
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
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 md:gap-6">
        {/* Top Processes */}
        <div className="bg-card rounded-2xl p-4 md:p-5 card-shadow">
          <div className="flex items-center gap-2 mb-1">
            <Activity className="h-4 w-4 text-muted-foreground" />
            <span className="text-[13px] font-semibold">{t('dashboard.topProcesses')}</span>
          </div>
          <p className="text-[11px] text-muted-foreground mb-4">{t('dashboard.topProcessesDesc')}</p>
          {processes.length === 0 ? (
            <p className="text-[13px] text-muted-foreground">{t('dashboard.noProcesses')}</p>
          ) : (
            <div className="overflow-x-auto">
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
                      <span className={p.cpu > 50 ? 'text-destructive' : p.cpu > 20 ? 'text-warning' : ''}>
                        {p.cpu.toFixed(1)}%
                      </span>
                    </TableCell>
                    <TableCell className="text-right font-mono text-[11px]">{p.memory.toFixed(1)}%</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            </div>
          )}
        </div>

        {/* Recent Logs (Firewall / System tabs) */}
        <div className="bg-card rounded-2xl p-4 md:p-5 card-shadow">
          <div className="flex items-center justify-between mb-1 flex-wrap gap-2">
            <div className="flex items-center gap-3">
              <div className="flex items-center gap-2">
                <Shield className="h-4 w-4 text-muted-foreground" />
                <span className="text-[13px] font-semibold">{t('dashboard.recentLogs')}</span>
              </div>
              <SegmentedControl
                options={[
                  { value: 'firewall' as const, label: t('dashboard.logTabFirewall') },
                  { value: 'syslog' as const, label: t('dashboard.logTabSystem') },
                ]}
                value={logTab}
                onChange={setLogTab}
              />
            </div>
            <button
              onClick={() => navigate(logTab === 'firewall' ? '/firewall/logs' : '/logs')}
              className="text-xs text-primary font-medium hover:underline rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
            >
              {t('dashboard.viewAll')}
            </button>
          </div>
          <p className="text-[11px] text-muted-foreground mb-4">{t('dashboard.recentLogsDesc')}</p>

          {logTab === 'firewall' ? (
            firewallLogs.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">{t('dashboard.noFirewallLogs')}</p>
            ) : (
              <FirewallLogMiniTable entries={firewallLogs} />
            )
          ) : (
            recentLogs.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">{t('dashboard.noLogs')}</p>
            ) : (
              <div className="bg-terminal rounded-xl p-3 font-mono text-[11px] text-terminal-foreground space-y-0.5 overflow-x-auto max-h-[320px]">
                {recentLogs.map((line, i) => (
                  // Composite key — line content + position survives the
                  // sliding-window update that re-keys index-only.
                  <div key={`${i}-${line.slice(0, 40)}`} className="whitespace-pre leading-5 hover:bg-white/5 px-1.5 rounded-lg">
                    {line}
                  </div>
                ))}
              </div>
            )
          )}
        </div>
      </div>

      {/* Quick Actions */}
      <div className="bg-card rounded-2xl p-4 md:p-5 card-shadow">
        <div className="mb-1">
          <span className="text-[13px] font-semibold">{t('dashboard.quickActions')}</span>
        </div>
        <p className="text-[11px] text-muted-foreground mb-4">{t('dashboard.quickActionsDesc')}</p>
        <div className="flex gap-2 overflow-x-auto md:grid md:grid-cols-5 md:gap-3 pb-1 md:pb-0 -mx-1 px-1 md:mx-0 md:px-0">
          {quickActions.map((action) => (
            <button
              key={action.to}
              onClick={() => navigate(action.to)}
              className="shrink-0 w-[120px] md:w-auto flex flex-col items-center gap-2.5 p-4 rounded-2xl bg-secondary/50 hover:bg-secondary transition-all duration-200 cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
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
