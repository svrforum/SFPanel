import { describe, expect, it } from 'vitest'
import { SNIPPETS_MAX, SNIPPET_BODY_MAX, snippetBody, snippetRefusal } from './terminalPrefs'
import type { TerminalSnippet } from '@/types/api'

describe('snippetBody', () => {
  it('keeps the words and their line breaks, without the blank edges', () => {
    expect(snippetBody('  run the tests\n\n')).toBe('run the tests')
    expect(snippetBody('one\r\ntwo\rthree')).toBe('one\ntwo\nthree')
    expect(snippetBody(' \n ')).toBe('')
  })
})

describe('snippetRefusal', () => {
  const list: TerminalSnippet[] = [{ kind: 'agent', body: 'review this' }]
  it('takes a new text', () => {
    expect(snippetRefusal(list, { kind: 'agent', body: 'fix the tests' })).toBeNull()
    // The same words for the other kind of session are another snippet.
    expect(snippetRefusal(list, { kind: 'shell', body: 'review this' })).toBeNull()
    expect(snippetRefusal(list, { kind: 'agent', body: '가'.repeat(SNIPPET_BODY_MAX) })).toBeNull()
  })
  it('says why it cannot take one, as the panel would', () => {
    expect(snippetRefusal(list, { kind: 'agent', body: '' })).toBe('empty')
    expect(snippetRefusal(list, { kind: 'agent', body: 'review this' })).toBe('there')
    // Counted in characters, as the panel counts: an emoji is one.
    expect(snippetRefusal(list, { kind: 'agent', body: '가'.repeat(SNIPPET_BODY_MAX + 1) })).toBe('long')
    expect(snippetRefusal(list, { kind: 'agent', body: '😀'.repeat(SNIPPET_BODY_MAX) })).toBeNull()
    const full = Array.from({ length: SNIPPETS_MAX }, (_, i): TerminalSnippet => ({ kind: 'shell', body: `echo ${i}` }))
    expect(snippetRefusal(full, { kind: 'shell', body: 'one more' })).toBe('full')
  })
})
