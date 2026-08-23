import { describe, it, expect } from 'vitest'
import { appendChartPoint, type ChartPoint } from './chartSeries'

const pt = (ts: number): ChartPoint => ({ ts, cpu: 1, memory: 2, disk: 3 })

describe('appendChartPoint', () => {
  it('takes the first sample', () => {
    expect(appendChartPoint([], pt(1000))).toHaveLength(1)
  })

  it('drops a sample that arrives too soon after the last', () => {
    const prev = [pt(0)]
    expect(appendChartPoint(prev, pt(2_000))).toBe(prev)
    expect(appendChartPoint(prev, pt(29_999))).toBe(prev)
  })

  it('takes a sample once the interval has passed', () => {
    expect(appendChartPoint([pt(0)], pt(30_000))).toHaveLength(2)
  })

  it('trims to the cap from the front', () => {
    const prev = [pt(0), pt(30_000), pt(60_000)]
    const next = appendChartPoint(prev, pt(90_000), 30_000, 3)
    expect(next.map((p) => p.ts)).toEqual([30_000, 60_000, 90_000])
  })

  /**
   * The reason this file exists.
   *
   * The socket sends a sample every two seconds. Appending all of them made
   * the 2880-point cap worth 96 minutes, so a dashboard open that long had
   * evicted the 24 hours of history it loaded at mount and the 24h range drew
   * an hour and a half. Thinning to one point per 30 seconds makes the same
   * cap worth exactly 24 hours.
   *
   * Feeding four hours of two-second samples on top of a full day of history
   * must leave the oldest history point in place. Remove the interval check
   * and this fails: 7200 samples overrun the cap on their own.
   */
  it('keeps a day of history under four hours of two-second samples', () => {
    const DAY_POINTS = 1440 // 24h of the server's 60s history
    let series: ChartPoint[] = Array.from({ length: DAY_POINTS }, (_, i) => pt(i * 60_000))
    const oldest = series[0].ts
    const start = series[series.length - 1].ts

    for (let ms = 2_000; ms <= 4 * 60 * 60 * 1000; ms += 2_000) {
      series = appendChartPoint(series, pt(start + ms))
    }

    expect(series[0].ts).toBe(oldest)
    expect(series.length).toBeLessThanOrEqual(2880)
    // 24h of history + 4h of samples, all at 30s or coarser.
    expect(series[series.length - 1].ts - series[0].ts).toBeGreaterThanOrEqual(24 * 60 * 60 * 1000)
  })
})
