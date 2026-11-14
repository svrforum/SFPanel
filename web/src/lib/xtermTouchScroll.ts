import type { Terminal } from '@xterm/xterm'

// A drag claims the finger once it has moved this far — the long press's own
// slop (lib/longPress), so a finger is either pressing or scrolling, never both.
const SLOP = 10
// The release velocity is read from the drag's last 100 ms, and a finger that
// rested 40 ms before lifting has none: Android's VelocityTracker rules, so a
// flick here behaves as it does in the phone's own lists. Event times, not the
// times the handlers ran: a busy page delivers a rest and the lift together.
const SAMPLE_MS = 100
const STOPPED_MS = 40
// A fling starts above 0.3 px/ms, is capped at 5, and slows as e^(-t/325 ms):
// it travels v × 325 ms, a couple of screens at the cap.
const FLING_MIN = 0.3
const FLING_MAX = 5
const FLING_TAU = 325
const FLING_END = 0.03

/** How a tmux session takes a touch line exactly. The panel's attach binds a
 *  wheel report with Shift held (internal/feature/ai/attach.go
 *  touchScrollCommands): followed by a plain one, it moves tmux history one
 *  line, or gives a program that takes the wheel that one plain report. */
export interface TmuxTouch {
  /** GET /ai/tools touch_scroll, read at each use: the bundle can arrive after
   *  the session has mounted, and a panel without it must get plain wheels. */
  bound: () => boolean
  /** writes to the session's socket */
  send: (data: string) => void
}

export interface Point { x: number; y: number }

// Set while scrollTerminalLines relays a wheel event of its own, so that the
// handler which turns synthetic wheels into lines lets it through to xterm.
let relaying = false

/**
 * Scrolls by whole lines, negative back into history, as far as a finger
 * travelled: exactly that many in a buffer with scrollback, and in tmux when
 * the attach bound touch scrolling — a Shift wheel report and a plain one per
 * line, aimed at the cell under `at`. Anything else in the alternate buffer
 * (or asking for the mouse) gets the one wheel event earlier panels sent, which
 * xterm turns into a single report: there is no telling how far the program
 * moves for it.
 */
export function scrollTerminalLines(term: Terminal, screen: HTMLElement | null, lines: number, at?: Point, tmux?: TmuxTouch): void {
  if (!lines) return
  if (term.buffer.active.type !== 'alternate' && term.modes.mouseTrackingMode === 'none') {
    term.scrollLines(lines)
    return
  }
  if (!screen || !term.rows || !term.cols || !screen.clientHeight) return
  const rect = screen.getBoundingClientRect()
  const x = at?.x ?? rect.left + rect.width / 2
  const y = at?.y ?? rect.top + rect.height / 2
  if (tmux?.bound()) {
    const col = Math.max(1, Math.min(term.cols, Math.floor((x - rect.left) / (rect.width / term.cols)) + 1))
    const row = Math.max(1, Math.min(term.rows, Math.floor((y - rect.top) / (rect.height / term.rows)) + 1))
    // Buttons 64/65 are wheel up/down; +4 is Shift.
    const up = lines < 0
    tmux.send(`\x1b[<${up ? 68 : 69};${col};${row}M\x1b[<${up ? 64 : 65};${col};${row}M`.repeat(Math.abs(lines)))
    return
  }
  relaying = true
  try {
    screen.dispatchEvent(new WheelEvent('wheel', {
      bubbles: true, cancelable: true, deltaY: lines * (screen.clientHeight / term.rows),
      clientX: Math.max(rect.left + 1, Math.min(rect.right - 1, x)),
      clientY: Math.max(rect.top + 1, Math.min(rect.bottom - 1, y)),
    }))
  } finally {
    relaying = false
  }
}

export interface TouchScrollOptions {
  /** where the lines go; scrollTerminalLines without tmux by default */
  scroll?: (lines: number, at: Point) => void
  /** offered every pixel of travel first, positive towards newer output; true
   *  takes them (a history view moving a pixel at a time), false leaves them
   *  to be counted into lines for scroll. flinging: the pixels are a fling's,
   *  after the finger lifted. */
  pixels?: (px: number, at: Point, flinging: boolean) => boolean
  /** a gesture began: a finger came down on the terminal, or a synthetic wheel */
  onStart?: () => void
  /** that gesture is over: the finger lifted with no fling, a fling came to
   *  rest or was caught, the gesture was abandoned, or the wheel was taken */
  onSettle?: () => void
  /** where the rest of a gesture is heard (the window, in tests a stand-in) */
  root?: EventTarget
  now?: () => number
  frame?: (step: () => void) => number
  cancelFrame?: (id: number) => void
}

/**
 * Makes a vertical drag on the terminal scroll it a line per line of finger
 * travel, and a flick carry on after the finger lifts. xterm v6's viewport is
 * not touch-scrollable, so without this a phone could not reach the history.
 * Pair with `touch-action: none` (Tailwind `touch-none`) on the container so
 * the browser's own gestures cannot preempt it. Returns a cleanup.
 *
 * Only the touchstart is heard on the container. The rest of a gesture is heard
 * on the window in the capture phase, ahead of the Android app's injected
 * script (assets/panel.js), which owns terminal drags from a document-level
 * capture listener: a claimed drag stops there, so the app's older translation
 * does not scroll the same finger a second time. The window listeners exist
 * only while a finger is down, so they never hold up the page's own scrolling,
 * and they are added after the long press's (lib/longPress), which stops a
 * fired press's drag before it gets here.
 *
 * Synthetic wheel events are turned into lines too, up to a page: the app's
 * page buttons (and its own drags, against a panel without this) dispatch one
 * with a deltaY of the pixels they mean, which xterm would cut to one report.
 */
export function attachXtermTouchScroll(container: HTMLElement, term: Terminal, opts: TouchScrollOptions = {}): () => void {
  const root = opts.root ?? window
  const now = opts.now ?? (() => performance.now())
  const frame = opts.frame ?? ((step: () => void) => requestAnimationFrame(step))
  const cancelFrame = opts.cancelFrame ?? ((id: number) => cancelAnimationFrame(id))
  const screenOf = () => container.querySelector('.xterm-screen') as HTMLElement | null
  const scroll = opts.scroll ?? ((lines: number, at: Point) => scrollTerminalLines(term, screenOf(), lines, at))
  // 0 while the terminal is not laid out (a hidden tab): nothing scrolls then.
  const cellPx = () => {
    const screen = screenOf()
    return screen && term.rows ? screen.clientHeight / term.rows : 0
  }
  const timeOf = (e: Event) => e.timeStamp || now()

  let tracking = false // a one-finger gesture that began on the container
  let claimed = false // ...and it is a vertical drag
  let caught = false // ...or it came down on a fling to stop it
  let start: Point = { x: 0, y: 0 }
  let lastY = 0
  let remainder = 0 // px towards newer output not yet scrolled as a line
  let samples: { t: number; y: number }[] = []
  let flingId: number | null = null

  const take = (px: number, flinging = false) => {
    if (opts.pixels?.(px, start, flinging)) return true
    const cell = cellPx()
    if (cell <= 0) return false
    remainder += px
    const lines = Math.trunc(remainder / cell)
    if (lines) {
      remainder -= lines * cell
      scroll(lines, start)
    }
    return true
  }

  // A touch anywhere else stops a fling too, as in any list; heard at the
  // window's bubble phase, after the container's own touchstart has seen it.
  const onAnyTouch = () => stopFling()
  const settle = () => opts.onSettle?.()
  const stopFling = () => {
    if (flingId === null) return
    cancelFrame(flingId)
    flingId = null
    root.removeEventListener('touchstart', onAnyTouch)
    settle()
  }
  const fling = (v0: number) => {
    let v = Math.max(-FLING_MAX, Math.min(FLING_MAX, v0))
    let last = now()
    const step = () => {
      const t = now()
      const decay = Math.exp(-(t - last) / FLING_TAU)
      const laidOut = take(v * FLING_TAU * (1 - decay), true)
      v *= decay
      last = t
      if (Math.abs(v) < FLING_END || !laidOut || !container.isConnected) stopFling()
      else flingId = frame(step)
    }
    flingId = frame(step)
    root.addEventListener('touchstart', onAnyTouch)
  }

  const listen = (on: boolean) => {
    const method = on ? 'addEventListener' : 'removeEventListener'
    root[method]('touchmove', onTouchMove as EventListener, { capture: true, passive: false })
    root[method]('touchend', onTouchEnd as EventListener, { capture: true, passive: false })
    root[method]('touchcancel', onTouchCancel, { capture: true })
  }
  const end = () => {
    if (tracking) listen(false)
    tracking = false
  }

  const onTouchStart = (e: TouchEvent) => {
    caught = flingId !== null
    stopFling()
    if (tracking) {
      end()
      settle()
    }
    claimed = false
    if (e.touches.length !== 1) return
    opts.onStart?.()
    tracking = true
    listen(true)
    start = { x: e.touches[0].clientX, y: e.touches[0].clientY }
    lastY = start.y
    remainder = 0
    samples = [{ t: timeOf(e), y: start.y }]
  }
  const onTouchMove = (e: TouchEvent) => {
    const p = e.touches[0]
    if (e.touches.length !== 1 || !p) {
      end()
      settle()
      return
    }
    if (!claimed) {
      const dx = Math.abs(p.clientX - start.x), dy = Math.abs(p.clientY - start.y)
      if (dx <= SLOP && dy <= SLOP) return
      if (dx > dy) {
        end()
        settle()
        return
      }
      claimed = true
    }
    if (e.cancelable) e.preventDefault()
    e.stopPropagation()
    const t = timeOf(e)
    samples.push({ t, y: p.clientY })
    while (samples.length > 2 && t - samples[0].t > SAMPLE_MS) samples.shift()
    take(lastY - p.clientY)
    lastY = p.clientY
  }
  const onTouchEnd = (e: TouchEvent) => {
    end()
    // No click after a drag, or after a tap that only stopped a fling: the
    // terminal would take focus, raise the keyboard, and tmux would get a click.
    if ((claimed || caught) && e.cancelable) e.preventDefault()
    const t = timeOf(e)
    const first = samples[0], last = samples[samples.length - 1]
    const v = claimed && first && last && t - last.t <= STOPPED_MS && last.t !== first.t
      ? (first.y - last.y) / (last.t - first.t) : 0
    if (Math.abs(v) >= FLING_MIN) fling(v)
    else settle()
  }
  const onTouchCancel = () => {
    end()
    settle()
  }
  const onWheel = (e: WheelEvent) => {
    if (e.isTrusted || relaying) return
    const cell = cellPx()
    const rounded = cell > 0 ? Math.round(e.deltaY / cell) : 0
    if (!rounded) return
    e.preventDefault()
    e.stopPropagation()
    // At most a page less one line, as the key bar pages: a page of pixels
    // rounds up to one row more than fits, which skipped a line every page.
    const lines = Math.sign(rounded) * Math.min(Math.abs(rounded), Math.max(1, term.rows - 1))
    const at = { x: e.clientX, y: e.clientY }
    opts.onStart?.()
    if (!opts.pixels?.(lines * cell, at, false)) scroll(lines, at)
    settle()
  }

  // Every move of a finger on the terminal is taken, the ones this helper
  // leaves alone too (a sideways drag). The browser pans nothing here either
  // way — the container is touch-action: none — but a flick it was not told
  // about it keeps as a fling in progress for a second or two, and the tap
  // that stops a fling is not a click: the next key pressed did nothing.
  const onAnyMove = (e: TouchEvent) => { if (e.cancelable) e.preventDefault() }

  container.addEventListener('touchstart', onTouchStart, { capture: true, passive: true })
  container.addEventListener('touchmove', onAnyMove, { passive: false })
  container.addEventListener('wheel', onWheel, { capture: true, passive: false })
  return () => {
    stopFling()
    end()
    container.removeEventListener('touchstart', onTouchStart, { capture: true })
    container.removeEventListener('touchmove', onAnyMove)
    container.removeEventListener('wheel', onWheel, { capture: true })
  }
}
