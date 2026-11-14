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
  /** its spoken name, a key under terminal.mobile; the label says enough when absent */
  name?: string
  /** drawn as the one key that stops things */
  danger?: boolean
}

const ESC: BarKey = { label: 'Esc', send: '\x1b', name: 'escape' }
const TAB: BarKey = { label: 'Tab', send: '\t', name: 'tab' }
const UP: BarKey = { label: '↑', send: '\x1b[A', repeat: true, name: 'up' }
const DOWN: BarKey = { label: '↓', send: '\x1b[B', repeat: true, name: 'down' }
const LEFT: BarKey = { label: '←', send: '\x1b[D', repeat: true, name: 'left' }
const RIGHT: BarKey = { label: '→', send: '\x1b[C', repeat: true, name: 'right' }
const INTERRUPT: BarKey = { label: '^C', send: '\x03', name: 'interrupt', danger: true }
const CTRL: BarKey = { label: 'Ctrl', modifier: 'ctrl' }
const ALT: BarKey = { label: 'Alt', modifier: 'alt' }
const SHIFT: BarKey = { label: 'Shift', modifier: 'shift' }
const HOME: BarKey = { label: 'Home', send: '\x1b[H', name: 'home' }
const END: BarKey = { label: 'End', send: '\x1b[F', name: 'end' }
const PAGE_UP: BarKey = { label: 'PgUp', send: '\x1b[5~', name: 'pageUp' }
const PAGE_DOWN: BarKey = { label: 'PgDn', send: '\x1b[6~', name: 'pageDown' }
const char = (c: string): BarKey => ({ label: c, send: c })

export const KEYSETS: Record<KeysetKind, BarKey[][]> = {
  // Stop it, change its permission mode, move through its menus and history,
  // answer it, start a command; then its numbered choices; then the rest.
  agent: [
    [ESC, { label: '⇧Tab', send: '\x1b[Z', name: 'shiftTab' }, UP, DOWN, { label: '⏎', send: '\r', name: 'enter' }, INTERRUPT, char('/')],
    [char('1'), char('2'), char('3'), LEFT, RIGHT, TAB, char('@')],
    [CTRL, ALT, SHIFT, PAGE_UP, PAGE_DOWN, HOME, END],
  ],
  shell: [
    [ESC, TAB, CTRL, UP, DOWN, LEFT, RIGHT],
    [INTERRUPT, { label: '^D', send: '\x04', name: 'endOfInput' }, { label: '^Z', send: '\x1a', name: 'suspend' }, { label: '^R', send: '\x12', name: 'searchHistory' }, char('|'), char('~'), char('/')],
    [HOME, END, PAGE_UP, PAGE_DOWN, SHIFT, ALT, char('-')],
  ],
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
