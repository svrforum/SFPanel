import { useEffect, useRef, useState } from 'react'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import type { ContainerMetricPoint, ContainerEvent } from '@/types/api'
import { EventTimelineRow } from '@/components/EventTimelineRow'

type Range = '1h' | '6h' | '24h'

interface Props {
  containerId: string
}

export function ContainerHistoryTab({ containerId }: Props) {
  const [range, setRange] = useState<Range>('1h')
  const [metrics, setMetrics] = useState<ContainerMetricPoint[]>([])
  const [events, setEvents] = useState<ContainerEvent[]>([])
  const [hasMore, setHasMore] = useState(true)
  const chartRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    api.getContainerMetrics(containerId, range)
      .then(setMetrics)
      .catch(() => setMetrics([]))
  }, [containerId, range])

  useEffect(() => {
    api.getContainerEvents(containerId, { limit: 50 })
      .then((evs) => {
        setEvents(evs)
        setHasMore(evs.length === 50)
      })
      .catch(() => setEvents([]))
  }, [containerId])

  useEffect(() => {
    if (!chartRef.current) return
    while (chartRef.current.firstChild) chartRef.current.removeChild(chartRef.current.firstChild)
    if (metrics.length === 0) return
    const xs = metrics.map(p => p.ts / 1000)
    const cpu = metrics.map(p => p.cpu_percent)
    const mem = metrics.map(p => p.mem_percent)
    const opts: uPlot.Options = {
      width: chartRef.current.clientWidth,
      height: 220,
      legend: { show: true },
      scales: { x: { time: true }, y: { auto: true, range: [0, 100] } },
      axes: [{}, { values: (_, ticks) => ticks.map(t => `${t}%`) }],
      series: [
        {},
        { label: 'CPU%', stroke: '#3b82f6', width: 1.5 },
        { label: 'MEM%', stroke: '#a855f7', width: 1.5 },
      ],
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    new uPlot(opts, [xs, cpu, mem] as any, chartRef.current)
  }, [metrics])

  async function loadMore() {
    if (events.length === 0) return
    const before = events[events.length - 1].ts
    const next = await api.getContainerEvents(containerId, { limit: 50, before })
    setEvents([...events, ...next])
    setHasMore(next.length === 50)
  }

  return (
    <div className="space-y-4">
      <div className="flex gap-1">
        {(['1h', '6h', '24h'] as Range[]).map(r => (
          <Button key={r} size="sm" variant={r === range ? 'default' : 'outline'} onClick={() => setRange(r)}>{r}</Button>
        ))}
      </div>
      <div className="border rounded-md p-2" style={{ minHeight: 220 }}>
        {metrics.length === 0 ? (
          <div className="text-muted-foreground text-[12px] py-8 text-center">수집 중…</div>
        ) : (
          <div ref={chartRef} />
        )}
      </div>
      <div>
        <h4 className="text-[13px] font-semibold mb-2">이벤트</h4>
        <div className="border rounded-md divide-y">
          {events.length === 0 && (
            <div className="text-muted-foreground text-[12px] py-8 text-center">이벤트 없음</div>
          )}
          {events.map((ev, i) => <div key={`${ev.ts}-${i}`} className="px-3"><EventTimelineRow event={ev} /></div>)}
        </div>
        {hasMore && (
          <div className="text-center mt-2">
            <Button size="sm" variant="outline" onClick={loadMore}>더 보기</Button>
          </div>
        )}
      </div>
    </div>
  )
}
