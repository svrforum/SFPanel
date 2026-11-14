import { describe, expect, it } from 'vitest'
import { DEFAULT_KEYSETS, KEYSETS, KEY_CATALOG, NO_BAR_MODIFIERS, activeModifiers, afterKey, barKey, isDefaultKeyset, keysetKind, keysetPages, tapModifier } from './terminalKeysets'

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

describe('barKey', () => {
  it('knows the keys of the catalog by their id', () => {
    expect(barKey({ id: 'esc' })).toMatchObject({ label: 'Esc', send: '\x1b' })
    expect(barKey({ id: 'shiftTab' })).toMatchObject({ label: '⇧Tab', send: '\x1b[Z' })
    expect(barKey({ id: 'ctrl' })).toMatchObject({ label: 'Ctrl', modifier: 'ctrl' })
    expect(barKey({ id: 'up' })).toMatchObject({ send: '\x1b[A', repeat: true })
  })

  it('makes Ctrl and any letter one key', () => {
    expect(barKey({ id: 'ctrl-a' })).toMatchObject({ label: '^A', send: '\x01' })
    // tmux's prefix, and the one that clears the screen.
    expect(barKey({ id: 'ctrl-b' })).toMatchObject({ label: '^B', send: '\x02' })
    expect(barKey({ id: 'ctrl-l' })).toMatchObject({ label: '^L', send: '\x0c' })
    expect(barKey({ id: 'ctrl-z' })).toMatchObject({ label: '^Z', send: '\x1a' })
    // The one that stops things is drawn as that wherever it is put.
    expect(barKey({ id: 'ctrl-c' })).toMatchObject({ label: '^C', send: '\x03', danger: true })
    expect(barKey({ id: 'ctrl-1' })).toBeNull()
    expect(barKey({ id: 'ctrl-ab' })).toBeNull()
  })

  it('leaves out an id it does not know: a newer panel may have saved it', () => {
    expect(barKey({ id: 'hyper' })).toBeNull()
    expect(barKey({ id: '' })).toBeNull()
    // Nothing of Object.prototype is a key.
    expect(barKey({ id: 'constructor' })).toBeNull()
    expect(barKey({ id: 'toString' })).toBeNull()
  })

  it('takes a key of the operator\'s own as its text', () => {
    expect(barKey({ label: '/compact', text: '/compact', enter: true })).toEqual({ label: '/compact', send: '/compact', own: true, enter: true })
    expect(barKey({ label: '|', text: '|' })).toEqual({ label: '|', send: '|', own: true, enter: false })
    expect(barKey({ label: '', text: 'x' })).toBeNull()
    expect(barKey({ label: 'x', text: '' })).toBeNull()
  })

  it('has every key of the catalog, each once', () => {
    const ids = KEY_CATALOG.flatMap((g) => g.ids)
    expect(new Set(ids).size).toBe(ids.length)
    for (const id of ids) {
      expect(barKey({ id }), id).not.toBeNull()
      // What the panel accepts as an id (internal/feature/ai prefs.go).
      expect(id).toMatch(/^[a-zA-Z0-9-]{1,24}$/)
    }
    // All of Ctrl+letter is there to pick.
    expect(ids.filter((id) => id.startsWith('ctrl-'))).toHaveLength(26)
  })
})

describe('keysetPages', () => {
  it('is the panel\'s own keys where nothing is saved', () => {
    expect(keysetPages('agent', undefined)).toEqual(KEYSETS.agent)
    expect(keysetPages('shell', {})).toEqual(KEYSETS.shell)
    expect(keysetPages('shell', { agent: [[{ id: 'esc' }]] })).toEqual(KEYSETS.shell)
    expect(keysetPages('agent', { agent: [] })).toEqual(KEYSETS.agent)
  })

  it('is what was saved for the kind', () => {
    const pages = keysetPages('agent', { agent: [[{ id: 'esc' }, { label: 'yes', text: 'yes', enter: true }], [{ id: 'ctrl-b' }]] })
    expect(pages.map((p) => p.map((k) => k.label))).toEqual([['Esc', 'yes'], ['^B']])
  })

  it('drops what it cannot draw, and a page left with nothing', () => {
    const pages = keysetPages('shell', { shell: [[{ id: 'hyper' }], [{ id: 'tab' }, { id: 'meta' }]] })
    expect(pages.map((p) => p.map((k) => k.label))).toEqual([['Tab']])
    // Nothing left at all: the panel's own, not a row without keys.
    expect(keysetPages('shell', { shell: [[{ id: 'hyper' }]] })).toEqual(KEYSETS.shell)
    // Not a list at all (a hand-written row).
    expect(keysetPages('shell', { shell: 'x' as never })).toEqual(KEYSETS.shell)
    expect(keysetPages('shell', { shell: [null as never, [null as never, { id: 'tab' }]] }).map((p) => p.map((k) => k.label))).toEqual([['Tab']])
  })

  it('never draws more than seven keys a page: the eighth cell turns it', () => {
    const many = Array.from({ length: 9 }, () => ({ id: 'esc' }))
    expect(keysetPages('agent', { agent: [many] })[0]).toHaveLength(7)
  })
})

describe('isDefaultKeyset', () => {
  it('tells the panel\'s own keys from an arrangement', () => {
    expect(isDefaultKeyset('agent', DEFAULT_KEYSETS.agent)).toBe(true)
    expect(isDefaultKeyset('agent', DEFAULT_KEYSETS.shell)).toBe(false)
    expect(isDefaultKeyset('shell', [...DEFAULT_KEYSETS.shell.slice(0, 2)])).toBe(false)
    // The same keys written out again are still the default.
    expect(isDefaultKeyset('shell', JSON.parse(JSON.stringify(DEFAULT_KEYSETS.shell)))).toBe(true)
  })
})
