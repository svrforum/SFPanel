import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import TimeSeriesChart from './charts/TimeSeriesChart'

interface MetricsChartProps {
  data: Array<{ ts: number; cpu: number; memory: number; disk: number | null }>
  title: string
  xDomain: [number, number]
}

export default function MetricsChart({ data, title, xDomain }: MetricsChartProps) {
  const { t } = useTranslation()
  const series = useMemo(() => [
    { key: 'cpu', label: 'CPU', color: '#3b82f6' },
    { key: 'memory', label: t('dashboard.memory'), color: '#a855f7' },
    // System history records disk.Usage("/"); the headline disk card can show another mount.
    { key: 'disk', label: t('charts.rootDisk'), color: '#d97706' },
  ], [t])
  const points = useMemo(() => data.map((point) => ({ ts: point.ts, values: [point.cpu, point.memory, point.disk] })), [data])
  return <TimeSeriesChart points={points} series={series} title={title} domain={xDomain}
    gapMs={Math.max(180000, (xDomain[1] - xDomain[0]) / 120 * 3)} />
}
