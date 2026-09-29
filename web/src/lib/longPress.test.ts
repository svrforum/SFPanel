import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { attachLongPress } from './longPress'

// The suite runs without a DOM, and the helper needs none: EventTargets stand
// in for the element and the window, and the events carry just what the
// listeners read — a type, a touches list, cancelability, a pointer type.
function fire(target: EventTarget, type: string, points: { x: number; y: number }[] = [], extra: Record<string, unknown> = {}) {
  const e = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(e, 'touches', { value: points.map((p) => ({ clientX: p.x, clientY: p.y })) })
  for (const [k, v] of Object.entries(extra)) Object.defineProperty(e, k, { value: v })
  target.dispatchEvent(e)
  return e
}

describe('attachLongPress', () => {
  let el: HTMLElement
  let root: EventTarget
  let fired: number
  let at: [number, number] | null
  let drags: [number, number][]
  let detach: () => void
  // Where the finger goes: touchstart lands on the element, the rest of the
  // gesture is heard on the root (the window), as in a browser.
  const down = (x = 10, y = 10) => fire(el, 'touchstart', [{ x, y }])
  const move = (x: number, y: number) => fire(root, 'touchmove', [{ x, y }])
  const up = () => fire(root, 'touchend')

  beforeEach(() => {
    vi.useFakeTimers()
    el = new EventTarget() as unknown as HTMLElement
    root = new EventTarget()
    fired = 0
    at = null
    drags = []
    detach = attachLongPress(el, (x, y) => { fired++; at = [x, y] }, { root, onDrag: (x, y) => { drags.push([x, y]) } })
  })
  afterEach(() => {
    detach()
    vi.useRealTimers()
  })

  it('fires once a resting finger has held for the delay, where it went down', () => {
    down(30, 40)
    vi.advanceTimersByTime(499)
    expect(fired).toBe(0)
    vi.advanceTimersByTime(1)
    expect(fired).toBe(1)
    expect(at).toEqual([30, 40])
  })

  it('tolerates a finger that drifts within the slop', () => {
    down()
    move(16, 4)
    vi.advanceTimersByTime(500)
    expect(fired).toBe(1)
  })

  // Moves are heard on the root (the window, in the capture phase), ahead of
  // the Android app's script, which stops terminal drags at the document; the
  // e2e suite checks that against the real script.
  it('treats a moving finger as a scroll and does not fire', () => {
    down()
    move(10, 30)
    vi.advanceTimersByTime(1000)
    expect(fired).toBe(0)
  })

  it('does not fire when the finger lifts early, and leaves that tap alone', () => {
    down()
    vi.advanceTimersByTime(200)
    const end = up()
    vi.advanceTimersByTime(1000)
    expect(fired).toBe(0)
    expect(end.defaultPrevented).toBe(false)
  })

  it('ignores a two-finger touch', () => {
    fire(el, 'touchstart', [{ x: 10, y: 10 }, { x: 50, y: 50 }])
    vi.advanceTimersByTime(1000)
    expect(fired).toBe(0)
  })

  it('consumes the gesture it fired: its drag, its click and its context menu', () => {
    const later = vi.fn()
    root.addEventListener('touchmove', later)
    down()
    vi.advanceTimersByTime(500)
    expect(fire(el, 'contextmenu').defaultPrevented).toBe(true)
    const drag = move(10, 80)
    expect(drag.defaultPrevented).toBe(true)
    expect(later).not.toHaveBeenCalled()
    expect(up().defaultPrevented).toBe(true)
    expect(fired).toBe(1)
  })

  it('hands the drag that follows a fired press to onDrag, and only that drag', () => {
    down()
    move(12, 12)
    expect(drags).toEqual([])
    vi.advanceTimersByTime(500)
    move(40, 12)
    move(80, 30)
    expect(drags).toEqual([[40, 12], [80, 30]])
    up()
    move(90, 90)
    expect(drags).toHaveLength(2)
  })

  it('lets everything go once that gesture is over', () => {
    down()
    vi.advanceTimersByTime(500)
    up()
    // A tap on the dialog it opened, and a right click, are not its business.
    expect(fire(root, 'touchend').defaultPrevented).toBe(false)
    expect(fire(el, 'contextmenu', [], { pointerType: 'mouse' }).defaultPrevented).toBe(false)
    expect(fire(el, 'contextmenu').defaultPrevented).toBe(false)
  })

  it('fires early when the platform context menu comes first', () => {
    down()
    vi.advanceTimersByTime(300)
    expect(fire(el, 'contextmenu').defaultPrevented).toBe(true)
    expect(fired).toBe(1)
    vi.advanceTimersByTime(1000)
    expect(fired).toBe(1)
  })

  it("leaves a mouse's right click alone", () => {
    expect(fire(el, 'contextmenu', [], { pointerType: 'mouse' }).defaultPrevented).toBe(false)
    expect(fired).toBe(0)
  })

  it('stops listening after cleanup', () => {
    detach()
    down()
    vi.advanceTimersByTime(1000)
    expect(fired).toBe(0)
  })
})
