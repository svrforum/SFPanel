import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Terminal } from '@xterm/xterm'
import { attachXtermTouchScroll, scrollTerminalLines, type TmuxTouch } from './xtermTouchScroll'

// Like longPress.test: no DOM. EventTargets stand in for the container and the
// window, and the events carry just what the listeners read.
const CELL = 16
const ROWS = 40
const COLS = 80

function fakeTerm({ alternate = true, mouse = 'any' }: { alternate?: boolean; mouse?: string } = {}) {
  return {
    rows: ROWS,
    cols: COLS,
    buffer: { active: { type: alternate ? 'alternate' : 'normal' } },
    modes: { mouseTrackingMode: mouse },
    scrollLines: vi.fn(),
  } as unknown as Terminal & { scrollLines: ReturnType<typeof vi.fn> }
}

function fakeScreen() {
  const screen = new EventTarget() as EventTarget & { clientHeight: number; getBoundingClientRect: () => DOMRect }
  screen.clientHeight = ROWS * CELL
  screen.getBoundingClientRect = () => ({ left: 0, top: 100, width: COLS * 10, height: ROWS * CELL, right: COLS * 10, bottom: 100 + ROWS * CELL }) as DOMRect
  return screen
}

describe('attachXtermTouchScroll', () => {
  let screen: ReturnType<typeof fakeScreen>
  let container: HTMLElement
  let root: EventTarget
  let clock: number
  let frames: Map<number, () => void>
  let nextFrame: number
  let scrolled: number[]
  let detach: () => void
  const total = () => scrolled.reduce((a, b) => a + b, 0)
  // An event as the browser hands it over: stamped with when it happened.
  const touch = (target: EventTarget, type: string, points: { x: number; y: number }[] = [], at = clock) => {
    const e = new Event(type, { bubbles: true, cancelable: true })
    Object.defineProperties(e, {
      touches: { value: points.map((p) => ({ clientX: p.x, clientY: p.y })) },
      timeStamp: { value: at },
    })
    const stopped = vi.spyOn(e, 'stopPropagation')
    target.dispatchEvent(e)
    return { prevented: e.defaultPrevented, stopped: stopped.mock.calls.length > 0 }
  }
  // One animation frame, 16 ms on.
  const tick = () => {
    clock += 16
    const pending = [...frames.values()]
    frames.clear()
    for (const step of pending) step()
  }
  const down = (y: number, x = 100) => touch(container, 'touchstart', [{ x, y }])
  const move = (y: number, x = 100) => touch(root, 'touchmove', [{ x, y }])
  const up = () => touch(root, 'touchend')
  // A finger dragging from y0 by dy px in steps of `step` px, 16 ms apart.
  const drag = (y0: number, dy: number, step: number) => {
    down(y0)
    let y = y0
    while (Math.abs(y - y0) < Math.abs(dy)) {
      y = y0 + Math.sign(dy) * Math.min(Math.abs(dy), Math.abs(y - y0) + step)
      clock += 16
      move(y)
    }
    return y
  }
  const flick = () => {
    drag(600, -8 * CELL, 32) // 2 px/ms up, lifted mid-motion
    return up()
  }

  beforeEach(() => {
    screen = fakeScreen()
    container = Object.assign(new EventTarget(), { querySelector: () => screen, isConnected: true }) as unknown as HTMLElement
    root = new EventTarget()
    clock = 1000
    frames = new Map()
    nextFrame = 1
    scrolled = []
    detach = attachXtermTouchScroll(container, fakeTerm(), {
      scroll: (lines) => { scrolled.push(lines) },
      root,
      now: () => clock,
      frame: (step) => { frames.set(nextFrame, step); return nextFrame++ },
      cancelFrame: (id) => { frames.delete(id) },
    })
  })
  afterEach(() => detach())

  // Whatever the move sizes, the lines add up to the finger's travel; what each
  // line becomes is scrollTerminalLines' (below).
  it('moves a line per line of finger travel, slow or fast', () => {
    drag(200, 10 * CELL, 3)
    clock += 100 // rests, then lifts: no fling
    up()
    expect(total()).toBe(-10)

    scrolled = []
    drag(200, 10 * CELL, 45)
    clock += 100
    up()
    expect(total()).toBe(-10)
  })

  it('scrolls towards newer output when the finger moves up', () => {
    drag(400, -6 * CELL, 5)
    clock += 100
    up()
    expect(total()).toBe(6)
  })

  it('leaves a finger within the slop alone — it may be a tap or a long press', () => {
    down(200)
    const within = move(209)
    expect(within).toEqual({ prevented: false, stopped: false })
    const past = move(211)
    expect(past).toEqual({ prevented: true, stopped: true })
  })

  it('does not take a sideways swipe', () => {
    down(200, 100)
    const sideways = move(205, 130)
    expect(sideways.prevented).toBe(false)
    move(260, 140)
    up()
    expect(scrolled).toEqual([])
  })

  it('drops the gesture when a second finger lands', () => {
    down(200)
    move(240)
    touch(root, 'touchmove', [{ x: 100, y: 300 }, { x: 200, y: 300 }])
    move(400)
    expect(total()).toBe(-2) // the 40 px before the second finger, no more
  })

  // The window hears a gesture only while a finger is down on the terminal: a
  // blocking touchmove listener there would hold up every scroll of the page.
  it('listens on the window only while its finger is down', () => {
    const add = vi.spyOn(root, 'addEventListener')
    const remove = vi.spyOn(root, 'removeEventListener')
    const moves = (spy: typeof add) => spy.mock.calls.filter(([type]) => type === 'touchmove').length
    // Nothing on the window yet: a finger elsewhere on the page is not ours.
    expect(move(400)).toEqual({ prevented: false, stopped: false })
    expect(moves(add)).toBe(0)
    down(200)
    expect(moves(add)).toBe(1)
    move(240)
    clock += 100
    up()
    expect(moves(remove)).toBe(1)
    expect(move(400).stopped).toBe(false)
  })

  // A drag's touchend would otherwise click: the terminal takes focus and the
  // keyboard comes up under the finger.
  it('keeps a drag from ending in a click, but not a tap', () => {
    drag(200, 3 * CELL, 8)
    clock += 100
    expect(up().prevented).toBe(true)
    down(200)
    expect(up().prevented).toBe(false)
  })

  it('carries a flick on after the finger lifts, slowing to a stop', () => {
    const atRelease = (() => { drag(600, -8 * CELL, 32); return total() })()
    expect(up().prevented).toBe(true)
    for (let i = 0; i < 200 && frames.size; i++) tick()
    expect(frames.size).toBe(0)
    // 2 px/ms decaying with τ 325 ms travels 650 px: about 40 lines more.
    const carried = total() - atRelease
    expect(carried).toBeGreaterThanOrEqual(38)
    expect(carried).toBeLessThanOrEqual(41)
    expect(scrolled.every((n) => n > 0)).toBe(true)
  })

  it('does not fling a finger that stopped before lifting', () => {
    drag(600, -8 * CELL, 32)
    clock += 40 + 1
    up()
    expect(frames.size).toBe(0)
  })

  // The handlers can run late and back to back on a busy page; when the finger
  // rested is the events' own business.
  it('reads the rest before the lift from the events, not from when they ran', () => {
    drag(600, -8 * CELL, 32)
    touch(root, 'touchend', [], clock + 60) // handled now, lifted 60 ms after the last move
    expect(frames.size).toBe(0)
  })

  it('stops a fling when the finger comes back down, and that tap does not click', () => {
    flick()
    tick()
    const caught = total()
    down(300)
    expect(up().prevented).toBe(true)
    for (let i = 0; i < 50; i++) tick()
    expect(total()).toBe(caught)
    // The fling is over: the next tap is a tap again.
    down(300)
    expect(up().prevented).toBe(false)
  })

  it('stops a fling at a touch anywhere else on the page', () => {
    flick()
    tick()
    const caught = total()
    touch(root, 'touchstart', [{ x: 5, y: 5 }]) // the key bar, say
    for (let i = 0; i < 50; i++) tick()
    expect(total()).toBe(caught)
  })

  // A hidden tab keeps its terminal mounted with nothing laid out: its cells
  // are 0 px high, and a line count divided by that scrolled to the very top.
  it('stops a fling once the terminal is no longer laid out', () => {
    flick()
    tick()
    const before = total()
    screen.clientHeight = 0
    tick()
    expect(frames.size).toBe(0)
    expect(total()).toBe(before)
    expect(scrolled.every(Number.isFinite)).toBe(true)
  })

  // The Android app's page buttons (and its own drags, on a panel without
  // this) dispatch a wheel of the pixels they mean; xterm would make one
  // report of it — one line of Codex for a page.
  it('turns a synthetic wheel into the lines it carries, up to a page, leaving a real one to xterm', () => {
    const wheel = (deltaY: number, isTrusted: boolean) => {
      const e = new Event('wheel', { cancelable: true })
      Object.defineProperties(e, { deltaY: { value: deltaY }, isTrusted: { value: isTrusted }, clientX: { value: 5 }, clientY: { value: 120 } })
      container.dispatchEvent(e)
      return e.defaultPrevented
    }
    expect(wheel(-3 * CELL, false)).toBe(true)
    expect(scrolled).toEqual([-3])
    // A page of pixels rounds to a row more than fits; a page is rows - 1.
    expect(wheel((ROWS + 1) * CELL, false)).toBe(true)
    expect(scrolled).toEqual([-3, ROWS - 1])
    expect(wheel(100, true)).toBe(false)
    expect(scrolled).toEqual([-3, ROWS - 1])
  })
})

describe('scrollTerminalLines', () => {
  const tmux = (bound: boolean) => {
    const sent: string[] = []
    const t: TmuxTouch = { bound: () => bound, send: (d) => { sent.push(d) } }
    return { t, sent }
  }

  it('scrolls a buffer with scrollback itself', () => {
    const term = fakeTerm({ alternate: false, mouse: 'none' })
    scrollTerminalLines(term, fakeScreen() as unknown as HTMLElement, -3)
    expect(term.scrollLines).toHaveBeenCalledWith(-3)
  })

  // The defect this replaced: every line went out as a wheel event, which xterm
  // cuts to one report — and under 50 px to 0.3 of one — so ten lines of finger
  // moved Codex two. Now a Shift wheel report and a plain one per line, at the
  // cell under the finger: the attach's bindings move tmux one line for each
  // pair, or hand the program in the pane the plain report.
  it('sends tmux a Shift wheel report and a plain one per line', () => {
    const { t, sent } = tmux(true)
    scrollTerminalLines(fakeTerm(), fakeScreen() as unknown as HTMLElement, -3, { x: 55, y: 100 + 4 * CELL + 1 }, t)
    expect(sent).toEqual(['\x1b[<68;6;5M\x1b[<64;6;5M'.repeat(3)])
    sent.length = 0
    scrollTerminalLines(fakeTerm(), fakeScreen() as unknown as HTMLElement, 2, { x: -50, y: 9999 }, t)
    expect(sent).toEqual([`\x1b[<69;1;${ROWS}M\x1b[<65;1;${ROWS}M`.repeat(2)])
  })

  // A panel that does not bind the pairs keeps the one wheel event earlier
  // panels sent: its programs would get two wheel reports per line.
  it('sends a panel that binds nothing what it always got', () => {
    class FakeWheel extends Event {
      deltaY: number
      constructor(type: string, init: { deltaY: number }) {
        super(type)
        this.deltaY = init.deltaY
      }
    }
    vi.stubGlobal('WheelEvent', FakeWheel)
    try {
      const { t, sent } = tmux(false)
      const screen = fakeScreen()
      const wheels: number[] = []
      screen.addEventListener('wheel', (e) => { wheels.push((e as FakeWheel).deltaY) })
      scrollTerminalLines(fakeTerm(), screen as unknown as HTMLElement, -2, undefined, t)
      expect(sent).toEqual([])
      expect(wheels).toEqual([-2 * CELL])
    } finally {
      vi.unstubAllGlobals()
    }
  })
})

// The history view takes a gesture's pixels whole, and needs to know where a
// gesture begins and when it is over: the finger lifted, a fling came to rest.
describe('attachXtermTouchScroll hooks', () => {
  let container: HTMLElement
  let root: EventTarget
  let clock: number
  let frames: Map<number, () => void>
  let nextFrame: number
  let log: string[]
  let taken: number
  let take: boolean
  let detach: () => void
  const touch = (target: EventTarget, type: string, points: { x: number; y: number }[] = [], at = clock) => {
    const e = new Event(type, { bubbles: true, cancelable: true })
    Object.defineProperties(e, {
      touches: { value: points.map((p) => ({ clientX: p.x, clientY: p.y })) },
      timeStamp: { value: at },
    })
    target.dispatchEvent(e)
    return e
  }
  const tick = () => {
    clock += 16
    const pending = [...frames.values()]
    frames.clear()
    for (const step of pending) step()
  }
  const drag = (y0: number, dy: number, step: number) => {
    touch(container, 'touchstart', [{ x: 100, y: y0 }])
    for (let y = y0; Math.abs(y - y0) < Math.abs(dy);) {
      y = y0 + Math.sign(dy) * Math.min(Math.abs(dy), Math.abs(y - y0) + step)
      clock += 16
      touch(root, 'touchmove', [{ x: 100, y }])
    }
  }

  beforeEach(() => {
    const screen = fakeScreen()
    container = Object.assign(new EventTarget(), { querySelector: () => screen, isConnected: true }) as unknown as HTMLElement
    root = new EventTarget()
    clock = 1000
    frames = new Map()
    nextFrame = 1
    log = []
    taken = 0
    take = true
    detach = attachXtermTouchScroll(container, fakeTerm(), {
      scroll: (lines) => { log.push(`lines ${lines}`) },
      pixels: (px) => { if (take) taken += px; return take },
      onStart: () => { log.push('start') },
      onSettle: () => { log.push('settle') },
      root,
      now: () => clock,
      frame: (step) => { frames.set(nextFrame, step); return nextFrame++ },
      cancelFrame: (id) => { frames.delete(id) },
    })
  })
  afterEach(() => detach())

  it('gives the pixels to whoever takes them, and counts no lines', () => {
    drag(200, 10 * CELL + 7, 3)
    touch(root, 'touchend', [], clock + 100)
    expect(taken).toBe(-(10 * CELL + 7))
    expect(log).toEqual(['start', 'settle'])
  })

  it('counts lines of what is left', () => {
    take = false
    drag(200, 3 * CELL, 8)
    touch(root, 'touchend', [], clock + 100)
    expect(log).toEqual(['start', 'lines -1', 'lines -1', 'lines -1', 'settle'])
  })

  it('settles a fling when it comes to rest, not when the finger lifts', () => {
    drag(600, -8 * CELL, 32)
    touch(root, 'touchend')
    expect(log).toEqual(['start'])
    for (let i = 0; i < 200 && frames.size; i++) tick()
    expect(log).toEqual(['start', 'settle'])
    expect(taken).toBeGreaterThan(8 * CELL + 30 * CELL)
  })

  it('settles a fling a new touch catches, before that touch starts its own', () => {
    drag(600, -8 * CELL, 32)
    touch(root, 'touchend')
    tick()
    touch(container, 'touchstart', [{ x: 100, y: 300 }])
    expect(log).toEqual(['start', 'settle', 'start'])
  })

  it('settles a gesture it gives up on', () => {
    touch(container, 'touchstart', [{ x: 100, y: 200 }])
    touch(root, 'touchmove', [{ x: 160, y: 205 }]) // sideways
    expect(log).toEqual(['start', 'settle'])
  })

  // The app's page buttons: one synthetic wheel is a whole gesture.
  it('makes a synthetic wheel a gesture of its own, in pixels', () => {
    const e = new Event('wheel', { cancelable: true })
    Object.defineProperties(e, { deltaY: { value: -5 * CELL }, isTrusted: { value: false }, clientX: { value: 5 }, clientY: { value: 120 } })
    container.dispatchEvent(e)
    expect(log).toEqual(['start', 'settle'])
    expect(taken).toBe(-5 * CELL)
  })
})
