import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Cpu, MemoryStick, Info, Globe, ChevronRight, HardDrive, Network, Variable } from 'lucide-react'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import type {
  ContainerInspectDetail,
  ContainerStatsResult,
  ContainerInspectPort,
  ContainerInspectMount,
  ContainerInspectNetwork,
} from '@/types/api'

type SingleContainerStats = Omit<ContainerStatsResult, 'id'>

// ContainerInspect renders the live inspect detail (resource stats + general
// info + ports/mounts/networks/env) for a single container. Shared by the
// Docker containers list and the Docker stack service rows so both open the
// same detail panel.
export default function ContainerInspect({ containerId }: { containerId: string }) {
  const { t } = useTranslation()
  const [data, setData] = useState<ContainerInspectDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [stats, setStats] = useState<SingleContainerStats | null>(null)
  const statsInterval = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const inspectData = await api.inspectContainer(containerId)
        if (cancelled) return
        setData(inspectData)
        if (inspectData.state === 'running') {
          const statsData = await api.containerStats(containerId)
          if (cancelled) return
          setStats(statsData)
          statsInterval.current = setInterval(async () => {
            try {
              const s = await api.containerStats(containerId)
              if (!cancelled) setStats(s)
            } catch { /* ignore */ }
          }, 3000)
        }
      } catch (err) {
        if (!cancelled) toast.error(err instanceof Error ? err.message : t('docker.containers.fetchFailed'))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => {
      cancelled = true
      if (statsInterval.current) clearInterval(statsInterval.current)
    }
  }, [containerId, t])

  if (loading) {
    return <div className="flex items-center justify-center py-8 text-muted-foreground">{t('common.loading')}</div>
  }

  if (!data) return null

  return (
    <div className="space-y-4 max-h-[500px] overflow-y-auto overflow-x-hidden pr-1 min-w-0">
      {/* Resource Stats */}
      {stats && (
        <div className="grid grid-cols-2 gap-3">
          <div className="bg-secondary/30 rounded-xl py-3 px-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Cpu className="h-4 w-4 text-primary" />
                <span className="text-[13px] font-medium">CPU</span>
              </div>
              <span className="text-lg font-bold">{stats.cpu_percent.toFixed(1)}%</span>
            </div>
            <div className="mt-2 h-1.5 bg-secondary rounded-full overflow-hidden">
              <div
                className="h-full rounded-full transition-all duration-500"
                style={{
                  width: `${Math.min(stats.cpu_percent, 100)}%`,
                  backgroundColor: stats.cpu_percent > 80 ? '#f04452' : stats.cpu_percent > 50 ? '#f59e0b' : '#3182f6'
                }}
              />
            </div>
          </div>
          <div className="bg-secondary/30 rounded-xl py-3 px-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <MemoryStick className="h-4 w-4 text-[#8b5cf6]" />
                <span className="text-[13px] font-medium">{t('docker.containers.memory')}</span>
              </div>
              <span className="text-lg font-bold">{stats.mem_percent.toFixed(1)}%</span>
            </div>
            <div className="flex items-center justify-between text-[11px] text-muted-foreground mt-1">
              <span>{formatBytes(stats.mem_usage)}</span>
              <span>{formatBytes(stats.mem_limit)}</span>
            </div>
            <div className="mt-1 h-1.5 bg-secondary rounded-full overflow-hidden">
              <div
                className="h-full rounded-full transition-all duration-500"
                style={{
                  width: `${Math.min(stats.mem_percent, 100)}%`,
                  backgroundColor: stats.mem_percent > 80 ? '#f04452' : stats.mem_percent > 50 ? '#f59e0b' : '#8b5cf6'
                }}
              />
            </div>
          </div>
        </div>
      )}

      {/* General Info */}
      <div className="space-y-1">
        <h4 className="text-sm font-semibold flex items-center gap-1.5">
          <Info className="h-3.5 w-3.5" />
          {t('docker.containers.generalInfo')}
        </h4>
        <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm bg-muted/30 rounded-lg p-3">
          <div className="text-muted-foreground shrink-0">{t('docker.containers.image')}</div>
          <div className="font-mono text-xs truncate min-w-0 text-right" title={data.image}>{data.image}</div>
          <div className="text-muted-foreground shrink-0">{t('docker.containers.command')}</div>
          <div className="font-mono text-xs truncate min-w-0 text-right" title={data.cmd || data.entrypoint}>{data.cmd || data.entrypoint || '-'}</div>
          <div className="text-muted-foreground shrink-0">{t('docker.containers.workingDir')}</div>
          <div className="font-mono text-xs truncate min-w-0 text-right" title={data.working_dir || '/'}>{data.working_dir || '/'}</div>
          <div className="text-muted-foreground shrink-0">{t('docker.containers.hostname')}</div>
          <div className="font-mono text-xs truncate min-w-0 text-right" title={data.hostname}>{data.hostname}</div>
          <div className="text-muted-foreground shrink-0">{t('docker.containers.startedAt')}</div>
          <div className="text-xs truncate min-w-0 text-right">{data.started_at ? new Date(data.started_at).toLocaleString() : '-'}</div>
        </div>
      </div>

      {/* Ports */}
      {data.ports && data.ports.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold flex items-center gap-1.5">
            <Globe className="h-3.5 w-3.5" />
            {t('docker.containers.portBindings')} ({data.ports.length})
          </h4>
          <div className="bg-muted/30 rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b">
                  <th className="text-left px-3 py-1.5 text-xs text-muted-foreground font-medium">{t('docker.containers.hostPort')}</th>
                  <th className="text-left px-3 py-1.5 text-xs text-muted-foreground font-medium"><ChevronRight className="h-3 w-3 inline" /></th>
                  <th className="text-left px-3 py-1.5 text-xs text-muted-foreground font-medium">{t('docker.containers.containerPort')}</th>
                  <th className="text-left px-3 py-1.5 text-xs text-muted-foreground font-medium">{t('docker.containers.protocol')}</th>
                </tr>
              </thead>
              <tbody>
                {data.ports.map((p: ContainerInspectPort, i: number) => (
                  <tr key={i} className="border-b last:border-0">
                    <td className="px-3 py-1 font-mono text-xs">{p.host_port ? `${p.host_ip || '0.0.0.0'}:${p.host_port}` : '-'}</td>
                    <td className="px-3 py-1"><ChevronRight className="h-3 w-3 text-muted-foreground" /></td>
                    <td className="px-3 py-1 font-mono text-xs">{p.container_port}</td>
                    <td className="px-3 py-1 text-xs text-muted-foreground">{p.protocol}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Mounts */}
      {data.mounts && data.mounts.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold flex items-center gap-1.5">
            <HardDrive className="h-3.5 w-3.5" />
            {t('docker.containers.volumes')} ({data.mounts.length})
          </h4>
          <div className="space-y-1">
            {data.mounts.map((m: ContainerInspectMount, i: number) => (
              <div key={i} className="bg-muted/30 rounded-lg px-3 py-2 text-xs font-mono flex items-center gap-2 min-w-0">
                <span className="inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border shrink-0">{m.type}</span>
                <span className="truncate min-w-0 flex-1" title={m.source}>{m.source}</span>
                <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
                <span className="truncate min-w-0 flex-1" title={m.destination}>{m.destination}</span>
                <span className={`inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium ml-auto shrink-0 ${m.rw === 'true' ? 'bg-secondary text-secondary-foreground' : 'border border-border'}`}>
                  {m.rw === 'true' ? 'rw' : 'ro'}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Networks */}
      {data.networks && data.networks.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold flex items-center gap-1.5">
            <Network className="h-3.5 w-3.5" />
            {t('docker.containers.networkInfo')} ({data.networks.length})
          </h4>
          <div className="space-y-1">
            {data.networks.map((n: ContainerInspectNetwork, i: number) => (
              <div key={i} className="bg-muted/30 rounded-lg px-3 py-2 text-xs flex flex-wrap items-center gap-x-4 gap-y-1 min-w-0">
                <span className="font-medium truncate min-w-0">{n.name}</span>
                <span className="font-mono text-muted-foreground">IP: {n.ip_address || '-'}</span>
                <span className="font-mono text-muted-foreground">GW: {n.gateway || '-'}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Environment Variables */}
      {data.env && data.env.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-sm font-semibold flex items-center gap-1.5">
            <Variable className="h-3.5 w-3.5" />
            {t('docker.containers.envVars')} ({data.env.length})
          </h4>
          <div className="bg-muted/30 rounded-lg p-3 max-h-[200px] overflow-y-auto">
            {data.env.map((e: string, i: number) => {
              const eqIdx = e.indexOf('=')
              const key = eqIdx >= 0 ? e.substring(0, eqIdx) : e
              const val = eqIdx >= 0 ? e.substring(eqIdx + 1) : ''
              return (
                <div key={i} className="text-xs font-mono py-0.5 flex min-w-0">
                  <span className="text-blue-400 shrink-0 break-all">{key}</span>
                  <span className="text-muted-foreground mx-1 shrink-0">=</span>
                  <span className="text-foreground truncate min-w-0" title={val}>{val}</span>
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
