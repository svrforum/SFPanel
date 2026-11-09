import { Terminal } from '@xterm/xterm'
import { Unicode11Addon } from '@xterm/addon-unicode11'

/**
 * Where a history view stands `px` pixels back from the bottom of its copy:
 * the buffer line its terminal shows at the top, and how far that terminal is
 * shifted. xterm draws whole rows, so the view's terminal is one row taller
 * than the screen it covers and slides by the fraction of a row the finger is
 * between lines — the top row is hidden at the bottom (shift = -cell) and
 * scrolls into view as the offset grows. maxTop is the top line at the bottom;
 * line 0 is a blank the view keeps hidden, so the top of the history is maxTop
 * whole rows back, with line 1 at the top.
 */
export function historyFrame(px: number, maxTop: number, cell: number): { px: number; top: number; shift: number } {
  const top = Math.max(0, maxTop)
  const p = Math.max(0, Math.min(px, top * cell))
  const back = Math.floor(p / cell)
  return { px: p, top: top - back, shift: -cell + (p - back * cell) }
}

export interface HistoryView {
  /** Moves by px towards newer output (negative: further back); returns the
   *  offset from the bottom the move ended at, 0 at the bottom. */
  move(px: number): number
  offset(): number
  close(): void
}

/**
 * Lays a read-only copy of a tmux session's history (GET
 * /ai/sessions/:id/scrollback) over its live terminal, scrolled to the bottom
 * — the live screen, row for row — for the page to move a pixel at a time
 * (historyFrame). It draws as the live terminal does: its font, size, colours
 * and character widths. A tap on it calls onTap — the page's way back to the
 * live screen; a drag or a caught fling does not click.
 */
export function openHistoryView(host: HTMLElement, live: Terminal, text: string, onTap?: () => void): HistoryView {
  const liveScreen = host.querySelector('.xterm-screen') as HTMLElement | null
  const frameEl = document.createElement('div')
  frameEl.dataset.historyView = ''
  const hostRect = host.getBoundingClientRect()
  const rect = liveScreen?.getBoundingClientRect() ?? hostRect
  // Hidden until the copy is written: xterm parses a write in the background,
  // and an empty view would blank the live screen for those milliseconds.
  Object.assign(frameEl.style, {
    position: 'absolute', zIndex: '5', overflow: 'hidden', touchAction: 'none', visibility: 'hidden',
    left: `${rect.left - hostRect.left}px`, top: `${rect.top - hostRect.top}px`,
    width: `${rect.width}px`, height: `${rect.height}px`,
    background: live.options.theme?.background ?? '',
  })
  // One blank line on top: the view is a row taller than the screen and hides
  // that row at the bottom, so a session with no history beyond its screen
  // would otherwise open a row away from it.
  const lines = ['', ...text.split('\n')]
  const term = new Terminal({
    cols: live.cols,
    rows: live.rows + 1,
    fontSize: live.options.fontSize,
    fontFamily: live.options.fontFamily,
    // The view's own scrollbar would sit on the shifted extra row; the live
    // terminal shows none either.
    theme: { ...live.options.theme, scrollbarSliderBackground: 'transparent', scrollbarSliderHoverBackground: 'transparent', scrollbarSliderActiveBackground: 'transparent' },
    // Rows, not lines: the copy's lines were joined (-J) and wrap again here,
    // and a row past the buffer's end recycles the oldest one. Two cells a
    // character, escape codes counted as text, bounds them from above.
    scrollback: Math.min(100_000, lines.length + 2 * Math.ceil(text.length / Math.max(1, live.cols)) + live.rows),
    disableStdin: true,
    cursorBlink: false,
    allowProposedApi: true,
  })
  const unicode = new Unicode11Addon()
  term.loadAddon(unicode)
  term.unicode.activeVersion = '11'
  host.appendChild(frameEl)
  if (onTap) frameEl.addEventListener('click', onTap)
  term.open(frameEl)
  const termEl = term.element as HTMLElement
  termEl.style.pointerEvents = 'none'
  const cell = () => {
    const screen = frameEl.querySelector('.xterm-screen') as HTMLElement | null
    return screen && term.rows ? screen.clientHeight / term.rows : 0
  }
  let maxTop = 0
  let ready = false
  let closed = false
  let at = 0
  // The shift waiting for xterm to paint new rows. xterm repaints on the next
  // animation frame, and a fling moves the view from inside one, so a shift
  // set at once would land a frame before its rows: the text would jump a row
  // back at every line it crossed. It goes in with the paint instead.
  let pendingShift: number | null = null
  // Shown with the copy's first paint, not when the write is parsed: the rows
  // are painted a frame later, and the view would blank the screen for it.
  let unrevealed = true
  const reveal = () => {
    unrevealed = false
    frameEl.style.visibility = ''
  }
  const shiftTo = (px: number) => { termEl.style.transform = `translateY(${px}px)` }
  term.onRender(() => {
    if (pendingShift !== null) {
      shiftTo(pendingShift)
      pendingShift = null
    }
    if (ready && unrevealed) reveal()
  })
  // Until the copy is written its length is unknown, so a move then (the
  // finger travelled while it loaded) is kept whole and clamped once it is.
  const draw = () => {
    const c = cell()
    if (!ready || c <= 0) return
    const f = historyFrame(at, maxTop, c)
    at = f.px
    frameEl.dataset.offset = String(Math.round(at))
    if (term.buffer.active.viewportY !== f.top) {
      term.scrollToLine(f.top)
      pendingShift = f.shift
    } else if (pendingShift !== null) {
      pendingShift = f.shift
    } else {
      shiftTo(f.shift)
    }
  }
  // Hidden cursor, and no line break after the last row: one would scroll the
  // copy up a row and open the view a row away from the live screen.
  term.write('\x1b[?25l' + lines.join('\r\n'), () => {
    if (closed) return
    maxTop = term.buffer.active.baseY
    ready = true
    draw()
    // In case nothing is left to paint after all.
    setTimeout(() => { if (!closed && unrevealed) reveal() }, 100)
  })
  return {
    move(px: number) {
      if (closed) return 0
      at = Math.max(0, at - px)
      draw()
      return at
    },
    offset: () => at,
    close() {
      if (closed) return
      closed = true
      term.dispose()
      frameEl.remove()
    },
  }
}
