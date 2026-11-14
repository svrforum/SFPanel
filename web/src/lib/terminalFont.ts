// The terminal's font size: its limits, the two events that carry it between
// a session and the page, and the pinch that changes it.

// 8px is what fits 80 columns across a phone held upright.
export const MIN_FONT_SIZE = 8
export const MAX_FONT_SIZE = 24
export const DEFAULT_FONT_SIZE = 14

/** A session's pinch ended on a new size (detail: the size); the page owns the
 *  setting, which applies to every session. */
export const FONT_SIZE_EVENT = 'sfpanel:terminal-font-size'
/** The active session's grid changed (detail: { cols, rows }). */
export const GRID_EVENT = 'sfpanel:terminal-grid'

export function clampFontSize(size: number): number {
  return Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, Math.round(size)))
}

/** The size two fingers have reached: the one they started on, scaled by how
 *  far apart they are now (d) against then (d0). */
export function pinchSize(start: number, d0: number, d: number): number {
  return d0 > 0 ? clampFontSize(start * d / d0) : start
}

export interface PinchOptions {
  /** the size a gesture starts from */
  size: () => number
  /** the size the fingers are at, while they move; nothing is applied yet */
  onPreview: (size: number) => void
  /** the gesture is over: the size to apply, or null when there is none — it
   *  ended where it began, or was cancelled */
  onEnd: (size: number | null) => void
  /** where the rest of a gesture is heard (the window; in tests a stand-in) */
  root?: EventTarget
}

/**
 * Two fingers on the terminal change its font size, as they zoom anything else
 * on a phone. The size is only previewed while they move and applied once when
 * they lift: every size is a different grid, and each grid is a resize the
 * program in the session redraws for.
 *
 * Only the touchstart is heard on el; the rest is heard on the window in the
 * capture phase and stopped there, ahead of the Android app's document-level
 * listeners, as the touch scrolling does (lib/xtermTouchScroll). That one and
 * the long press both let go of a gesture the moment a second finger lands.
 */
export function attachPinch(el: HTMLElement, { size, onPreview, onEnd, root = window }: PinchOptions): () => void {
  let active = false
  let from = 0 // the size the gesture began on
  let d0 = 0 // ...and how far apart the fingers were
  let at = 0 // the size they are at

  const distance = (e: TouchEvent) => Math.hypot(e.touches[0].clientX - e.touches[1].clientX, e.touches[0].clientY - e.touches[1].clientY)
  const listen = (on: boolean) => {
    const method = on ? 'addEventListener' : 'removeEventListener'
    root[method]('touchmove', onTouchMove as EventListener, { capture: true, passive: false })
    root[method]('touchend', onTouchEnd, { capture: true })
    root[method]('touchcancel', onTouchCancel, { capture: true })
  }
  const finish = (apply: boolean) => {
    if (!active) return
    active = false
    listen(false)
    onEnd(apply && at !== from ? at : null)
  }
  const onTouchStart = (e: TouchEvent) => {
    if (active) {
      // A third finger: the gesture ends on what the two had reached.
      finish(true)
      return
    }
    if (e.touches.length !== 2) return
    active = true
    from = at = size()
    d0 = distance(e)
    listen(true)
  }
  const onTouchMove = (e: TouchEvent) => {
    if (e.touches.length !== 2) {
      finish(true)
      return
    }
    if (e.cancelable) e.preventDefault()
    e.stopPropagation()
    const next = pinchSize(from, d0, distance(e))
    if (next === at) return
    at = next
    onPreview(at)
  }
  // No click follows a gesture of two fingers, so the lift needs no stopping:
  // the terminal does not take focus and raise the keyboard.
  const onTouchEnd = () => { finish(true) }
  const onTouchCancel = () => { finish(false) }

  el.addEventListener('touchstart', onTouchStart, { capture: true, passive: true })
  return () => {
    if (active) {
      active = false
      listen(false)
    }
    el.removeEventListener('touchstart', onTouchStart, { capture: true })
  }
}
