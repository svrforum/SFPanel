import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { cn } from '@/lib/utils'
import { percentCeiling, prepareSeries, validDomain, type SeriesPoint } from './series'

export interface ChartSeries {
  key: string
  label: string
  color: string
}
interface Props {
  points: SeriesPoint[]
  series: ChartSeries[]
  title: string
  domain: [number, number]
  gapMs: number
}

/** Shared, responsive percent chart. Readouts stay outside the plot so they never hide a spike. */
export default function TimeSeriesChart({ points, series, title, domain, gapMs }: Props) {
  const { t, i18n } = useTranslation()
  const hintId = useId()
  const plotElement = useRef<HTMLDivElement>(null)
  const plot = useRef<uPlot | null>(null)
  const [selectedTime, setSelectedTime] = useState<number | null>(null)
  const [hidden, setHidden] = useState<string[]>([])
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  const normalized = useMemo(() => prepareSeries(points, series.length, gapMs), [points, series.length, gapMs])
  const observed = useMemo(() => normalized.filter((point) => point.values.some((value) => value != null)), [normalized])
  const selected = observed.find((point) => point.ts === selectedTime) ?? observed.at(-1)
  const shown = useMemo(() => series.map((item) => !hidden.includes(item.key)), [series, hidden])
  const ceiling = useMemo(() => percentCeiling(normalized, shown), [normalized, shown])
  const [start, end] = validDomain(domain)
  const data = useMemo<uPlot.AlignedData>(() => [
    normalized.map((point) => point.ts / 1000),
    ...series.map((_, index) => normalized.map((point) => point.values[index])),
  ], [normalized, series])
  const locale = i18n.language
  const formatTime = (ts: number) => new Date(ts).toLocaleString(locale, {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  })

  useEffect(() => {
    const observer = new MutationObserver(() => setDark(document.documentElement.classList.contains('dark')))
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    const element = plotElement.current
    if (!element) return
    const textColor = dark ? '#acb6c3' : '#596779'
    const gridColor = dark ? 'rgba(172,182,195,0.14)' : 'rgba(89,103,121,0.12)'
    let touchSelected = false
    const chart = new uPlot({
      width: Math.max(1, element.clientWidth), height: element.clientHeight || 220,
      padding: [12, 14, 0, 0], legend: { show: false },
      cursor: {
        x: true, y: false, drag: { x: false, y: false },
        bind: { mousedown: () => null, mouseup: () => null, dblclick: () => null },
        points: { size: 7 },
      },
      scales: {
        x: { time: true },
        y: { range: (_chart, _min, max) => [0, Math.ceil(Math.max(100, max ?? 100) / 50) * 50] },
      },
      axes: [
        {
          stroke: textColor, grid: { show: false }, ticks: { show: false },
          font: '12px sans-serif', space: 80, size: 48,
          values: (chart, ticks) => ticks.map((value) => {
            const date = new Date(value * 1000)
            const time = date.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', hour12: false })
            return (chart.scales.x.max ?? 0) - (chart.scales.x.min ?? 0) >= 12 * 3600
              ? `${date.toLocaleDateString(locale, { month: '2-digit', day: '2-digit' })}\n${time}` : time
          }),
        },
        {
          stroke: textColor, grid: { stroke: gridColor, width: 1 }, ticks: { show: false },
          font: '12px sans-serif', space: 42, size: 52,
          values: (_, ticks) => ticks.map((value) => `${value}%`),
        },
      ],
      series: [
        {},
        ...series.map((item, index) => ({
          label: item.label, stroke: item.color, width: 2,
          // Different dash patterns keep overlapping lines distinguishable without color alone.
          dash: index === 1 ? [6, 3] : index === 2 ? [2, 3] : undefined,
          spanGaps: false,
          points: { show: (chart: uPlot) => chart.data[0].length <= 2, size: 6 },
        })),
      ],
      hooks: {
        setCursor: [(chart) => {
          const index = chart.cursor.idx
          const hasValue = index != null && chart.data.slice(1).some((values) => values[index] != null)
          // Touch browsers synthesize mouseleave after a tap. Keep the tapped
          // sample readable until another touch or a real mouse movement.
          if (touchSelected && (index == null || !hasValue)) return
          setSelectedTime(index == null || !hasValue ? null : Math.round((chart.data[0][index] ?? 0) * 1000))
        }],
      },
    }, [[], ...series.map(() => [])] as uPlot.AlignedData, element)
    plot.current = chart
    const resize = new ResizeObserver(([entry]) => {
      if (entry.contentRect.width > 0 && entry.contentRect.height > 0) {
        chart.setSize({ width: entry.contentRect.width, height: entry.contentRect.height })
      }
    })
    resize.observe(element)
    // Native vertical scrolling is preserved; a tap/horizontal move inspects a sample.
    const touch = (event: PointerEvent) => {
      if (event.pointerType === 'mouse') { touchSelected = false; return }
      touchSelected = true
      const rect = chart.over.getBoundingClientRect()
      chart.setCursor({ left: Math.max(0, Math.min(rect.width, event.clientX - rect.left)), top: rect.height / 2 })
    }
    chart.over.style.touchAction = 'pan-y'
    chart.over.addEventListener('pointerdown', touch, { passive: true })
    chart.over.addEventListener('pointermove', touch, { passive: true })
    return () => {
      resize.disconnect()
      chart.over.removeEventListener('pointerdown', touch)
      chart.over.removeEventListener('pointermove', touch)
      chart.destroy()
      plot.current = null
    }
  }, [series, locale, dark])

  useEffect(() => {
    const chart = plot.current
    if (!chart) return
    chart.setData(data, false)
    shown.forEach((show, index) => chart.setSeries(index + 1, { show }))
    chart.setScale('x', { min: start / 1000, max: end / 1000 })
    chart.setScale('y', { min: 0, max: ceiling })
  }, [data, start, end, ceiling, shown, series, locale, dark])

  const moveSelection = (event: React.KeyboardEvent) => {
    if (!observed.length) return
    let index = observed.findIndex((point) => point.ts === selected?.ts)
    if (event.key === 'ArrowLeft') index = Math.max(0, index - 1)
    else if (event.key === 'ArrowRight') index = Math.min(observed.length - 1, index + 1)
    else if (event.key === 'Home') index = 0
    else if (event.key === 'End') index = observed.length - 1
    else return
    event.preventDefault()
    const point = observed[index]
    setSelectedTime(point.ts)
    const chart = plot.current
    if (chart) chart.setCursor({ left: chart.valToPos(point.ts / 1000, 'x'), top: chart.over.clientHeight / 2 })
  }

  return (
    <div className="min-w-0 space-y-2" data-chart>
      <div className="flex flex-wrap items-center justify-between gap-1 text-xs text-muted-foreground">
        <span>{t(selectedTime != null && observed.some((point) => point.ts === selectedTime) ? 'charts.selectedSample' : 'charts.latestSample')}</span>
        <output data-chart-readout>{selected ? formatTime(selected.ts) : '—'}</output>
      </div>
      <div role="group" aria-label={t('charts.series')} className="flex flex-wrap gap-2">
        {series.map((item, index) => (
          <button key={item.key} type="button" aria-pressed={shown[index]}
            disabled={shown[index] && shown.filter(Boolean).length === 1}
            onClick={() => setHidden((previous) => shown[index] ? [...previous, item.key] : previous.filter((key) => key !== item.key))}
            className={cn('flex min-h-11 items-center gap-2 rounded-lg border px-3 text-xs focus-visible:outline-2 focus-visible:outline-ring', shown[index] ? 'border-border bg-secondary/30' : 'border-transparent text-muted-foreground')}>
            <span aria-hidden="true" className="w-4 border-t-2" style={{ borderColor: item.color, borderStyle: index === 1 ? 'dashed' : index === 2 ? 'dotted' : 'solid', opacity: shown[index] ? 1 : 0.4 }} />
            {item.label}<span className="font-mono font-semibold">{selected?.values[index] != null ? `${selected.values[index].toFixed(1)}%` : '—'}</span>
          </button>
        ))}
      </div>
      <div role="img" aria-label={`${title} · 0–${ceiling}%`} aria-describedby={hintId} tabIndex={0} onKeyDown={moveSelection}
        className="relative h-[220px] w-full rounded-lg focus-visible:outline-2 focus-visible:outline-ring md:h-[260px]" ref={plotElement} />
      <p id={hintId} className="text-xs text-muted-foreground">{t('charts.interactionHint')}</p>
      <details className="text-xs text-muted-foreground">
        <summary className="min-h-11 cursor-pointer content-center rounded-md focus-visible:outline-2 focus-visible:outline-ring">{t('charts.dataTable')}</summary>
        <div className="max-h-56 overflow-auto">
          <table className="w-full text-left tabular-nums">
            <caption className="sr-only">{title}</caption>
            <thead><tr><th scope="col" className="p-2">{t('charts.time')}</th>{series.map((item) => <th scope="col" className="p-2" key={item.key}>{item.label}</th>)}</tr></thead>
            <tbody>{observed.slice().reverse().map((point) => <tr key={point.ts} className="border-t border-border">
              <th scope="row" className="whitespace-nowrap p-2 font-normal">{formatTime(point.ts)}</th>
              {point.values.map((value, index) => <td key={index} className="p-2">{value == null ? '—' : `${value.toFixed(1)}%`}</td>)}
            </tr>)}</tbody>
          </table>
        </div>
      </details>
    </div>
  )
}
