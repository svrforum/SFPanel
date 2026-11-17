import { describe, expect, it } from 'vitest'
import en from '@/i18n/locales/en.json'
import ko from '@/i18n/locales/ko.json'
import { DEFAULT_KEYSETS, KEYSETS, KEY_CATALOG, NO_BAR_MODIFIERS, activeModifiers, afterKey, barKey, barKeyName, isDefaultKeyset, keysetKind, keysetPages, longLabel, tightLabel, TIGHT_LABEL, ownKeyProblem, tapModifier } from './terminalKeysets'

// i18next's t over one locale file, enough for the key names.
function translator(locale: Record<string, unknown>) {
  return (key: string, options?: Record<string, unknown>) => {
    const found = key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown>)?.[part], locale)
    if (typeof found !== 'string') return key
    return found.replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(options?.[name] ?? ''))
  }
}

describe('ownKeyProblem', () => {
  // The editor's Add stayed disabled with nothing saying why: a text longer
  // than a cap needs a label of its own, and both have their limits.
  it('says why a key of the operator\'s own cannot be added yet', () => {
    expect(ownKeyProblem('', '')).toBe('noText')
    // Eight characters: the text is its own label.
    expect(ownKeyProblem('/compact', '')).toBeNull()
    expect(ownKeyProblem('/compact now', '')).toBe('needsLabel')
    expect(ownKeyProblem('/compact now', 'cmp')).toBeNull()
    expect(ownKeyProblem('ls', 'a long label')).toBe('labelLong')
    expect(ownKeyProblem('x'.repeat(65), 'x')).toBe('textLong')
    // Characters are counted, not bytes or UTF-16 units.
    expect(ownKeyProblem('한글여덟자입니다', '')).toBeNull()
    expect(ownKeyProblem('   ', '')).toBe('needsLabel')
  })
})

describe('longLabel', () => {
  it('sets more than four characters smaller, on two lines', () => {
    for (const label of ['Shift', '/compact']) expect(longLabel(label), label).toBe(true)
    for (const label of ['⇧Tab', 'Home', 'PgUp', 'PgDn', 'Ctrl', 'Esc', 'Tab', 'End', 'Alt', '^C', '⇧←', '↑', '/']) expect(longLabel(label), label).toBe(false)
  })

  // On a 320px screen a cap is 35px: "⇧Tab" at full size was cut to "⇧Tal",
  // and Home and PgUp did not fit either. Only there are they set smaller.
  it('marks four characters, which are small only on a narrow screen', () => {
    for (const label of ['⇧Tab', 'Home', 'PgUp', 'PgDn', 'Ctrl']) expect(tightLabel(label), label).toBe(true)
    for (const label of ['Esc', 'Tab', 'Shift', '/compact', '↑']) expect(tightLabel(label), label).toBe(false)
    expect(TIGHT_LABEL).toBe('max-[340px]:text-[10px]')
  })
})

describe('barKeyName', () => {
  // A voice user says what they see ("Esc"), and a name that does not contain
  // it ("이스케이프") cannot be found that way (WCAG 2.5.3). Caps that show only
  // a symbol — an arrow, ⏎ — have no words to match.
  it('contains what the cap shows, in both languages', () => {
    for (const [lang, locale] of [['en', en], ['ko', ko]] as const) {
      const t = translator(locale)
      for (const { ids } of KEY_CATALOG) {
        for (const id of ids) {
          const key = barKey({ id })!
          const visible = key.label.replace(/[⇧⏎↑↓←→]/g, '')
          if (!visible) continue
          expect(barKeyName(key, t).toLowerCase(), `${lang} ${id}`).toContain(visible.toLowerCase())
        }
      }
    }
  })
})

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
    // What Codex inside tmux answers a queued question on, and takes the last
    // queued message back to edit: Shift and the left arrow, as one key.
    expect(key('agent', '⇧←')?.send).toBe('\x1b[1;2D')
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

  it('has Shift and the left arrow next to the arrows, not two pages apart', () => {
    // Shift is on the third page and ← on the second: the combination an AI
    // CLI asks for by name ("shift+← to answer") took a tap, two page turns
    // and another tap, with nothing on screen saying so.
    expect(KEYSETS.agent[1].map((k) => k.label)).toEqual(['1', '2', '3', '←', '→', '⇧←', 'Tab'])
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
    expect(barKey({ id: 'shiftLeft' })).toMatchObject({ label: '⇧←', send: '\x1b[1;2D' })
    // One press, not a movement to hold down.
    expect(barKey({ id: 'shiftLeft' })?.repeat).toBeUndefined()
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
