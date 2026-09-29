// attachLongPress calls onLongPress when one finger rests on el for `delay` ms
// without moving more than `slop` px. A moving finger (a scroll), a second
// finger, or lifting early cancels it. Returns a cleanup.
//
// Only the touchstart is heard on el. The rest of the gesture is heard on
// `root` (the window) in the capture phase, ahead of every other listener: the
// Android app's injected script owns terminal drags from a document-level
// capture listener and stops them there, so a listener on el never saw the
// finger move and a scroll longer than the delay fired the long press.
//
// The gesture that fired is consumed. The rest of its drag is stopped, so the
// terminal behind the dialog does not scroll; its touchend is prevented, so the
// browser sends no click and the terminal does not take focus and raise the
// keyboard. The contextmenu the platform raises for the same long press is
// swallowed too, and fires the long press early if it comes first — Android
// raises it on its own timer, which may beat ours. A mouse's contextmenu (a
// desktop right click) is left alone.
export function attachLongPress(
  el: HTMLElement,
  onLongPress: () => void,
  { delay = 500, slop = 10, root = window as EventTarget }: { delay?: number; slop?: number; root?: EventTarget } = {},
): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined
  let tracking = false // a one-finger gesture that began on el is in progress
  let fired = false // ...and it has fired
  let startX = 0
  let startY = 0

  const cancel = () => {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
  }
  const fire = () => {
    cancel()
    fired = true
    onLongPress()
  }
  const onTouchStart = (e: TouchEvent) => {
    cancel()
    tracking = false
    fired = false
    if (e.touches.length !== 1) return
    tracking = true
    startX = e.touches[0].clientX
    startY = e.touches[0].clientY
    timer = setTimeout(fire, delay)
  }
  const onTouchMove = (e: TouchEvent) => {
    if (!tracking) return
    if (fired) {
      e.stopImmediatePropagation()
      if (e.cancelable) e.preventDefault()
      return
    }
    const t = e.touches[0]
    if (e.touches.length !== 1 || !t || Math.abs(t.clientX - startX) > slop || Math.abs(t.clientY - startY) > slop) {
      cancel()
      tracking = false
    }
  }
  const onTouchEnd = (e: TouchEvent) => {
    if (!tracking) return
    cancel()
    if (fired && e.cancelable) e.preventDefault()
    tracking = false
    fired = false
  }
  const onTouchCancel = () => {
    cancel()
    tracking = false
    fired = false
  }
  const onContextMenu = (e: Event) => {
    if ((e as PointerEvent).pointerType === 'mouse') return
    if (!tracking) return
    e.preventDefault()
    e.stopPropagation()
    if (!fired) fire()
  }

  el.addEventListener('touchstart', onTouchStart, { capture: true, passive: true })
  el.addEventListener('contextmenu', onContextMenu, { capture: true })
  root.addEventListener('touchmove', onTouchMove as EventListener, { capture: true, passive: false })
  root.addEventListener('touchend', onTouchEnd as EventListener, { capture: true, passive: false })
  root.addEventListener('touchcancel', onTouchCancel, { capture: true })
  return () => {
    cancel()
    el.removeEventListener('touchstart', onTouchStart, { capture: true })
    el.removeEventListener('contextmenu', onContextMenu, { capture: true })
    root.removeEventListener('touchmove', onTouchMove as EventListener, { capture: true })
    root.removeEventListener('touchend', onTouchEnd as EventListener, { capture: true })
    root.removeEventListener('touchcancel', onTouchCancel, { capture: true })
  }
}
