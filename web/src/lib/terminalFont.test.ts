import { describe, expect, it, vi } from 'vitest'
import { MAX_FONT_SIZE, MIN_FONT_SIZE, attachPinch, clampFontSize, pinchSize } from './terminalFont'

// No DOM here: EventTargets stand in for the terminal and the window, and the
// events carry what the listeners read — a touches list and cancelability.
function fire(target: EventTarget, type: string, points: [number, number][]) {
  const e = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(e, 'touches', { value: points.map(([x, y]) => ({ clientX: x, clientY: y })) })
  target.dispatchEvent(e)
  return e
}
function setup(size = 14) {
  const el = new EventTarget()
  const root = new EventTarget()
  const onPreview = vi.fn()
  const onEnd = vi.fn()
  const detach = attachPinch(el as unknown as HTMLElement, { size: () => size, onPreview, onEnd, root })
  return { el, root, onPreview, onEnd, detach }
}

describe('clampFontSize', () => {
  it('keeps a size whole and inside the limits', () => {
    expect(clampFontSize(11.6)).toBe(12)
    expect(clampFontSize(2)).toBe(MIN_FONT_SIZE)
    expect(clampFontSize(90)).toBe(MAX_FONT_SIZE)
  })
})

describe('pinchSize', () => {
  it('scales the size by how far the fingers moved apart', () => {
    expect(pinchSize(14, 200, 100)).toBe(MIN_FONT_SIZE) // 7 is under it
    expect(pinchSize(14, 200, 150)).toBe(11) // 10.5 rounds up
    expect(pinchSize(14, 100, 150)).toBe(21)
    expect(pinchSize(14, 100, 400)).toBe(MAX_FONT_SIZE)
  })
  it('leaves the size alone when the fingers started on one point', () => {
    expect(pinchSize(14, 0, 120)).toBe(14)
  })
})

describe('attachPinch', () => {
  it('previews as two fingers spread and ends on the size they reached', () => {
    const { el, root, onPreview, onEnd } = setup()
    fire(el, 'touchstart', [[100, 300], [200, 300]])
    const move = fire(root, 'touchmove', [[75, 300], [225, 300]])
    expect(onPreview).toHaveBeenLastCalledWith(21)
    // The page must not scroll or zoom under the gesture.
    expect(move.defaultPrevented).toBe(true)
    fire(root, 'touchmove', [[90, 300], [210, 300]])
    expect(onPreview).toHaveBeenLastCalledWith(17)
    fire(root, 'touchend', [[90, 300]])
    expect(onEnd).toHaveBeenCalledTimes(1)
    expect(onEnd).toHaveBeenCalledWith(17)
    // The finger still down lifts later, and that is nothing more.
    fire(root, 'touchend', [])
    expect(onEnd).toHaveBeenCalledTimes(1)
  })

  it('ends with nothing to apply when the size did not change', () => {
    const { el, root, onEnd } = setup()
    fire(el, 'touchstart', [[100, 300], [200, 300]])
    fire(root, 'touchmove', [[100, 302], [200, 302]])
    fire(root, 'touchend', [])
    expect(onEnd).toHaveBeenCalledWith(null)
  })

  it('leaves one finger alone: that is a scroll', () => {
    const { el, root, onPreview, onEnd } = setup()
    fire(el, 'touchstart', [[100, 300]])
    const move = fire(root, 'touchmove', [[100, 200]])
    expect(move.defaultPrevented).toBe(false)
    expect(onPreview).not.toHaveBeenCalled()
    fire(root, 'touchend', [])
    expect(onEnd).not.toHaveBeenCalled()
  })

  it('a third finger ends it where it stood', () => {
    const { el, root, onEnd } = setup()
    fire(el, 'touchstart', [[100, 300], [200, 300]])
    fire(root, 'touchmove', [[50, 300], [250, 300]])
    fire(root, 'touchmove', [[50, 300], [250, 300], [150, 400]])
    expect(onEnd).toHaveBeenCalledWith(24)
    // Over: the fingers that stay do nothing more.
    fire(root, 'touchmove', [[100, 300], [200, 300]])
    expect(onEnd).toHaveBeenCalledTimes(1)
  })

  it('a cancelled gesture applies nothing', () => {
    const { el, root, onEnd } = setup()
    fire(el, 'touchstart', [[100, 300], [200, 300]])
    fire(root, 'touchmove', [[50, 300], [250, 300]])
    fire(root, 'touchcancel', [])
    expect(onEnd).toHaveBeenCalledWith(null)
  })

  it('hears nothing after it is detached', () => {
    const { el, root, onPreview, detach } = setup()
    detach()
    fire(el, 'touchstart', [[100, 300], [200, 300]])
    fire(root, 'touchmove', [[50, 300], [250, 300]])
    expect(onPreview).not.toHaveBeenCalled()
  })
})
