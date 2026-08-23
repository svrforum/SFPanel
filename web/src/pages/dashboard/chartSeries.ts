export interface ChartPoint {
  ts: number
  cpu: number
  memory: number
  disk: number
}

// 24h at 30s intervals = 2880 points; cap to keep the chart readable.
export const MAX_CHART_POINTS = 2880

/**
 * How far apart chart points are kept.
 *
 * The metrics socket sends a sample every two seconds and every one of them
 * used to be appended, so the 2880-point cap was worth 96 minutes. A dashboard
 * left open that long had quietly evicted the 24 hours of history it loaded at
 * mount, and the 24h button then drew an hour and a half. Thirty seconds makes
 * the cap mean exactly the longest range the chart offers, and matches the
 * 60-second granularity the server's own history already has. Nothing is lost
 * visually: at the shortest range an hour spans a few hundred pixels, so a
 * two-second sample was a third of one.
 */
export const CHART_SAMPLE_MS = 30_000

/**
 * Add a sample to the chart series, thinning and trimming as it goes.
 *
 * Its own module rather than a closure inside the page: this is the kind of
 * arithmetic that is wrong for hours before anyone notices, because the
 * symptom only appears on a tab left open long enough for the cap to bite.
 */
export function appendChartPoint(
  prev: ChartPoint[],
  point: ChartPoint,
  sampleMs = CHART_SAMPLE_MS,
  maxPoints = MAX_CHART_POINTS,
): ChartPoint[] {
  const last = prev[prev.length - 1]
  if (last && point.ts - last.ts < sampleMs) return prev
  const next = [...prev, point]
  if (next.length > maxPoints) return next.slice(next.length - maxPoints)
  return next
}
