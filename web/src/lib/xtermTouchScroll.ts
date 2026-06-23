import type { Terminal } from '@xterm/xterm'

// xterm v6's .xterm-viewport is not natively touch-scrollable, so on a touch
// device a drag can't reach the scrollback. Translate a vertical touch-drag
// into term.scrollLines so mobile can scroll up through output history.
// Returns a cleanup that removes the listeners. Pair with `touch-action: none`
// (Tailwind `touch-none`) on the container so the browser's own touch gestures
// can't preempt it.
export function attachXtermTouchScroll(container: HTMLElement, term: Terminal): () => void {
  let touchY = 0
  let touchAccum = 0
  const cellPx = () => {
    const screenEl = container.querySelector('.xterm-screen') as HTMLElement | null
    return screenEl && term.rows ? screenEl.clientHeight / term.rows : 17
  }
  const onTouchStart = (e: TouchEvent) => {
    if (e.touches.length !== 1) return
    touchY = e.touches[0].clientY
    touchAccum = 0
  }
  const onTouchMove = (e: TouchEvent) => {
    if (e.touches.length !== 1) return
    const y = e.touches[0].clientY
    touchAccum += touchY - y
    touchY = y
    const lines = Math.trunc(touchAccum / cellPx())
    if (lines !== 0) {
      term.scrollLines(lines)
      touchAccum -= lines * cellPx()
      e.preventDefault()
    }
  }
  // Capture phase so we run before any xterm internal touch handler that might
  // stopPropagation; passive:false on touchmove so preventDefault works.
  container.addEventListener('touchstart', onTouchStart, { capture: true, passive: true })
  container.addEventListener('touchmove', onTouchMove, { capture: true, passive: false })
  return () => {
    container.removeEventListener('touchstart', onTouchStart, { capture: true })
    container.removeEventListener('touchmove', onTouchMove, { capture: true })
  }
}
