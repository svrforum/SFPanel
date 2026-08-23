import { describe, it, expect } from 'vitest'
import { exceedsSlop, LONG_PRESS_SLOP_PX } from './useLongPress'

describe('exceedsSlop', () => {
  const origin = { x: 100, y: 100 }

  // A finger moves a few pixels just resting on the glass. Cancelling on that
  // would make the gesture impossible to perform.
  it('tolerates the drift of a finger held still', () => {
    expect(exceedsSlop(origin, { x: 100, y: 100 })).toBe(false)
    expect(exceedsSlop(origin, { x: 104, y: 103 })).toBe(false)
    expect(exceedsSlop(origin, { x: 100, y: 109 })).toBe(false)
  })

  // The more annoying failure is the other way: a menu appearing over content
  // that is still scrolling.
  it('treats a scroll as a cancel', () => {
    expect(exceedsSlop(origin, { x: 100, y: 140 })).toBe(true)
    expect(exceedsSlop(origin, { x: 60, y: 100 })).toBe(true)
    expect(exceedsSlop(origin, { x: 130, y: 130 })).toBe(true)
  })

  it('measures distance, not per-axis movement', () => {
    // 8 across and 8 down is 11.3 away — over the threshold even though
    // neither axis is.
    expect(exceedsSlop(origin, { x: 108, y: 108 })).toBe(true)
    expect(exceedsSlop(origin, { x: 100 + LONG_PRESS_SLOP_PX, y: 100 })).toBe(false)
    expect(exceedsSlop(origin, { x: 100 + LONG_PRESS_SLOP_PX + 1, y: 100 })).toBe(true)
  })

  it('is symmetric in every direction', () => {
    for (const [dx, dy] of [[20, 0], [-20, 0], [0, 20], [0, -20], [-15, -15]]) {
      expect(exceedsSlop(origin, { x: origin.x + dx, y: origin.y + dy })).toBe(true)
    }
  })
})
