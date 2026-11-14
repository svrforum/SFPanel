import { describe, expect, it } from 'vitest'
import { KEYSETS, NO_BAR_MODIFIERS, activeModifiers, afterKey, keysetKind, tapModifier } from './terminalKeysets'

describe('keysetKind', () => {
  it('gives the AI CLIs their own keys and everything else the shell\'s', () => {
    expect(keysetKind('claude')).toBe('agent')
    expect(keysetKind('codex')).toBe('agent')
    expect(keysetKind('gemini')).toBe('agent')
    expect(keysetKind('shell')).toBe('shell')
    // A temporary (PTY) shell has no tool at all.
    expect(keysetKind(undefined)).toBe('shell')
  })
})

describe('KEYSETS', () => {
  it('fills every page with seven keys: the eighth cell turns the page', () => {
    for (const pages of Object.values(KEYSETS)) {
      expect(pages.length).toBeGreaterThan(1)
      for (const page of pages) expect(page).toHaveLength(7)
    }
  })

  it('has each key once in a set, so a key is always on the same page', () => {
    for (const pages of Object.values(KEYSETS)) {
      const labels = pages.flat().map((k) => k.label)
      expect(new Set(labels).size).toBe(labels.length)
    }
  })

  it('sends the bytes its caps name', () => {
    const key = (kind: 'agent' | 'shell', label: string) => KEYSETS[kind].flat().find((k) => k.label === label)
    expect(key('agent', 'Esc')?.send).toBe('\x1b')
    // What an AI CLI cycles its permission mode on.
    expect(key('agent', '⇧Tab')?.send).toBe('\x1b[Z')
    expect(key('agent', '⏎')?.send).toBe('\r')
    expect(key('agent', '^C')?.send).toBe('\x03')
    expect(key('shell', '^D')?.send).toBe('\x04')
    expect(key('shell', '^Z')?.send).toBe('\x1a')
    expect(key('shell', '^R')?.send).toBe('\x12')
    expect(key('shell', '↑')).toMatchObject({ send: '\x1b[A', repeat: true })
    expect(key('shell', 'Ctrl')).toMatchObject({ modifier: 'ctrl' })
    // A modifier sends nothing of its own.
    expect(key('shell', 'Ctrl')?.send).toBeUndefined()
  })

  it('keeps what an agent session is steered with on its first page', () => {
    expect(KEYSETS.agent[0].map((k) => k.label)).toEqual(['Esc', '⇧Tab', '↑', '↓', '⏎', '^C', '/'])
  })
})

describe('modifiers', () => {
  it('a tap arms one for the next key, a second locks it, a third lets go', () => {
    const armed = tapModifier(NO_BAR_MODIFIERS, 'ctrl')
    expect(armed.ctrl).toBe(1)
    const locked = tapModifier(armed, 'ctrl')
    expect(locked.ctrl).toBe(2)
    expect(tapModifier(locked, 'ctrl').ctrl).toBe(0)
    // The others are left as they were.
    expect(tapModifier({ shift: 2, ctrl: 0, alt: 1 }, 'ctrl')).toEqual({ shift: 2, ctrl: 1, alt: 1 })
  })

  it('a key uses up the armed ones and leaves the locked', () => {
    expect(afterKey({ shift: 1, ctrl: 2, alt: 0 })).toEqual({ shift: 0, ctrl: 2, alt: 0 })
  })

  it('armed and locked both apply to the key', () => {
    expect(activeModifiers({ shift: 0, ctrl: 1, alt: 2 })).toEqual({ shift: false, ctrl: true, alt: true })
  })
})
