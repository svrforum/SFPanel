import { useCallback, useRef } from 'react'

/** How long a press must be held before it counts. */
export const LONG_PRESS_MS = 500

/**
 * How far a finger may drift and still count as a press.
 *
 * A finger is not a mouse: it moves a few pixels just resting on the glass. Too
 * small a tolerance cancels every long press; too large and the gesture fires
 * in the middle of a scroll, which is the more annoying failure — the menu
 * appears over content that is still moving.
 */
export const LONG_PRESS_SLOP_PX = 10

export interface LongPressHandlers {
  onPointerDown: (e: React.PointerEvent) => void
  onPointerMove: (e: React.PointerEvent) => void
  onPointerUp: () => void
  onPointerCancel: () => void
  onPointerLeave: () => void
}

/**
 * Open a Radix context menu from a long press.
 *
 * Radix drives its context menu from the native `contextmenu` event and offers
 * no controlled `open` prop, so the way to trigger one by hand is to dispatch
 * that event at the coordinates the finger was held. Passing the coordinates
 * matters: the menu positions itself from them, and a menu that always appeared
 * at the top-left of the element would land under the thumb on a phone.
 */
export function dispatchContextMenu(element: HTMLElement | null, x: number, y: number) {
  if (!element) return
  element.dispatchEvent(
    new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: x, clientY: y }),
  )
}

/**
 * Fire a callback when a touch is held in place.
 *
 * Touch devices have no right-click, so every context menu on this page was
 * reachable only with a mouse. On a phone the same actions meant finding a
 * small overflow button by eye.
 *
 * Mouse presses are ignored: a mouse already has a context menu, and arming
 * this for it would mean a slow click on a row opens a menu the user did not
 * ask for.
 */
export function useLongPress(onLongPress: (x: number, y: number) => void): LongPressHandlers {
  const timer = useRef<number | null>(null)
  const origin = useRef<{ x: number; y: number } | null>(null)

  const cancel = useCallback(() => {
    if (timer.current !== null) {
      window.clearTimeout(timer.current)
      timer.current = null
    }
    origin.current = null
  }, [])

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    if (e.pointerType === 'mouse') return
    origin.current = { x: e.clientX, y: e.clientY }
    const { clientX, clientY } = e
    timer.current = window.setTimeout(() => {
      timer.current = null
      // A short buzz, where the platform offers one, so the gesture is
      // acknowledged before the menu paints.
      navigator.vibrate?.(10)
      onLongPress(clientX, clientY)
    }, LONG_PRESS_MS)
  }, [onLongPress])

  const onPointerMove = useCallback((e: React.PointerEvent) => {
    if (timer.current === null || !origin.current) return
    if (exceedsSlop(origin.current, { x: e.clientX, y: e.clientY })) cancel()
  }, [cancel])

  return {
    onPointerDown,
    onPointerMove,
    onPointerUp: cancel,
    onPointerCancel: cancel,
    onPointerLeave: cancel,
  }
}

/** True when a pointer has moved far enough to be a scroll rather than a press. */
export function exceedsSlop(from: { x: number; y: number }, to: { x: number; y: number }) {
  // Compared squared, to skip a square root on a handler that runs on every
  // pointermove.
  const dx = to.x - from.x
  const dy = to.y - from.y
  return dx * dx + dy * dy > LONG_PRESS_SLOP_PX * LONG_PRESS_SLOP_PX
}
