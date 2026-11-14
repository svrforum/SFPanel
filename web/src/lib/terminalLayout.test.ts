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
