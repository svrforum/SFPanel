import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { usePolledResource } from '@/hooks/usePolledResource'
import { percentCeiling, prepareSeries } from '@/components/charts/series'

interface Props {
  containerId: string
  metric: 'cpu' | 'mem'
  width?: number
  height?: number
}

/** A zero-based miniature trend, without per-row canvas instances or leaked chart listeners. */
export function ContainerSparkline({ containerId, metric, width = 80, height = 24 }: Props) {
  const { t } = useTranslation()
  const load = useCallback(async () => (await api.getContainerMetrics(containerId, '1h')) ?? [], [containerId])
  const resource = usePolledResource(load, 60000)
  const points = useMemo(() => prepareSeries((resource.data ?? []).map((point) => ({
    ts: point.ts, values: [metric === 'cpu' ? point.cpu_percent : point.mem_percent],
  })), 1, 180000), [resource.data, metric])
  const observed = points.filter((point) => point.values[0] != null)
  const label = t(metric === 'cpu' ? 'charts.cpuTrend' : 'charts.memoryTrend')
  if (!observed.length || resource.error) {
    return <span title={t(resource.error ? 'dashboard.loadFailed' : resource.loading ? 'common.loading' : 'charts.noSamples')}
      aria-label={`${label}: ${t(resource.error ? 'dashboard.loadFailed' : resource.loading ? 'common.loading' : 'charts.noSamples')}`}
      className="inline-block shrink-0 text-center text-xs text-muted-foreground" style={{ width, height, lineHeight: `${height}px` }}>—</span>
  }
  const end = points.at(-1)!.ts
  const start = end - 3600000
  const ceiling = percentCeiling(points, [true])
  const x = (ts: number) => 2 + Math.max(0, (ts - start) / (end - start)) * (width - 4)
  const y = (value: number) => height - 2 - value / ceiling * (height - 4)
  const path = points.map((point, index) => {
    const value = point.values[0]
    if (value == null) return ''
    const command = index === 0 || points[index - 1].values[0] == null ? 'M' : 'L'
    return `${command}${x(point.ts).toFixed(2)},${y(value).toFixed(2)}`
  }).join(' ')
  const latest = observed.at(-1)!
  return (
    <svg role="img" aria-label={`${label}: ${latest.values[0]!.toFixed(1)}%`} width={width} height={height}
      viewBox={`0 0 ${width} ${height}`} className="inline-block shrink-0 align-middle">
      <title>{label} · {t('charts.zeroBaseline')}</title>
      <path d={`M2,${height - 2} H${width - 2}`} stroke="currentColor" opacity="0.15" />
      <path d={path} fill="none" stroke={metric === 'cpu' ? '#3b82f6' : '#a855f7'} strokeWidth="1.5" />
      <circle cx={x(latest.ts)} cy={y(latest.values[0]!)} r="2" fill={metric === 'cpu' ? '#3b82f6' : '#a855f7'} />
    </svg>
  )
}
