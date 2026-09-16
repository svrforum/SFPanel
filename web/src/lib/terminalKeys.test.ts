import { describe, expect, it } from 'vitest'
import { NO_MODIFIERS, terminalKey } from './terminalKeys'

describe('terminal modifier encoding', () => {
  it('supports Shift+Tab and Shift+Enter for AI CLIs', () => {
    expect(terminalKey('\t', { ...NO_MODIFIERS, shift: true })).toBe('\x1b[Z')
    expect(terminalKey('\r', { ...NO_MODIFIERS, shift: true })).toBe('\x1b[13;2u')
  })
  it('combines modifiers for cursor movement', () => {
    expect(terminalKey('\x1b[D', { shift: true, ctrl: true, alt: false })).toBe('\x1b[1;6D')
    expect(terminalKey('\x1b[5~', { shift: false, ctrl: true, alt: false })).toBe('\x1b[5;5~')
  })
  it('encodes controls and symbols without altering composed text', () => {
    expect(terminalKey('c', { ...NO_MODIFIERS, ctrl: true })).toBe('\x03')
    expect(terminalKey('c', { shift: false, ctrl: true, alt: true })).toBe('\x1b\x03')
    expect(terminalKey('1', { ...NO_MODIFIERS, shift: true })).toBe('!')
    expect(terminalKey('안녕하세요', { ...NO_MODIFIERS, shift: true })).toBe('안녕하세요')
    expect(terminalKey('\x1b[200~echo hello\n\x1b[201~', { ...NO_MODIFIERS, ctrl: true })).toBe('\x1b[200~echo hello\n\x1b[201~')
  })
})
