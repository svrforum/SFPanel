import { describe, expect, it } from 'vitest'
import { KNOB_EDGE, knobLeft } from './selectionKnob'

// A phone in portrait: 390 CSS pixels across. A handle's target is 40px wide.
const WIDTH = 390

describe('knobLeft', () => {
  it('centres the knob under a selection end away from the edges', () => {
    expect(knobLeft(200, WIDTH)).toBe(180)
  })

  it('keeps the knob for an end in the last column clear of the right edge', () => {
    expect(WIDTH - (knobLeft(WIDTH, WIDTH) + 40)).toBe(KNOB_EDGE)
  })

  it('keeps the knob for an end in the first column clear of the left edge', () => {
    expect(knobLeft(0, WIDTH)).toBe(KNOB_EDGE)
  })
})
