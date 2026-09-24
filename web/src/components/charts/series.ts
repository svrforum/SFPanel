export interface SeriesPoint {
  ts: number
  values: Array<number | null>
}

/** Stable ordering, explicit missing values, and a break across unobserved periods. */
export function prepareSeries(points: SeriesPoint[], count: number, gapMs: number): SeriesPoint[] {
  const byTime = new Map<number, SeriesPoint>()
  for (const point of points) {
    if (!Number.isFinite(point.ts) || point.ts <= 0) continue
    byTime.set(point.ts, {
      ts: point.ts,
      values: Array.from({ length: count }, (_, index) => {
        const value = point.values[index]
        return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null
      }),
    })
  }
  const sorted = [...byTime.values()].sort((a, b) => a.ts - b.ts)
  const result: SeriesPoint[] = []
  for (const point of sorted) {
    const prev = result.at(-1)
    if (prev && point.ts - prev.ts > gapMs) {
      result.push({ ts: prev.ts + 1, values: Array(count).fill(null) })
    }
    result.push(point)
  }
  return result
}

/** Percentages start at zero; Docker CPU can legitimately exceed 100% on multiple cores. */
export function percentCeiling(points: SeriesPoint[], visible: boolean[]): number {
  let max = 100
  for (const point of points) point.values.forEach((value, index) => {
    if (visible[index] && value != null) max = Math.max(max, value)
  })
  return Math.ceil(max / 50) * 50
}

export function validDomain(domain: [number, number]): [number, number] {
  const end = Number.isFinite(domain[1]) && domain[1] > 0 ? domain[1] : 60000
  return [Number.isFinite(domain[0]) && domain[0] < end ? domain[0] : end - 60000, end]
}
