import { describe, expect, it } from 'vitest'
import { historyFrame } from './terminalHistory'

// The view's terminal is a row taller than the screen. At the bottom it shows
// the line above the screen in that extra row, shifted out of sight, so the
// rows in view are the live screen's; every pixel back slides it down by one.
describe('historyFrame', () => {
  const CELL = 16
  const MAX = 100 // the top line at the bottom

  it('opens on the live screen: the extra row hidden above it', () => {
    expect(historyFrame(0, MAX, CELL)).toEqual({ px: 0, top: MAX, shift: -CELL })
  })

  it('moves by the pixel between two lines', () => {
    expect(historyFrame(5, MAX, CELL)).toEqual({ px: 5, top: MAX, shift: -CELL + 5 })
    // A whole line back is the next line up, shifted out again: no jump at the seam.
    expect(historyFrame(CELL, MAX, CELL)).toEqual({ px: CELL, top: MAX - 1, shift: -CELL })
    expect(historyFrame(10 * CELL + 7, MAX, CELL)).toEqual({ px: 10 * CELL + 7, top: MAX - 10, shift: -CELL + 7 })
  })

  it('stops at either end', () => {
    expect(historyFrame(-30, MAX, CELL)).toEqual({ px: 0, top: MAX, shift: -CELL })
    // At the top line 1 is the first row in view: line 0, the blank the view
    // pads its copy with, stays hidden.
    expect(historyFrame(1e9, MAX, CELL)).toEqual({ px: MAX * CELL, top: 0, shift: -CELL })
  })

  // A session with no history above its screen: the view cannot move, so it
  // settles at the bottom and gives way to the live screen.
  it('has nowhere to go with no history above the screen', () => {
    expect(historyFrame(40, 0, CELL)).toEqual({ px: 0, top: 0, shift: -CELL })
  })
})
