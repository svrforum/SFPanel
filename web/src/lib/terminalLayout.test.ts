import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// No DOM in this suite: a window that only carries events, and a storage that
// can be told to throw, as a private-mode browser's does.
let store: Map<string, string>
let storageThrows: boolean
let win: EventTarget
let layout: typeof import('./terminalLayout')

beforeEach(async () => {
  store = new Map()
  storageThrows = false
  win = new EventTarget()
  vi.stubGlobal('window', win)
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => { if (storageThrows) throw new Error('denied'); return store.get(k) ?? null },
    setItem: (k: string, v: string) => { if (storageThrows) throw new Error('denied'); store.set(k, v) },
  })
  vi.resetModules()
  layout = await import('./terminalLayout')
})
afterEach(() => vi.unstubAllGlobals())

describe('terminal focus', () => {
  it('is off until chosen, and is read back from storage after a reload', async () => {
    expect(layout.readTerminalFocus()).toBe(false)
    layout.setTerminalFocus(true)
    expect(store.get('sfpanel_terminal_focus')).toBe('true')
    vi.resetModules()
    const reloaded = await import('./terminalLayout')
    expect(reloaded.readTerminalFocus()).toBe(true)
  })

  it('tells its listeners, so the app shell follows the header button', () => {
    const heard = vi.fn()
    win.addEventListener('sfpanel:terminal-focus', heard)
    layout.setTerminalFocus(true)
    expect(heard).toHaveBeenCalledTimes(1)
  })

  it('still toggles where storage throws', () => {
    storageThrows = true
    expect(layout.readTerminalFocus()).toBe(false)
    layout.setTerminalFocus(true)
    expect(layout.readTerminalFocus()).toBe(true)
    layout.setTerminalFocus(false)
    expect(layout.readTerminalFocus()).toBe(false)
  })
})

describe('keyboardDismissed', () => {
  it('is the viewport growing by a keyboard at the same width', () => {
    expect(layout.keyboardDismissed({ width: 412, height: 560 }, { width: 412, height: 860 })).toBe(true)
  })
  it('is not the keyboard opening, a toolbar sliding away, or a rotation', () => {
    expect(layout.keyboardDismissed({ width: 412, height: 860 }, { width: 412, height: 560 })).toBe(false)
    // A browser's address bar hides as the page is used: some 56px.
    expect(layout.keyboardDismissed({ width: 412, height: 804 }, { width: 412, height: 860 })).toBe(false)
    // Sideways to upright with the keyboard up: taller, but another width.
    expect(layout.keyboardDismissed({ width: 915, height: 190 }, { width: 412, height: 560 })).toBe(false)
  })
})
