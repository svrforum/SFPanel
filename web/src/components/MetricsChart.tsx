import { useEffect, useRef, useMemo } from 'react'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'

interface MetricsChartProps {
  data: Array<{ ts: number; cpu: number; memory: number }>
  title: string
  headerAction?: React.ReactNode
  xDomain?: [number, number]
}

function isDark(): boolean {
  return document.documentElement.classList.contains('dark')
}

function getColors() {
  const dark = isDark()
  return {
    axes: dark ? '#8b949e' : '#8b95a1',
    grid: dark ? 'rgba(139,148,158,0.15)' : 'rgba(0,0,0,0.06)',
    bg: 'transparent',
    tooltipBg: dark ? '#161b22' : '#ffffff',
    tooltipBorder: dark ? '#30363d' : '#e5e8eb',
    tooltipText: dark ? '#e6edf3' : '#191f28',
  }
}

function fmtTime(ts: number): string {
  const d = new Date(ts * 1000)
  return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit' })
}

function fmtTimeFull(ts: number): string {
  const d = new Date(ts * 1000)
  return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function buildTooltipContent(
  container: HTMLDivElement,
  ts: number,
  cpu: number,
  mem: number,
): void {
  const c = getColors()
  // Clear previous content
  while (container.firstChild) container.removeChild(container.firstChild)

  // Timestamp line
  const tsDiv = document.createElement('div')
  tsDiv.style.cssText = `font-size:11px;color:${c.tooltipText};opacity:0.7;margin-bottom:4px`
  tsDiv.textContent = fmtTimeFull(ts)
  container.appendChild(tsDiv)

  // CPU line
  const cpuDiv = document.createElement('div')
  cpuDiv.style.cssText = `display:flex;align-items:center;gap:6px;font-size:12px;color:${c.tooltipText}`
  const cpuDot = document.createElement('span')
  cpuDot.style.cssText = 'display:inline-block;width:8px;height:8px;border-radius:50%;background:#3182f6'
  cpuDiv.appendChild(cpuDot)
  cpuDiv.appendChild(document.createTextNode(`CPU: ${cpu.toFixed(1)}%`))
  container.appendChild(cpuDiv)

  // Memory line
  const memDiv = document.createElement('div')
  memDiv.style.cssText = `display:flex;align-items:center;gap:6px;font-size:12px;color:${c.tooltipText};margin-top:2px`
  const memDot = document.createElement('span')
  memDot.style.cssText = 'display:inline-block;width:8px;height:8px;border-radius:50%;background:#00c471'
  memDiv.appendChild(memDot)
  memDiv.appendChild(document.createTextNode(`Memory: ${mem.toFixed(1)}%`))
  container.appendChild(memDiv)
}

export default function MetricsChart({ data, title, headerAction, xDomain }: MetricsChartProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<uPlot | null>(null)
  const tooltipRef = useRef<HTMLDivElement>(null)

  // Convert data to uPlot format: [timestamps[], cpu[], memory[]]
  const uData = useMemo((): uPlot.AlignedData => {
    if (data.length === 0) return [new Float64Array(0), new Float64Array(0), new Float64Array(0)]
    const timestamps = new Float64Array(data.length)
    const cpuArr = new Float64Array(data.length)
    const memArr = new Float64Array(data.length)
    for (let i = 0; i < data.length; i++) {
      timestamps[i] = data[i].ts / 1000 // uPlot uses seconds
      cpuArr[i] = data[i].cpu
      memArr[i] = data[i].memory
    }
    return [timestamps, cpuArr, memArr]
  }, [data])

  // Build chart options
  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const colors = getColors()
    const width = el.clientWidth
    const height = 300

    const opts: uPlot.Options = {
      width,
      height,
      padding: [16, 12, 0, 0],
      cursor: {
        show: true,
        x: true,
        y: false,
        drag: { x: false, y: false },
      },
      legend: { show: false },
      scales: {
        x: {
          time: true,
          ...(xDomain ? { min: xDomain[0] / 1000, max: xDomain[1] / 1000 } : {}),
        },
        y: {
          min: 0,
          max: 100,
        },
      },
      axes: [
        {
          stroke: colors.axes,
          grid: { stroke: colors.grid, width: 1 },
          ticks: { show: false },
          font: '11px "Pretendard Variable", sans-serif',
          values: (_u: uPlot, splits: number[]) => splits.map(fmtTime),
        },
        {
          stroke: colors.axes,
          grid: { stroke: colors.grid, width: 1 },
          ticks: { show: false },
          font: '11px "Pretendard Variable", sans-serif',
          values: (_u: uPlot, splits: number[]) => splits.map(v => `${v}%`),
          size: 42,
        },
      ],
      series: [
        {},
        {
          label: 'CPU',
          stroke: '#3182f6',
          width: 2,
          fill: 'rgba(49,130,246,0.06)',
          points: { show: false },
        },
        {
          label: 'Memory',
          stroke: '#00c471',
          width: 2,
          fill: 'rgba(0,196,113,0.06)',
          points: { show: false },
        },
      ],
      hooks: {
        setCursor: [
          (u: uPlot) => {
            const tooltip = tooltipRef.current
            if (!tooltip) return

            const idx = u.cursor.idx
            if (idx == null || idx < 0 || idx >= (u.data[0]?.length ?? 0)) {
              tooltip.style.display = 'none'
              return
            }

            const ts = u.data[0][idx] as number
            const cpu = (u.data[1][idx] ?? 0) as number
            const mem = (u.data[2][idx] ?? 0) as number
            const c = getColors()

            buildTooltipContent(tooltip, ts, cpu, mem)

            tooltip.style.display = 'block'
            tooltip.style.background = c.tooltipBg
            tooltip.style.border = `1px solid ${c.tooltipBorder}`

            // Position tooltip
            const left = u.cursor.left ?? 0
            const rect = el.getBoundingClientRect()
            const tooltipW = tooltip.offsetWidth
            const xPos = left + 60 // offset for y-axis
            if (xPos + tooltipW + 8 > rect.width) {
              tooltip.style.left = `${xPos - tooltipW - 16}px`
            } else {
              tooltip.style.left = `${xPos + 8}px`
            }
            tooltip.style.top = '40px'
          },
        ],
      },
    }

    // Destroy previous chart
    if (chartRef.current) {
      chartRef.current.destroy()
    }

    const chart = new uPlot(opts, uData, el)
    chartRef.current = chart

    return () => {
      chart.destroy()
      chartRef.current = null
    }
    // Re-create chart when xDomain changes (theme could also change)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [xDomain])

  // Update data without recreating chart
  useEffect(() => {
    const chart = chartRef.current
    if (!chart) return
    chart.setData(uData)
    if (xDomain) {
      chart.setScale('x', { min: xDomain[0] / 1000, max: xDomain[1] / 1000 })
    }
  }, [uData, xDomain])

  // Handle resize
  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const chart = chartRef.current
        if (chart) {
          chart.setSize({ width: entry.contentRect.width, height: 300 })
        }
      }
    })

    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  return (
    <div className="bg-card rounded-2xl p-6 card-shadow">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-4">
          <h3 className="text-[13px] font-semibold text-foreground">{title}</h3>
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-1.5">
              <span className="inline-block w-2 h-2 rounded-full" style={{ background: '#3182f6' }} />
              <span className="text-[11px] text-muted-foreground">CPU</span>
            </div>
            <div className="flex items-center gap-1.5">
              <span className="inline-block w-2 h-2 rounded-full" style={{ background: '#00c471' }} />
              <span className="text-[11px] text-muted-foreground">Memory</span>
            </div>
          </div>
        </div>
        {headerAction}
      </div>
      <div className="h-[300px] w-full relative" ref={containerRef}>
        <div
          ref={tooltipRef}
          style={{
            display: 'none',
            position: 'absolute',
            zIndex: 10,
            padding: '8px 12px',
            borderRadius: '12px',
            fontSize: '12px',
            boxShadow: '0 4px 12px rgba(0,0,0,0.08)',
            pointerEvents: 'none',
          }}
        />
      </div>
    </div>
  )
}
