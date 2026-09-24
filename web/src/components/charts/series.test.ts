import { describe, expect, it } from 'vitest'
import { percentCeiling, prepareSeries, validDomain } from './series'

describe('time series presentation', () => {
  it('orders and deduplicates samples without turning missing values into zero', () => {
    const points = prepareSeries([
      { ts: 2000, values: [12, null] }, { ts: 1000, values: [NaN, 30] },
      { ts: 2000, values: [25, -1] }, { ts: NaN, values: [100, 100] },
    ], 2, 60000)
    expect(points).toEqual([{ ts: 1000, values: [null, 30] }, { ts: 2000, values: [25, null] }])
  })
  it('breaks the line over missing collection periods', () => {
    const points = prepareSeries([{ ts: 1000, values: [12] }, { ts: 601000, values: [18] }], 1, 180000)
    expect(points).toHaveLength(3)
    expect(points[1].values).toEqual([null])
  })
  it('keeps an honest zero-to-100 baseline and does not clip multi-core CPU usage', () => {
    const points = [{ ts: 1000, values: [243, 35] }]
    expect(percentCeiling(points, [true, true])).toBe(250)
    expect(percentCeiling(points, [false, true])).toBe(100)
    expect(percentCeiling([{ ts: 1000, values: [0.1] }], [true])).toBe(100)
  })
  it('provides a non-degenerate axis for a single point or no data', () => {
    expect(validDomain([1000, 1000])).toEqual([-59000, 1000])
    expect(validDomain([NaN, NaN])).toEqual([0, 60000])
  })
})
