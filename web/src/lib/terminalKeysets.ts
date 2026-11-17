import type { TerminalModifiers } from './terminalKeys'

// The key row under the terminal on a touch device: seven keys a page and a
// cell that turns the page, so nothing scrolls sideways. Which pages depends on
// what the session runs — an AI CLI is steered with other keys than a shell.

export type KeysetKind = 'agent' | 'shell'
export type ModifierName = 'shift' | 'ctrl' | 'alt'
/** 0: off. 1: armed, for the next key only. 2: locked, until tapped again. */
export type ModifierState = 0 | 1 | 2
export type BarModifiers = Record<ModifierName, ModifierState>
export const NO_BAR_MODIFIERS: BarModifiers = { shift: 0, ctrl: 0, alt: 0 }

export interface BarKey {
  /** what the cap shows */
  label: string
  /** the bytes it sends, before any modifier (lib/terminalKeys terminalKey) */
  send?: string
  /** or the modifier it switches */
  modifier?: ModifierName
  /** held down, it repeats */
  repeat?: boolean
  /** its spoken name, a key under terminal.mobile; the label says enough when
   *  absent. Where the cap shows letters the name starts with them, so a voice
   *  user who says "PgUp" finds it (WCAG 2.5.3): "이스케이프" was the name of a
   *  cap reading Esc. */
  name?: string
  /** or the name itself, where it needs no translating ("^B (Ctrl+B)") */
  spoken?: string
  /** drawn as the one key that stops things */
  danger?: boolean
  /** a key of the operator's own: `send` is the text it types */
  own?: boolean
  /** and whether Enter follows that text */
  enter?: boolean
}

/**
 * A key as it is saved (GET /ai/prefs, internal/feature/ai prefs.go): one of
 * the catalog's by its id, or one of the operator's own that types a text.
 */
export type StoredKey = { id: string } | { label: string; text: string; enter?: boolean }
export type StoredKeysets = Partial<Record<KeysetKind, StoredKey[][]>>

// The panel's limits, which the editor keeps to.
export const PAGE_KEYS = 7
export const PAGES_MAX = 6
export const KEY_LABEL_MAX = 8
export const KEY_TEXT_MAX = 64

const NAMED: Record<string, BarKey> = {
  esc: { label: 'Esc', send: '\x1b' },
  tab: { label: 'Tab', send: '\t' },
  shiftTab: { label: '⇧Tab', send: '\x1b[Z', name: 'shiftTab' },
  // Codex inside tmux answers a queued question and edits its last queued
  // message on this; with Shift on one page and ← on another it was four
  // presses nobody was told about.
  shiftLeft: { label: '⇧←', send: '\x1b[1;2D', name: 'shiftLeft' },
  enter: { label: '⏎', send: '\r', name: 'enter' },
  up: { label: '↑', send: '\x1b[A', repeat: true, name: 'up' },
  down: { label: '↓', send: '\x1b[B', repeat: true, name: 'down' },
  left: { label: '←', send: '\x1b[D', repeat: true, name: 'left' },
  right: { label: '→', send: '\x1b[C', repeat: true, name: 'right' },
  home: { label: 'Home', send: '\x1b[H' },
  end: { label: 'End', send: '\x1b[F' },
  pageUp: { label: 'PgUp', send: '\x1b[5~', name: 'pageUp' },
  pageDown: { label: 'PgDn', send: '\x1b[6~', name: 'pageDown' },
  ctrl: { label: 'Ctrl', modifier: 'ctrl' },
  alt: { label: 'Alt', modifier: 'alt' },
  shift: { label: 'Shift', modifier: 'shift' },
}
// Ctrl and a letter as one key. Four of them have a name worth saying.
const CONTROL_NAMES: Record<string, string> = { c: 'interrupt', d: 'endOfInput', z: 'suspend', r: 'searchHistory' }
const LETTERS = 'abcdefghijklmnopqrstuvwxyz'

/** The keys there are to pick from, as the editor groups them. */
export const KEY_CATALOG: { group: 'basic' | 'move' | 'modifier' | 'control'; ids: string[] }[] = [
  { group: 'basic', ids: ['esc', 'tab', 'shiftTab', 'enter'] },
  { group: 'move', ids: ['up', 'down', 'left', 'right', 'shiftLeft', 'home', 'end', 'pageUp', 'pageDown'] },
  { group: 'modifier', ids: ['ctrl', 'alt', 'shift'] },
  { group: 'control', ids: [...LETTERS].map((c) => `ctrl-${c}`) },
]

/** The key a saved one draws as; null for one this page cannot draw. */
export function barKey(key: StoredKey): BarKey | null {
  if (!key || typeof key !== 'object') return null
  if ('id' in key) {
    if (typeof key.id !== 'string') return null
    if (Object.hasOwn(NAMED, key.id)) return NAMED[key.id]
    const letter = /^ctrl-([a-z])$/.exec(key.id)?.[1]
    if (!letter) return null
    return {
      label: `^${letter.toUpperCase()}`,
      send: String.fromCharCode(letter.charCodeAt(0) - 96),
      ...(CONTROL_NAMES[letter] ? { name: CONTROL_NAMES[letter] } : { spoken: `^${letter.toUpperCase()} (Ctrl+${letter.toUpperCase()})` }),
      ...(letter === 'c' ? { danger: true } : {}),
    }
  }
  if (typeof key.label !== 'string' || typeof key.text !== 'string' || !key.label.trim() || !key.text) return null
  return { label: key.label, send: key.text, own: true, enter: key.enter === true }
}

/**
 * What stops a key of the operator's own from being added, or null when it
 * can be. A text short enough for a cap is its own label; a longer one needs a
 * label of its own. Lengths are in characters, as the operator counts them.
 */
export type OwnKeyProblem = 'noText' | 'needsLabel' | 'labelLong' | 'textLong'
export function ownKeyProblem(text: string, label: string): OwnKeyProblem | null {
  const chars = (s: string) => [...s].length
  if (text === '') return 'noText'
  if (chars(text) > KEY_TEXT_MAX) return 'textLong'
  const shown = label.trim() || (chars(text) <= KEY_LABEL_MAX ? text.trim() : '')
  if (shown === '') return 'needsLabel'
  if (chars(shown) > KEY_LABEL_MAX) return 'labelLong'
  return null
}

const char = (c: string): StoredKey => ({ label: c, text: c })
const id = (...ids: string[]): StoredKey[] => ids.map((i) => ({ id: i }))

/** The keys a session has until the operator arranges their own. */
export const DEFAULT_KEYSETS: Record<KeysetKind, StoredKey[][]> = {
  // Stop it, change its permission mode, move through its menus and history,
  // answer it, start a command; then its numbered choices and what it queued;
  // then the rest.
  agent: [
    [...id('esc', 'shiftTab', 'up', 'down', 'enter', 'ctrl-c'), char('/')],
    [char('1'), char('2'), char('3'), ...id('left', 'right', 'shiftLeft', 'tab')],
    id('ctrl', 'alt', 'shift', 'pageUp', 'pageDown', 'home', 'end'),
  ],
  shell: [
    id('esc', 'tab', 'ctrl', 'up', 'down', 'left', 'right'),
    [...id('ctrl-c', 'ctrl-d', 'ctrl-z', 'ctrl-r'), char('|'), char('~'), char('/')],
    [...id('home', 'end', 'pageUp', 'pageDown', 'shift', 'alt'), char('-')],
  ],
}

// What of a saved arrangement this page can draw: no key it does not know, no
// page left empty, nothing past the panel's limits.
function drawable(pages: unknown): StoredKey[][] {
  if (!Array.isArray(pages)) return []
  return pages
    .map((page) => (Array.isArray(page) ? (page as StoredKey[]).filter((k) => barKey(k) !== null).slice(0, PAGE_KEYS) : []))
    .filter((page) => page.length > 0)
    .slice(0, PAGES_MAX)
}
const draw = (pages: StoredKey[][]): BarKey[][] => pages.map((page) => page.map((k) => barKey(k)!))

export const KEYSETS: Record<KeysetKind, BarKey[][]> = {
  agent: draw(DEFAULT_KEYSETS.agent),
  shell: draw(DEFAULT_KEYSETS.shell),
}

/**
 * The arrangement a session of this kind has, as it is saved: what was saved
 * for it, less what this page cannot draw, or the panel's own where that
 * leaves nothing. What the editor starts from.
 */
export function storedPages(kind: KeysetKind, saved: StoredKeysets | undefined): StoredKey[][] {
  const mine = drawable(saved?.[kind])
  return mine.length > 0 ? mine : DEFAULT_KEYSETS[kind]
}

/** The same, as the keys the row draws. */
export function keysetPages(kind: KeysetKind, saved: StoredKeysets | undefined): BarKey[][] {
  return draw(storedPages(kind, saved))
}

/**
 * A cap is some forty pixels wide, and 35 on a 320px screen. What fits it in
 * one line at full size is three characters: at four "⇧Tab" was cut to "⇧Tal"
 * there, and Home and PgUp with it. A longer label — four characters, or an
 * operator's "/compact" — is set smaller and breaks into two lines rather than
 * being cut off.
 */
export function longLabel(label: string): boolean {
  return [...label].length > 3
}
export const LONG_LABEL = 'px-0.5 text-[10px] leading-[1.15] whitespace-normal break-all'

/** A key's name for a screen reader; `t` is the page's translator. */
export function barKeyName(key: BarKey, t: (key: string, options?: Record<string, unknown>) => string): string {
  if (key.modifier) return t('terminal.mobile.modifier', { key: key.modifier })
  if (key.name) return t(`terminal.mobile.${key.name}`)
  return key.spoken ?? key.label
}

/** Whether an arrangement is the panel's own, which is saved as nothing at all. */
export function isDefaultKeyset(kind: KeysetKind, pages: StoredKey[][]): boolean {
  return JSON.stringify(pages) === JSON.stringify(DEFAULT_KEYSETS[kind])
}

/** The AI CLIs get their own keys; a shell, and a temporary shell (no tool), the shell's. */
export function keysetKind(tool: string | undefined): KeysetKind {
  return tool === 'claude' || tool === 'codex' || tool === 'gemini' ? 'agent' : 'shell'
}

/** A tap on a modifier: armed for the next key, then locked, then off. */
export function tapModifier(mods: BarModifiers, name: ModifierName): BarModifiers {
  return { ...mods, [name]: ((mods[name] + 1) % 3) as ModifierState }
}

/** A key went out: the armed modifiers are used up, the locked ones stay. */
export function afterKey(mods: BarModifiers): BarModifiers {
  const keep = (s: ModifierState): ModifierState => (s === 2 ? 2 : 0)
  return { shift: keep(mods.shift), ctrl: keep(mods.ctrl), alt: keep(mods.alt) }
}

/** What applies to the next key, armed or locked. */
export function activeModifiers(mods: BarModifiers): TerminalModifiers {
  return { shift: mods.shift > 0, ctrl: mods.ctrl > 0, alt: mods.alt > 0 }
}
