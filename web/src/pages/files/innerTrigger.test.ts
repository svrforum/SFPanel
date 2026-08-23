import { describe, it, expect } from 'vitest'
import { exceedsSlop, LONG_PRESS_SLOP_PX } from './innerTrigger'

describe('exceedsSlop', () => {
  const origin = { x: 100, y: 100 }

  it('treats a finger that has not moved as held', () => {
    expect(exceedsSlop(origin, { x: 100, y: 100 })).toBe(false)
  })

  it('tolerates the drift a resting finger produces', () => {
    // Measured against the real panel: Radix cancels its long press on any
    // pointermove at all, and a 3px drift was enough to stop the menu ever
    // appearing.
    expect(exceedsSlop(origin, { x: 103, y: 100 })).toBe(false)
    expect(exceedsSlop(origin, { x: 100, y: 97 })).toBe(false)
  })

  it('treats a deliberate drag as a scroll', () => {
    expect(exceedsSlop(origin, { x: 100, y: 140 })).toBe(true)
    expect(exceedsSlop(origin, { x: 60, y: 100 })).toBe(true)
  })

  it('measures diagonally rather than per-axis', () => {
    // 8px on each axis is under the threshold on either one alone but 11.3px
    // away, which is a drag. Comparing axes separately would let it through.
    const d = 8
    expect(d).toBeLessThan(LONG_PRESS_SLOP_PX)
    expect(exceedsSlop(origin, { x: 100 + d, y: 100 + d })).toBe(true)
  })
})
