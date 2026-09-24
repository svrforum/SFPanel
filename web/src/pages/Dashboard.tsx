import { useState, useCallback, useMemo, useRef } from 'react'
import { Link, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Cpu, MemoryStick, HardDrive, Network, Server, AlertTriangle, ArrowUpRight, ChevronDown } from 'lucide-react'
import { api } from '@/lib/api'
import { useWebSocket } from '@/hooks/useWebSocket'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import MetricsCard from '@/components/MetricsCard'
import MetricsChart from '@/components/MetricsChart'
import FirewallLogMiniTable from '@/components/FirewallLogMiniTable'
import type { LayoutOutletContext } from '@/components/Layout'
import { cn, formatBytes, formatDate, formatUptime } from '@/lib/utils'
import { parseFirewallLine, type FirewallLogEntry } from '@/lib/logParsers'
import type { Metrics } from '@/types/api'
import { worstFilesystem, rootFilesystem } from '@/lib/filesystems'
import { containerHealth, compareForSummary, needsAttention, type ContainerHealth } from '@/lib/containerState'
import { usePolledResource } from '@/hooks/usePolledResource'
import ResourceStatus from '@/components/ResourceStatus'
import DashboardSection from './dashboard/DashboardSection'
import QuickActions from './dashboard/QuickActions'

const OVERVIEW_REUSE_MS = 10_000
const CONTAINER_PILL: Record<ContainerHealth, string> = {
  running: 'bg-success/10 text-success', unhealthy: 'bg-warning/15 text-warning',
  restarting: 'bg-warning/15 text-warning', crashed: 'bg-destructive/10 text-destructive',
  stopped: 'bg-secondary text-muted-foreground',
}
type ChartRange = '1h' | '4h' | '12h' | '24h'
const CHART_RANGE_MS: Record<ChartRange, number> = {
  '1h': 3600000, '4h': 14400000, '12h': 43200000, '24h': 86400000,
}
const loadContainers = async () => (await api.getContainers()) ?? []
const loadFilesystems = async () => (await api.getFilesystems()) ?? []
const loadBackup = async () => (await api.getBackupSchedule())?.schedule ?? null
const loadProcesses = async () => (await api.getTopProcesses()) ?? []
const loadInterfaces = async () => (await api.getNetworkInterfaces()) ?? []
const loadSystemLogs = async () => (await api.readLog('syslog', 8)).lines ?? []
const linkClass = 'inline-flex min-h-11 items-center gap-1 rounded-md px-2 text-xs font-medium text-primary hover:underline focus-visible:outline-2 focus-visible:outline-ring'

function SegmentedControl<T extends string>({ options, value, onChange, label }: {
  options: Array<{ value: T; label: string }>; value: T; onChange: (value: T) => void; label: string
}) {
  return <div role="group" aria-label={label} className="flex flex-wrap gap-1 rounded-lg bg-secondary/60 p-1">
    {options.map((opt) => <button key={opt.value} type="button" aria-pressed={value === opt.value} onClick={() => onChange(opt.value)}
      className={cn('min-h-11 min-w-11 rounded-md px-3 text-xs font-medium focus-visible:outline-2 focus-visible:outline-ring', value === opt.value ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>
      {opt.label}
    </button>)}
  </div>
}

export default function Dashboard() {
  const { t, i18n } = useTranslation()
  const outletCtx = useOutletContext<LayoutOutletContext | undefined>()
  const sharedOverview = outletCtx?.overview
  const loadOverview = useCallback(() => {
    const shared = sharedOverview?.data && sharedOverview.node === api.currentNode && Date.now() - sharedOverview.at < OVERVIEW_REUSE_MS
      ? sharedOverview.data : null
    return shared ? Promise.resolve(shared) : api.getDashboardOverview()
  }, [sharedOverview])
  const overview = usePolledResource(loadOverview, 60000, !outletCtx || !!sharedOverview)
  const containers = usePolledResource(loadContainers, 30000)
  const filesystems = usePolledResource(loadFilesystems, 30000)
  const backup = usePolledResource(loadBackup, 60000)
  const processes = usePolledResource(loadProcesses, 10000)
  const interfaces = usePolledResource(loadInterfaces, 60000)
  const systemLogs = usePolledResource(loadSystemLogs, 30000)
  const [logTab, setLogTab] = useState<'firewall' | 'syslog'>('firewall')
  const logTabDecided = useRef(false)
  const loadFirewallLogs = useCallback(async () => {
    const data = await api.readLog('firewall', 50)
    const parsed = (data.lines ?? []).map(parseFirewallLine).filter((e): e is FirewallLogEntry => e.parsed).slice(-15)
    if (!logTabDecided.current) {
      logTabDecided.current = true
      if (parsed.length === 0) setLogTab('syslog')
    }
    return parsed
  }, [])
  const firewallLogs = usePolledResource(loadFirewallLogs, 30000)
  const activeLogs = logTab === 'firewall' ? firewallLogs : systemLogs
  const [chartRange, setChartRange] = useState<ChartRange>('1h')
  const loadHistory = useCallback(async () => (await api.getMetricsHistory(chartRange)) ?? [], [chartRange])
  const history = usePolledResource(loadHistory, 30000)

  const [live, setLive] = useState<{ data: Metrics; at: number } | null>(null)
  const [clock, setClock] = useState(() => Date.now())
  const tick = useCallback(() => setClock(Date.now()), [])
  useVisibleInterval(tick, 5000)
  const [netRate, setNetRate] = useState<{ sent: number; recv: number } | null>(null)
  const prevNet = useRef<Metrics | null>(null)
  const onMessage = useCallback((data: Metrics) => {
    setLive({ data, at: Date.now() })
    const prev = prevNet.current
    const seconds = prev ? (data.timestamp - prev.timestamp) / 1000 : 0
    // Reconnection gaps and counter resets are not measurements of the current speed.
    if (prev && seconds > 0 && seconds <= 15 && data.net_bytes_sent >= prev.net_bytes_sent && data.net_bytes_recv >= prev.net_bytes_recv) {
      setNetRate({ sent: (data.net_bytes_sent - prev.net_bytes_sent) / seconds, recv: (data.net_bytes_recv - prev.net_bytes_recv) / seconds })
    } else setNetRate(null)
    prevNet.current = data
  }, [])
  const { connected } = useWebSocket({ url: '/ws/metrics', onMessage })
  const hostInfo = overview.data?.host
  const metrics = live && (!overview.data?.metrics || live.data.timestamp >= overview.data.metrics.timestamp) ? live.data : overview.data?.metrics
  const metricsAt = metrics === live?.data ? live?.at : overview.updatedAt
  const liveFresh = connected && live != null && clock - live.at < 15000
  const metricsStatus = liveFresh ? 'dashboard.metricsLive' : connected ? 'dashboard.metricsWaiting' : 'dashboard.metricsDisconnected'
  const defaultIf = interfaces.data?.find((item) => item.is_default && item.state === 'up')
  const primaryIP = defaultIf?.addresses.find((item) => item.family === 'ipv4')?.address
  const liveUptime = hostInfo ? hostInfo.uptime + Math.max(0, Math.floor(((metrics?.timestamp ?? 0) - (overview.data?.metrics?.timestamp ?? 0)) / 1000)) : 0
  const updateAvailable = overview.data?.update_info?.update_available ? overview.data.update_info.latest_version : null

  const chartData = useMemo(() => (history.data ?? []).map((point) => ({ ts: point.time, cpu: point.cpu, memory: point.mem_percent, disk: point.disk_percent ?? null })), [history.data])
  // Use server time plus elapsed local time: stale history must leave a visible gap at the right.
  const chartEnd = Math.max(chartData.at(-1)?.ts ?? 0, metrics ? metrics.timestamp + Math.max(0, clock - (metricsAt ?? clock)) : clock)
  const chartXDomain = useMemo<[number, number]>(() => [chartEnd - CHART_RANGE_MS[chartRange], chartEnd], [chartEnd, chartRange])
  const filteredChartData = useMemo(() => chartData.filter((point) => point.ts >= chartXDomain[0]), [chartData, chartXDomain])
  const containerRows = useMemo(() => (containers.data ?? []).map((c) => ({
    id: c.Id, name: c.Names?.[0]?.replace(/^\//, '') || c.Id.slice(0, 12),
    health: containerHealth(c.State, c.Status), cpu: c.cpu_avg_1h ?? null,
  })).sort(compareForSummary), [containers.data])
  const runningCount = containerRows.filter((c) => c.health === 'running').length
  const attentionCount = containerRows.filter((c) => needsAttention(c.health)).length
  const stoppedCount = containerRows.filter((c) => c.health === 'stopped').length
  const worstFs = useMemo(() => worstFilesystem(filesystems.data ?? []), [filesystems.data])
  const rootFs = useMemo(() => rootFilesystem(filesystems.data ?? []), [filesystems.data])
  const backupConfig = backup.data
  const backupLabel = !backupConfig ? '—' : !backupConfig.enabled ? t('dashboard.backupDisabled') : backupConfig.last_status === 'error' ? t('dashboard.backupFailed') : backupConfig.last_run ? formatDate(backupConfig.last_run) : t('dashboard.backupNever')
  const backupFailed = backupConfig?.enabled && backupConfig.last_status === 'error'
  const diskPercent = worstFs?.use_percent ?? metrics?.disk_percent
  const issues = [
    ...(attentionCount ? [{ key: 'containers', to: '/docker/containers', text: t('dashboard.attentionContainers', { count: attentionCount }), stale: containers.error }] : []),
    ...(diskPercent != null && diskPercent >= 80 ? [{ key: 'disk', to: '/disk/filesystems', text: t('dashboard.attentionDisk', { mount: worstFs?.mount_point ?? '/', percent: diskPercent.toFixed(0) }), stale: worstFs ? filesystems.error : !liveFresh }] : []),
    ...(backupFailed ? [{ key: 'backup', to: '/settings?scope=node&tab=system', text: t('dashboard.attentionBackup'), stale: backup.error }] : []),
  ]
  const failedSources = [overview, containers, filesystems, backup, processes, interfaces, history, systemLogs, firewallLogs].filter((resource) => resource.error).length
  const diskDescription = worstFs ? `${worstFs.mount_point} · ${formatBytes(worstFs.used)} / ${formatBytes(worstFs.size)}` : metrics ? `/ · ${formatBytes(metrics.disk_used)} / ${formatBytes(metrics.disk_total)}` : undefined

  return (
    <div className="mx-auto max-w-[1400px] space-y-4 md:space-y-5">
      <header>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-xs font-medium text-muted-foreground">{t('dashboard.title')}</p>
            <h1 className="mt-1 break-all text-xl font-bold tracking-tight">{hostInfo?.hostname || t('dashboard.serverOverview')}</h1>
          </div>
          <span className="inline-flex max-w-[50%] shrink-0 items-center gap-2 rounded-full bg-card px-3 py-2 text-xs text-muted-foreground">
            <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', liveFresh ? 'bg-success' : 'bg-warning')} />{t(metricsStatus)}
          </span>
        </div>
        <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span>{t('dashboard.ipAddress')} · {primaryIP || '—'}</span>
          <span>{t('dashboard.uptime')} · {hostInfo ? formatUptime(liveUptime) : '—'}</span>
          {metricsAt != null && <span>{t('dashboard.updatedAt', { time: new Date(metricsAt).toLocaleTimeString(i18n.language) })}</span>}
        </div>
        {(overview.error || !overview.updatedAt) && <ResourceStatus resource={overview} label={t('dashboard.hostInfo')} />}
      </header>

      {(issues.length > 0 || failedSources > 0) && <section aria-label={t('dashboard.attentionTitle')} className="rounded-2xl border border-warning/30 bg-warning/5 p-3 md:p-4">
        <h2 className="flex items-center gap-2 text-sm font-semibold"><AlertTriangle className="size-4 text-warning" aria-hidden="true" />{t('dashboard.attentionTitle')}</h2>
        {issues.length > 0 && <ul className="mt-1 flex flex-wrap gap-x-4">
          {issues.map((issue) => <li key={issue.key} className="min-w-0"><Link to={issue.to} className="flex min-h-11 items-center gap-2 rounded-md py-2 text-sm font-medium hover:underline focus-visible:outline-2 focus-visible:outline-ring">
            <span className="break-all">{issue.text}{issue.stale && <span className="ml-1 text-xs text-muted-foreground">({t('dashboard.lastKnown')})</span>}</span><ArrowUpRight aria-hidden="true" className="size-4 shrink-0" />
          </Link></li>)}
        </ul>}
        {failedSources > 0 && <p className="mt-1 text-xs text-muted-foreground">{t('dashboard.incompleteData', { count: failedSources })}</p>}
      </section>}

      <QuickActions />

      <section id="resources" aria-label={t('dashboard.resources')}>
        <h2 className="sr-only">{t('dashboard.resources')}</h2>
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <MetricsCard title={t('dashboard.cpuUsage')} value={metrics ? `${metrics.cpu.toFixed(1)}%` : '—'} percent={metrics?.cpu} to="/processes" icon={<Cpu className="size-4" />} description={t('dashboard.openProcesses')} />
          <MetricsCard title={t('dashboard.memory')} value={metrics ? `${metrics.mem_percent.toFixed(1)}%` : '—'} percent={metrics?.mem_percent} to="/processes" icon={<MemoryStick className="size-4" />}
            description={metrics ? `${formatBytes(metrics.mem_used)} / ${formatBytes(metrics.mem_total)}` : undefined}
            subLabel={t('dashboard.swap')} subValue={metrics ? metrics.swap_total > 0 ? `${formatBytes(metrics.swap_used)} / ${formatBytes(metrics.swap_total)}` : t('dashboard.swapDisabled') : undefined} />
          <MetricsCard title={t('dashboard.disk')} value={diskPercent != null ? `${diskPercent.toFixed(1)}%` : '—'} percent={diskPercent} to="/disk/filesystems" icon={<HardDrive className="size-4" />} description={diskDescription}
            subLabel={worstFs?.mount_point !== '/' && rootFs ? '/' : undefined} subValue={rootFs ? `${rootFs.use_percent.toFixed(1)}%` : undefined} />
          <MetricsCard title={t('dashboard.network')} value={netRate && liveFresh ? `↑ ${formatBytes(netRate.sent)}/s\n↓ ${formatBytes(netRate.recv)}/s` : '—'} to="/network/interfaces" icon={<Network className="size-4" />} description={t(netRate && liveFresh ? 'dashboard.networkRate' : 'dashboard.waitingRate')} />
        </div>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-x-4 gap-y-1">
          <ResourceStatus resource={filesystems} label={t('dashboard.disk')} />
          <details className="group text-xs text-muted-foreground">
            <summary className="flex min-h-11 cursor-pointer list-none items-center gap-1 rounded-md focus-visible:outline-2 focus-visible:outline-ring">{t('dashboard.networkTotals')}<ChevronDown className="size-3 group-open:rotate-180" aria-hidden="true" /></summary>
            <p className="pb-2">{t('dashboard.totalSent')} {metrics ? formatBytes(metrics.net_bytes_sent) : '—'} · {t('dashboard.totalReceived')} {metrics ? formatBytes(metrics.net_bytes_recv) : '—'}</p>
          </details>
        </div>
      </section>

      <div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-3">
        <DashboardSection id="resource-history" title={t('dashboard.chartTitle')} className="lg:col-span-2"
          summary={<>
            <ResourceStatus resource={history} />
            {filteredChartData.length > 0 && <p className="mt-1 md:hidden">{t('dashboard.historyPeak', {
              cpu: Math.max(...filteredChartData.map((point) => point.cpu)).toFixed(1),
              memory: Math.max(...filteredChartData.map((point) => point.memory)).toFixed(1),
            })}</p>}
          </>}>
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <p className="text-xs text-muted-foreground">{t('dashboard.historyHint')}</p>
            <SegmentedControl label={t('dashboard.chartRange')} options={(['1h', '4h', '12h', '24h'] as ChartRange[]).map((range) => ({ value: range, label: t(`dashboard.chartRange${range.toUpperCase()}`) }))} value={chartRange} onChange={setChartRange} />
          </div>
          {history.updatedAt != null && (filteredChartData.length > 0 ? <MetricsChart data={filteredChartData} title={t('dashboard.chartTitle')} xDomain={chartXDomain} /> : <p className="py-4 text-sm text-muted-foreground">{t('dashboard.noHistory')}</p>)}
        </DashboardSection>

        <section id="containers" aria-label={t('dashboard.dockerSummary')} className="min-w-0 rounded-2xl bg-card p-4 card-shadow md:p-5">
          <div className="flex items-center justify-between gap-2"><h2 className="text-sm font-semibold">{t('dashboard.dockerSummary')}</h2><Link to="/docker/containers" className={linkClass}>{t('dashboard.viewAll')}</Link></div>
          <ResourceStatus resource={containers} />
          {containers.updatedAt != null && (containerRows.length === 0 ? <p className="mt-3 text-sm text-muted-foreground">{t('dashboard.noContainers')}</p> : <>
            <div className="my-3 grid grid-cols-3 gap-2 text-center">
              {[
                { count: runningCount, label: 'containersRunning', color: 'text-success' },
                { count: attentionCount, label: 'containersAttention', color: attentionCount ? 'text-warning' : 'text-muted-foreground' },
                { count: stoppedCount, label: 'containersStopped', color: 'text-muted-foreground' },
              ].map((item) => <div key={item.label} className="rounded-xl bg-secondary/50 py-2"><p className={cn('text-xl font-bold', item.color)}>{item.count}</p><p className="text-xs text-muted-foreground">{t(`dashboard.${item.label}`)}</p></div>)}
            </div>
            <ul className="divide-y divide-border">
              {containerRows.slice(0, 5).map((c) => <li key={c.id}><Link to={`/docker/containers?container=${encodeURIComponent(c.id)}`} className="flex min-h-11 items-center justify-between gap-2 rounded-md py-2 hover:bg-secondary/50 focus-visible:outline-2 focus-visible:outline-ring">
                <span className="min-w-0 truncate text-sm font-medium">{c.name}</span>
                <span className="flex shrink-0 items-center gap-1.5">
                  {c.cpu != null && <span title={t('dashboard.containerCpuAverage')} className="font-mono text-xs text-muted-foreground">{c.cpu.toFixed(1)}%</span>}
                  <span className={cn('rounded-full px-2 py-1 text-xs font-medium', CONTAINER_PILL[c.health])}>{t(`dashboard.containerState.${c.health}`)}</span><ArrowUpRight className="size-3 text-muted-foreground" aria-hidden="true" />
                </span>
              </Link></li>)}
            </ul>
          </>)}
        </section>
      </div>

      <div className="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
        <section id="processes" aria-label={t('dashboard.topProcesses')} className="min-w-0 rounded-2xl bg-card p-4 card-shadow md:p-5">
          <div className="flex items-center justify-between gap-2"><h2 className="text-sm font-semibold">{t('dashboard.topProcesses')}</h2><Link to="/processes" className={linkClass}>{t('dashboard.viewAll')}</Link></div>
          <ResourceStatus resource={processes} />
          <p className="my-2 text-xs text-muted-foreground">{t('dashboard.topProcessesDesc')}</p>
          {processes.updatedAt != null && ((processes.data ?? []).length === 0 ? <p className="text-sm text-muted-foreground">{t('dashboard.noProcesses')}</p> : <Table>
            <TableHeader><TableRow><TableHead className="hidden sm:table-cell">{t('dashboard.pid')}</TableHead><TableHead>{t('dashboard.processName')}</TableHead><TableHead className="text-right">{t('dashboard.processCpu')}</TableHead><TableHead className="text-right">{t('dashboard.processMemory')}</TableHead></TableRow></TableHeader>
            <TableBody>{processes.data?.slice(0, 5).map((p) => <TableRow key={p.pid}>
              <TableCell className="hidden font-mono text-xs sm:table-cell">{p.pid}</TableCell><TableCell className="max-w-[120px] truncate text-sm" title={p.name}>{p.name}</TableCell>
              <TableCell className={cn("text-right font-mono text-xs", p.cpu > 50 ? "text-destructive" : p.cpu > 20 ? "text-warning" : "")}>{p.cpu.toFixed(1)}%</TableCell><TableCell className="text-right font-mono text-xs">{p.memory.toFixed(1)}%</TableCell>
            </TableRow>)}</TableBody>
          </Table>)}
        </section>

        <DashboardSection id="recent-logs" title={t('dashboard.recentLogs')}
          action={<Link to={logTab === 'firewall' ? '/firewall/logs' : '/logs'} className={linkClass}>{t('dashboard.viewAll')}</Link>}
          summary={<>
            <ResourceStatus resource={activeLogs} label={t(logTab === 'firewall' ? 'dashboard.logTabFirewall' : 'dashboard.logTabSystem')} />
            {(logTab === 'firewall' ? systemLogs.error : firewallLogs.error) && <ResourceStatus resource={logTab === 'firewall' ? systemLogs : firewallLogs} label={t(logTab === 'firewall' ? 'dashboard.logTabSystem' : 'dashboard.logTabFirewall')} />}
            {activeLogs.updatedAt != null && <p className="mt-1">{t('dashboard.logCount', { count: activeLogs.data?.length ?? 0 })}</p>}
          </>}>
          <SegmentedControl label={t('dashboard.recentLogsDesc')} options={[{ value: 'firewall' as const, label: t('dashboard.logTabFirewall') }, { value: 'syslog' as const, label: t('dashboard.logTabSystem') }]} value={logTab} onChange={(tab) => { logTabDecided.current = true; setLogTab(tab) }} />
          <div className="mt-3">
            {activeLogs.updatedAt != null && (logTab === 'firewall' ? (firewallLogs.data?.length ? <FirewallLogMiniTable entries={firewallLogs.data} /> : <p className="text-sm text-muted-foreground">{t('dashboard.noFirewallLogs')}</p>) : (systemLogs.data?.length ? <div className="max-h-64 overflow-auto rounded-xl bg-terminal p-3 font-mono text-xs text-terminal-foreground">
              {systemLogs.data.map((line, index) => <div key={`${index}-${line.slice(0, 40)}`} className="whitespace-pre leading-6">{line}</div>)}
            </div> : <p className="text-sm text-muted-foreground">{t('dashboard.noLogs')}</p>))}
          </div>
        </DashboardSection>
      </div>

      <details id="server-details" className="group rounded-2xl bg-card p-4 card-shadow md:p-5">
        <summary className="flex min-h-11 cursor-pointer list-none items-center gap-2 rounded-md text-sm font-semibold focus-visible:outline-2 focus-visible:outline-ring"><Server className="size-4" aria-hidden="true" />{t('dashboard.hostInfo')}<ChevronDown className="ml-auto size-4 group-open:rotate-180" aria-hidden="true" /></summary>
        <div className="mt-3 space-y-4">
          <ResourceStatus resource={overview} />
          {hostInfo && <dl className="grid grid-cols-2 gap-4 md:grid-cols-4">
            {[
              [t('dashboard.hostname'), hostInfo.hostname], [t('dashboard.os'), hostInfo.os],
              [t('dashboard.platform'), `${hostInfo.platform} ${hostInfo.platform_version || ''}`], [t('dashboard.kernel'), hostInfo.kernel],
              [t('dashboard.uptime'), formatUptime(liveUptime)], [t('dashboard.cpuCores'), hostInfo.num_cpu],
            ].map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-xs text-muted-foreground">{label}</dt><dd className="mt-1 break-all text-sm font-medium">{value}</dd></div>)}
          </dl>}
          <div><p className="text-sm">{t('dashboard.ipAddress')} · {primaryIP || '—'}</p><ResourceStatus resource={interfaces} /></div>
        </div>
      </details>

      <section aria-label={t('dashboard.backupLabel')} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-border pt-3">
        <div><Link to="/settings?scope=node&tab=system" className={linkClass}>{t('dashboard.backupLabel')} · <span className={backupFailed ? 'text-destructive' : ''}>{backupLabel}</span><ArrowUpRight className="size-3" aria-hidden="true" /></Link><ResourceStatus resource={backup} /></div>
        {updateAvailable && <Link to="/settings?scope=node&tab=system" className={linkClass}>{t('dashboard.updateBanner', { version: updateAvailable })}<ArrowUpRight className="size-3 shrink-0" aria-hidden="true" /></Link>}
      </section>
    </div>
  )
}
