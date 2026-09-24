import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import type { ContainerEvent } from '@/types/api'
import { EventTimelineRow } from '@/components/docker/EventTimelineRow'
import TimeSeriesChart from '@/components/charts/TimeSeriesChart'
import ResourceStatus from '@/components/ResourceStatus'
import { usePolledResource } from '@/hooks/usePolledResource'

type Range = '1h' | '6h' | '24h'
const RANGE_MS = { '1h': 3600000, '6h': 21600000, '24h': 86400000 }

export function ContainerHistoryTab({ containerId }: { containerId: string }) {
  const { t } = useTranslation()
  const [range, setRange] = useState<Range>('1h')
  const loadMetrics = useCallback(async () => (await api.getContainerMetrics(containerId, range)) ?? [], [containerId, range])
  const metrics = usePolledResource(loadMetrics, 30000)
  const loadEvents = useCallback(async () => (await api.getContainerEvents(containerId, { limit: 50 })) ?? [], [containerId])
  const events = usePolledResource(loadEvents, 60000)
  const [older, setOlder] = useState<{ id: string; rows: ContainerEvent[]; hasMore: boolean } | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const [moreError, setMoreError] = useState(false)
  const rows = [...(events.data ?? []), ...(older?.id === containerId ? older.rows : [])]
    .filter((event, index, all) => all.findIndex((item) => item.ts === event.ts && item.event_type === event.event_type && item.detail === event.detail) === index)
  const hasMore = older?.id === containerId ? older.hasMore : (events.data?.length ?? 0) === 50
  const points = useMemo(() => (metrics.data ?? []).map((point) => ({ ts: point.ts, values: [point.cpu_percent, point.mem_percent] })), [metrics.data])
  const series = useMemo(() => [
    { key: 'cpu', label: t('charts.containerCpu'), color: '#3b82f6' },
    { key: 'memory', label: t('dashboard.memory'), color: '#a855f7' },
  ], [t])
  // API timestamps and receipt time are milliseconds. Keep the selected window fixed even with sparse history.
  const end = Math.max(metrics.updatedAt ?? 0, ...points.map((point) => point.ts))

  async function loadMore() {
    if (!rows.length || loadingMore) return
    setLoadingMore(true)
    setMoreError(false)
    try {
      const next = (await api.getContainerEvents(containerId, { limit: 50, before: rows[rows.length - 1].ts })) ?? []
      setOlder((previous) => ({ id: containerId, rows: [...(previous?.id === containerId ? previous.rows : []), ...next], hasMore: next.length === 50 }))
    } catch { setMoreError(true) }
    finally { setLoadingMore(false) }
  }

  return (
    <div className="min-w-0 space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div role="group" aria-label={t('dashboard.chartRange')} className="flex gap-1">
          {(['1h', '6h', '24h'] as Range[]).map((value) => (
            <Button key={value} className="min-h-11 min-w-11" size="sm" aria-pressed={range === value}
              variant={value === range ? 'default' : 'outline'} onClick={() => setRange(value)}>{value}</Button>
          ))}
        </div>
        <ResourceStatus resource={metrics} />
      </div>
      <p className="text-xs text-muted-foreground">{t('charts.containerCpuHint')}</p>
      <div className="min-w-0 rounded-xl border border-border p-3">
        {metrics.updatedAt != null && (points.length ? (
          <TimeSeriesChart points={points} series={series} title={t('charts.containerHistory')}
            domain={[end - RANGE_MS[range], end]} gapMs={Math.max(180000, RANGE_MS[range] / 120 * 3)} />
        ) : <p className="py-6 text-center text-sm text-muted-foreground">{t('charts.noSamples')}</p>)}
      </div>
      <div>
        <h4 className="mb-2 text-sm font-semibold">{t('docker.containers.events', 'Events')}</h4>
        <ResourceStatus resource={events} />
        <div className="mt-2 divide-y rounded-lg border">
          {events.updatedAt != null && rows.length === 0 && <p className="py-6 text-center text-sm text-muted-foreground">{t('docker.containers.noEvents', 'No events')}</p>}
          {rows.map((event, index) => <div key={`${event.ts}-${index}`} className="px-3"><EventTimelineRow event={event} /></div>)}
        </div>
        {moreError && <p role="status" className="mt-2 text-sm text-destructive">{t('dashboard.loadFailed')}</p>}
        {hasMore && <div className="mt-2 text-center"><Button className="min-h-11" size="sm" variant="outline" onClick={loadMore} disabled={loadingMore}>
          {t(loadingMore ? 'common.loading' : moreError ? 'dashboard.retry' : 'docker.containers.loadMore')}
        </Button></div>}
      </div>
    </div>
  )
}
