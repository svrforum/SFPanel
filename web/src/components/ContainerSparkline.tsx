import { useEffect, useRef, useState } from 'react'
import uPlot from 'uplot'
import { api } from '@/lib/api'
import type { ContainerMetricPoint } from '@/types/api'

interface Props {
  containerId: string
  metric: 'cpu' | 'mem'
  width?: number
  height?: number
}

// ContainerSparkline renders a 60-point uplot mini chart for either CPU or
// memory percentage over the last hour. Fetches once on mount and re-renders
// only when containerId changes — the parent table polls separately for the
// current value column. Sparkline is "trend background" not real-time data.
export function ContainerSparkline({ containerId, metric, width = 80, height = 24 }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  const [data, setData] = useState<ContainerMetricPoint[] | null>(null)

  useEffect(() => {
    let cancelled = false
    api.getContainerMetrics(containerId, '1h')
      .then((points) => { if (!cancelled) setData(points) })
      .catch(() => { if (!cancelled) setData([]) })
    return () => { cancelled = true }
  }, [containerId])

  useEffect(() => {
    if (!ref.current || !data || data.length === 0) return
    const xs = data.map(p => p.ts / 1000)
    const ys = data.map(p => metric === 'cpu' ? p.cpu_percent : p.mem_percent)
    const opts: uPlot.Options = {
      width, height,
      cursor: { show: false },
      legend: { show: false },
      axes: [{ show: false }, { show: false }],
      scales: { x: { time: true }, y: { auto: true } },
      series: [
        {},
        { stroke: metric === 'cpu' ? '#3b82f6' : '#a855f7', width: 1, points: { show: false } },
      ],
    }
    while (ref.current.firstChild) ref.current.removeChild(ref.current.firstChild)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    new uPlot(opts, [xs, ys] as any, ref.current)
  }, [data, metric, width, height])

  if (!data || data.length === 0) {
    return <span className="inline-block flex-shrink-0 text-muted-foreground text-[10px] align-middle" style={{ width, height, lineHeight: `${height}px`, textAlign: 'center' }}>—</span>
  }

  return <div ref={ref} className="inline-block flex-shrink-0 align-middle overflow-hidden" style={{ width, height }} />
}
