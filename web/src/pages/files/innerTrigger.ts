import { useRef } from 'react'
import type React from 'react'

/**
 * How far a finger may drift and still count as held.
 *
 * Radix has no tolerance at all: any `pointermove` cancels its long press.
 * A finger resting on glass reports small movements, and cancelling on them
 * means the menu sometimes just never appears, with nothing to show why.
 */
export const LONG_PRESS_SLOP_PX = 10

/** True once a pointer has moved far enough to be a scroll rather than a hold. */
export function exceedsSlop(from: { x: number; y: number }, to: { x: number; y: number }) {
  // Compared squared, to skip a square root in a handler that runs on every
  // pointermove.
  const dx = to.x - from.x
  const dy = to.y - from.y
  return dx * dx + dy * dy > LONG_PRESS_SLOP_PX * LONG_PRESS_SLOP_PX
}

/**
 * Props for a context-menu trigger nested inside another one.
 *
 * The listing has two layers of trigger: a row, card or tile, and the
 * background trigger covering the whole listing area so "new file" is
 * reachable from empty space. A touch long press opened *both* — eight row
 * items and four background items on screen at once — because Radix arms its
 * long-press timer on `pointerdown`, and that event bubbles to every trigger
 * above it, each then opening on its own schedule.
 *
 * A right-click escapes this by accident: the inner trigger calls
 * preventDefault, and Radix skips a composed handler whose event is already
 * defaultPrevented, so the outer trigger never opens. Nothing equivalent
 * happens on the pointer path.
 *
 * Two things follow. `stopPropagation` on pointerdown cuts the ancestors off
 * without setting defaultPrevented, so this element's own trigger still arms
 * and the innermost menu — the one under the finger — is the one that opens.
 * And `preventDefault` on a small pointermove uses that same skip deliberately,
 * to stop Radix cancelling the press over a millimetre of drift.
 */
export function useInnerTrigger() {
  const origin = useRef<{ x: number; y: number } | null>(null)

  return {
    onPointerDown: (e: React.PointerEvent) => {
      e.stopPropagation()
      origin.current = { x: e.clientX, y: e.clientY }
    },
    onPointerMove: (e: React.PointerEvent) => {
      if (!origin.current) return
      if (!exceedsSlop(origin.current, { x: e.clientX, y: e.clientY })) e.preventDefault()
    },
    onPointerUp: () => { origin.current = null },
    onPointerCancel: () => { origin.current = null },
  }
}
